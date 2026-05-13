package controller_test

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type fakeSub2APIPaymentProvider struct {
	captureTxHash string
	captureErr    error
	payoutTxHash  string
	payoutErr     error
	captureCalls  int
	payoutCalls   int
	payoutTo      string
	payoutAmount  *big.Int
}

func (f *fakeSub2APIPaymentProvider) CaptureX402(_ context.Context, _ string, _ *big.Int) (string, error) {
	f.captureCalls++
	return f.captureTxHash, f.captureErr
}

func (f *fakeSub2APIPaymentProvider) PayoutUSDC(_ context.Context, to string, amount *big.Int) (string, error) {
	f.payoutCalls++
	f.payoutTo = to
	if amount != nil {
		f.payoutAmount = new(big.Int).Set(amount)
	}
	return f.payoutTxHash, f.payoutErr
}

func (f *fakeSub2APIPaymentProvider) Close() {}

func openSub2APITestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Withdrawal{}, &model.Log{}, &model.Sub2APISource{}, &model.ProviderEndpointDepositLock{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func seedSub2APITestUser(t *testing.T, db *gorm.DB, quota int) int {
	t.Helper()
	user := model.User{Username: "buyer", Quota: quota}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.Id
}

func withUserContext(userID int, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("id", userID)
		handler(c)
	}
}

func postJSON(t *testing.T, h http.Handler, path string, headers map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestBuySub2APIQuota_402ChallengeWithoutPayment(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 0)

	r := gin.New()
	r.POST("/api/sub2api/buy", withUserContext(userID, controller.BuySub2APIQuota))

	rr := postJSON(t, r, "/api/sub2api/buy", nil, `{"usdPaid":1.5}`)
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "x402 payment required") {
		t.Fatalf("body missing challenge: %s", rr.Body.String())
	}
	if got := rr.Header().Get("X-PAYMENT-REQUIRED"); !strings.Contains(got, "eip-3009") {
		t.Fatalf("X-PAYMENT-REQUIRED header missing or unexpected: %q", got)
	}
}

func TestBuySub2APIQuota_ProviderSuccessGrantsQuota(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 0)

	fake := &fakeSub2APIPaymentProvider{captureTxHash: "0xfeedfacefeedface"}
	restore := controller.SetSub2APIPaymentProviderForTest(func(context.Context) (controller.Sub2APIPaymentProvider, error) {
		return fake, nil
	})
	t.Cleanup(restore)

	r := gin.New()
	r.POST("/api/sub2api/buy", withUserContext(userID, controller.BuySub2APIQuota))

	rr := postJSON(t, r, "/api/sub2api/buy",
		map[string]string{"X-PAYMENT": "eip-3009-fake-authorization"},
		`{"usdPaid":2}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "0xfeedfacefeedface") {
		t.Fatalf("body missing settlement tx hash: %s", rr.Body.String())
	}
	if fake.captureCalls != 1 {
		t.Fatalf("CaptureX402 called %d times, want 1", fake.captureCalls)
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota == 0 {
		t.Fatalf("user quota not incremented; row=%+v", user)
	}
}

func TestBuySub2APIQuota_ProviderErrorReturns502(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 0)

	fake := &fakeSub2APIPaymentProvider{captureErr: fmt.Errorf("signature replay rejected")}
	restore := controller.SetSub2APIPaymentProviderForTest(func(context.Context) (controller.Sub2APIPaymentProvider, error) {
		return fake, nil
	})
	t.Cleanup(restore)

	r := gin.New()
	r.POST("/api/sub2api/buy", withUserContext(userID, controller.BuySub2APIQuota))

	rr := postJSON(t, r, "/api/sub2api/buy",
		map[string]string{"X-PAYMENT": "eip-3009-fake-authorization"},
		`{"usdPaid":2}`)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "signature replay rejected") {
		t.Fatalf("body missing provider error: %s", rr.Body.String())
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != 0 {
		t.Fatalf("user quota incremented despite capture failure; row=%+v", user)
	}
}

func TestBuySub2APIQuota_ProviderNotConfiguredReturns503(t *testing.T) {
	_ = openSub2APITestDB(t)
	userID := 1

	r := gin.New()
	r.POST("/api/sub2api/buy", withUserContext(userID, controller.BuySub2APIQuota))

	rr := postJSON(t, r, "/api/sub2api/buy",
		map[string]string{"X-PAYMENT": "eip-3009-fake-authorization"},
		`{"usdPaid":2}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not configured") {
		t.Fatalf("body missing configuration error: %s", rr.Body.String())
	}
}

func TestCreateSub2APISource_LocksDepositAndDeleteUnlocks(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)

	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-admin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/keys") {
			_, _ = w.Write([]byte(`{"token":"runtime-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(management.Close)
	t.Setenv("SUB2API_MANAGEMENT_BASE_URL", management.URL)
	t.Setenv("SUB2API_MANAGEMENT_ADMIN_BEARER", "test-admin")

	r := gin.New()
	r.POST("/api/sub2api/sources", withUserContext(userID, controller.CreateSub2APISource))
	r.DELETE("/api/sub2api/sources/:id", withUserContext(userID, controller.DeleteSub2APISource))

	createBody := `{
		"name":"provider source",
		"provider":"openai",
		"model":"gpt-4o-mini",
		"base_url":"https://api.openai.com/v1",
		"api_key":"sk-test",
		"credential_type":"api_key",
		"deposit_quota":80,
		"idempotency_key":"source-create-1"
	}`
	rr := postJSON(t, r, "/api/sub2api/sources", nil, createBody)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("create status = %d body=%s", rr.Code, rr.Body.String())
	}

	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 80 {
		t.Fatalf("provider_locked_quota after create = %d, want 80", user.ProviderLockedQuota)
	}

	var source model.Sub2APISource
	if err := db.First(&source, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("reload source: %v", err)
	}
	var lock model.ProviderEndpointDepositLock
	if err := db.First(&lock, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if lock.SourceId != source.Id || lock.EndpointID != source.EndpointID {
		t.Fatalf("lock not attached to source; lock=%+v source=%+v", lock, source)
	}

	req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/sub2api/sources/%d", source.Id), nil)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("delete status = %d body=%s", rr.Code, rr.Body.String())
	}
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user after delete: %v", err)
	}
	if user.ProviderLockedQuota != 0 {
		t.Fatalf("provider_locked_quota after delete = %d, want 0", user.ProviderLockedQuota)
	}
}

func TestCreateSub2APISource_IdempotentRetryDoesNotDoubleLockOrProvision(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	managementCalls := 0
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		managementCalls++
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/keys") {
			_, _ = w.Write([]byte(`{"token":"runtime-token"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(management.Close)
	t.Setenv("SUB2API_MANAGEMENT_BASE_URL", management.URL)
	t.Setenv("SUB2API_MANAGEMENT_ADMIN_BEARER", "test-admin")

	r := gin.New()
	r.POST("/api/sub2api/sources", withUserContext(userID, controller.CreateSub2APISource))
	body := `{
		"name":"provider source",
		"provider":"openai",
		"model":"gpt-4o-mini",
		"base_url":"https://api.openai.com/v1",
		"api_key":"sk-test",
		"credential_type":"api_key",
		"deposit_quota":80,
		"idempotency_key":"source-create-idempotent"
	}`

	first := postJSON(t, r, "/api/sub2api/sources", nil, body)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"success":true`) {
		t.Fatalf("first create status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, r, "/api/sub2api/sources", nil, body)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"success":true`) {
		t.Fatalf("second create status = %d body=%s", second.Code, second.Body.String())
	}
	if managementCalls != 3 {
		t.Fatalf("management calls = %d, want 3 (no retry provisioning)", managementCalls)
	}
	var sourceCount int64
	if err := db.Model(&model.Sub2APISource{}).Where("user_id = ?", userID).Count(&sourceCount).Error; err != nil {
		t.Fatalf("count sources: %v", err)
	}
	if sourceCount != 1 {
		t.Fatalf("source count = %d, want 1", sourceCount)
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 80 {
		t.Fatalf("provider_locked_quota = %d, want 80", user.ProviderLockedQuota)
	}
}

func TestEstimateSub2APISourceDepositReportsAvailableAndLockedQuota(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	_, err := model.LockProviderEndpointDeposit(userID, 40, "existing-source")
	if err != nil {
		t.Fatalf("seed deposit lock: %v", err)
	}

	r := gin.New()
	r.POST("/api/sub2api/sources/deposit-estimate", withUserContext(userID, controller.EstimateSub2APISourceDeposit))

	rr := postJSON(t, r, "/api/sub2api/sources/deposit-estimate", nil, `{"committed_tokens":50,"deposit_policy_id":"default-v1"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("estimate status = %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"required_deposit":50`, `"available_balance":60`, `"locked_balance":40`, `"deposit_policy_id":"default-v1"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("estimate response missing %s: %s", want, rr.Body.String())
		}
	}
}

func TestWithdrawal_RejectRefundsQuota(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 1000)

	r := gin.New()
	r.POST("/api/user/withdrawals", withUserContext(userID, controller.CreateWithdrawal))
	r.PUT("/api/withdrawal/:id", controller.UpdateWithdrawal)

	rr := postJSON(t, r, "/api/user/withdrawals", nil, `{"quota":600,"currency":"USDC","payout_method":"manual","payout_account":"0xreceiver"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("submit status = %d body=%s", rr.Code, rr.Body.String())
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != 400 {
		t.Fatalf("user quota after submit = %d, want 400", user.Quota)
	}
	var w model.Withdrawal
	if err := db.First(&w).Error; err != nil {
		t.Fatalf("reload withdrawal: %v", err)
	}
	rejReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/withdrawal/%d", w.Id), strings.NewReader(`{"status":"rejected","remark":"declined"}`))
	rejReq.Header.Set("Content-Type", "application/json")
	rejRR := httptest.NewRecorder()
	r.ServeHTTP(rejRR, rejReq)
	if rejRR.Code != http.StatusOK || !strings.Contains(rejRR.Body.String(), `"success":true`) {
		t.Fatalf("reject status = %d body=%s", rejRR.Code, rejRR.Body.String())
	}
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user after reject: %v", err)
	}
	if user.Quota != 1000 {
		t.Fatalf("user quota after reject = %d, want 1000 (full refund)", user.Quota)
	}
}

func TestWithdrawal_ApproveManualSettlesWithoutProvider(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 1000)
	w := model.Withdrawal{
		UserId:        userID,
		Quota:         300,
		Amount:        0.0006,
		Currency:      "USDC",
		PayoutMethod:  "manual",
		PayoutAccount: "off-chain-ref",
		Status:        model.WithdrawalStatusPending,
		CreatedTime:   common.GetTimestamp(),
		UpdatedTime:   common.GetTimestamp(),
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("seed withdrawal: %v", err)
	}
	r := gin.New()
	r.PUT("/api/withdrawal/:id", controller.UpdateWithdrawal)

	rr := postJSON(t, r, fmt.Sprintf("/api/withdrawal/%d", w.Id), nil, `{"status":"approved","remark":"offline settled"}`)
	httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/withdrawal/%d", w.Id), strings.NewReader(`{"status":"approved","remark":"offline settled"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}
	var updated model.Withdrawal
	if err := db.First(&updated, w.Id).Error; err != nil {
		t.Fatalf("reload withdrawal: %v", err)
	}
	if updated.Status != model.WithdrawalStatusApproved {
		t.Fatalf("withdrawal status = %q, want approved", updated.Status)
	}
	if updated.TxHash != "" {
		t.Fatalf("manual approve must not set tx_hash; got %q", updated.TxHash)
	}
}

func TestWithdrawal_ApproveNonManualWithoutProviderFails(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 1000)
	w := model.Withdrawal{
		UserId:        userID,
		Quota:         300,
		Amount:        0.0006,
		Currency:      "USDC",
		PayoutMethod:  "base_usdc",
		PayoutAccount: "0xreceiver",
		Status:        model.WithdrawalStatusPending,
		CreatedTime:   common.GetTimestamp(),
		UpdatedTime:   common.GetTimestamp(),
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("seed withdrawal: %v", err)
	}
	r := gin.New()
	r.PUT("/api/withdrawal/:id", controller.UpdateWithdrawal)

	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/withdrawal/%d", w.Id), strings.NewReader(`{"status":"approved","remark":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"success":false`) || !strings.Contains(rr.Body.String(), "not configured") {
		t.Fatalf("approve must reject when provider missing; body=%s", rr.Body.String())
	}
	var reloaded model.Withdrawal
	if err := db.First(&reloaded, w.Id).Error; err != nil {
		t.Fatalf("reload withdrawal: %v", err)
	}
	if reloaded.Status != model.WithdrawalStatusPending {
		t.Fatalf("withdrawal status = %q, want pending (no settlement path)", reloaded.Status)
	}
}

func TestWithdrawal_ApproveBaseUSDCPayoutConvertsQuotaToUSDCAtoms(t *testing.T) {
	db := openSub2APITestDB(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })
	userID := seedSub2APITestUser(t, db, 1_000_000)
	withdrawal, err := model.CreateWithdrawal(context.Background(), userID, 500_000, "USDC", "base_usdc", "0x1000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("create withdrawal: %v", err)
	}

	fake := &fakeSub2APIPaymentProvider{payoutTxHash: "0xpayout"}
	restore := controller.SetSub2APIPaymentProviderForTest(func(context.Context) (controller.Sub2APIPaymentProvider, error) {
		return fake, nil
	})
	t.Cleanup(restore)

	r := gin.New()
	r.PUT("/api/withdrawal/:id", controller.UpdateWithdrawal)
	req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/withdrawal/%d", withdrawal.Id), strings.NewReader(`{"status":"approved","remark":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}
	if fake.payoutCalls != 1 {
		t.Fatalf("PayoutUSDC called %d times, want 1", fake.payoutCalls)
	}
	if fake.payoutTo != "0x1000000000000000000000000000000000000001" {
		t.Fatalf("payout to = %q", fake.payoutTo)
	}
	if fake.payoutAmount == nil || fake.payoutAmount.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Fatalf("payout amount = %v, want 1000000 USDC atoms", fake.payoutAmount)
	}
	var updated model.Withdrawal
	if err := db.First(&updated, withdrawal.Id).Error; err != nil {
		t.Fatalf("reload withdrawal: %v", err)
	}
	if updated.Status != model.WithdrawalStatusApproved || updated.TxHash != "0xpayout" {
		t.Fatalf("withdrawal after payout = %+v", updated)
	}
}
