package controller_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Withdrawal{}, &model.Log{}, &model.Sub2APISource{}, &model.Sub2APIPeerChannel{}, &model.ProviderEndpointDepositLock{}); err != nil {
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
	suffix := common.GetUUID()
	user := model.User{Username: "buyer-" + suffix, AffCode: suffix, Quota: quota}
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

func getHTTP(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func deleteHTTP(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
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

func TestEstimateSub2APIPeerChannelDepositReportsAvailableAndLockedQuota(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	_, err := model.LockProviderEndpointDeposit(userID, 35, "existing-peer-source")
	if err != nil {
		t.Fatalf("seed deposit lock: %v", err)
	}

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/deposit-estimate", withUserContext(userID, controller.EstimateSub2APIPeerChannelDeposit))

	rr := postJSON(t, r, "/api/sub2api/peer-channels/deposit-estimate", nil, `{"committed_tokens":45,"deposit_policy_id":"peer-default-v1"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("estimate status = %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"required_deposit":45`, `"available_balance":65`, `"locked_balance":35`, `"deposit_policy_id":"peer-default-v1"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("estimate response missing %s: %s", want, rr.Body.String())
		}
	}
}

func TestCreateSub2APIPeerChannel_LocksDepositAndReturnsPendingChannel(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)

	r := gin.New()
	r.POST("/api/sub2api/peer-channels", withUserContext(userID, controller.CreateSub2APIPeerChannel))

	body := `{
		"display_name":"peer one",
		"peer_endpoint_url":"https://peer.example.com/sub2api",
		"backend_id":"backend-1",
		"peer_public_key":"ed25519-public-key",
		"signature_scheme":"ed25519-v1",
		"nonce_window_seconds":120,
		"supported_models":["gpt-4o-mini"],
		"model_mapping":{"gpt-4o-mini":"provider-model"},
		"capacity_config":{"rpm":60},
		"pricing_tier_id":"tier-default",
		"deposit_quota":70,
		"idempotency_key":"peer-channel-create-1"
	}`
	rr := postJSON(t, r, "/api/sub2api/peer-channels", nil, body)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("create status = %d body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"routing_status":"disabled"`, `"verification_status":"pending_verification"`, `"health_status":"unknown"`, `"required_deposit":70`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("create response missing %s: %s", want, rr.Body.String())
		}
	}

	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 70 {
		t.Fatalf("provider_locked_quota after create = %d, want 70", user.ProviderLockedQuota)
	}
	var channel model.Sub2APIPeerChannel
	if err := db.First(&channel, "provider_user_id = ?", userID).Error; err != nil {
		t.Fatalf("reload peer channel: %v", err)
	}
	var lock model.ProviderEndpointDepositLock
	if err := db.First(&lock, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if lock.PeerChannelId != channel.Id || lock.SourceId != 0 || channel.DepositLockId != lock.Id {
		t.Fatalf("lock not attached to peer channel; lock=%+v channel=%+v", lock, channel)
	}
}

func TestCreateSub2APIPeerChannel_IdempotentRetryDoesNotDoubleLock(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)

	r := gin.New()
	r.POST("/api/sub2api/peer-channels", withUserContext(userID, controller.CreateSub2APIPeerChannel))
	body := `{
		"display_name":"peer one",
		"peer_endpoint_url":"https://peer.example.com/sub2api",
		"backend_id":"backend-1",
		"peer_public_key":"ed25519-public-key",
		"supported_models":["gpt-4o-mini"],
		"deposit_quota":70,
		"idempotency_key":"peer-channel-create-idempotent"
	}`

	first := postJSON(t, r, "/api/sub2api/peer-channels", nil, body)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"success":true`) {
		t.Fatalf("first create status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, r, "/api/sub2api/peer-channels", nil, body)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"success":true`) {
		t.Fatalf("second create status = %d body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"idempotent_replay":true`) {
		t.Fatalf("second response missing idempotent replay marker: %s", second.Body.String())
	}

	var channelCount int64
	if err := db.Model(&model.Sub2APIPeerChannel{}).Where("provider_user_id = ?", userID).Count(&channelCount).Error; err != nil {
		t.Fatalf("count peer channels: %v", err)
	}
	if channelCount != 1 {
		t.Fatalf("peer channel count = %d, want 1", channelCount)
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 70 {
		t.Fatalf("provider_locked_quota = %d, want 70", user.ProviderLockedQuota)
	}
}

func TestListSub2APIPeerChannels_ReturnsOnlyCurrentUserChannels(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	otherUserID := seedSub2APITestUser(t, db, 100)
	if _, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "owned peer",
		PeerEndpointURL:    "https://owned-peer.example.com/sub2api",
		BackendID:          "backend-owned",
		PeerPublicKey:      "owned-public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "owned-peer-list",
	}); err != nil {
		t.Fatalf("seed owned peer channel: %v", err)
	}
	if _, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     otherUserID,
		ProviderAccountId:  otherUserID,
		DisplayName:        "other peer",
		PeerEndpointURL:    "https://other-peer.example.com/sub2api",
		BackendID:          "backend-other",
		PeerPublicKey:      "other-public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "other-peer-list",
	}); err != nil {
		t.Fatalf("seed other peer channel: %v", err)
	}

	r := gin.New()
	r.GET("/api/sub2api/peer-channels", withUserContext(userID, controller.ListSub2APIPeerChannels))

	rr := getHTTP(t, r, "/api/sub2api/peer-channels")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("list status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"display_name":"owned peer"`) {
		t.Fatalf("list response missing owned peer: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"display_name":"other peer"`) {
		t.Fatalf("list response leaked other user's peer: %s", rr.Body.String())
	}
}

func TestGetSub2APIPeerChannel_RejectsOtherUserChannel(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	otherUserID := seedSub2APITestUser(t, db, 100)
	otherChannel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     otherUserID,
		ProviderAccountId:  otherUserID,
		DisplayName:        "other peer",
		PeerEndpointURL:    "https://other-peer-detail.example.com/sub2api",
		BackendID:          "backend-other-detail",
		PeerPublicKey:      "other-public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "other-peer-detail",
	})
	if err != nil {
		t.Fatalf("seed other peer channel: %v", err)
	}

	r := gin.New()
	r.GET("/api/sub2api/peer-channels/:id", withUserContext(userID, controller.GetSub2APIPeerChannel))

	rr := getHTTP(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d", otherChannel.Id))
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"success":false`) {
		t.Fatalf("detail should reject other user's peer: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"display_name":"other peer"`) {
		t.Fatalf("detail response leaked other user's peer: %s", rr.Body.String())
	}
}

func TestVerifySub2APIPeerChannel_RecordsOwnershipVerified(t *testing.T) {
	db := openSub2APITestDB(t)
	t.Setenv("SUB2API_PEER_ALLOW_PRIVATE_ENDPOINTS", "1")
	userID := seedSub2APITestUser(t, db, 100)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	var channelID int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sub2api/internal/verify-challenge":
			if r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			var req map[string]string
			if err := common.DecodeJson(r.Body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if req["backend_id"] != "backend-verify" || req["channel_id"] != fmt.Sprintf("%d", channelID) {
				http.Error(w, "unexpected challenge request", http.StatusBadRequest)
				return
			}
			message := strings.Join([]string{req["challenge_id"], req["challenge_nonce"], req["backend_id"], req["channel_id"]}, "\n")
			signature := ed25519.Sign(privateKey, []byte(message))
			_, _ = w.Write([]byte(fmt.Sprintf(`{"challenge_id":%q,"signature_scheme":"ed25519-v1","signature":%q}`, req["challenge_id"], base64.StdEncoding.EncodeToString(signature))))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"online"}`))
		case "/models":
			_, _ = w.Write([]byte(`{"models":["gpt-4o-mini"]}`))
		case "/capacity":
			_, _ = w.Write([]byte(`{"capacity":{"rpm":60}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)

	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "verify peer",
		PeerEndpointURL:    peer.URL,
		BackendID:          "backend-verify",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(publicKey),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "verify-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}
	channelID = channel.Id

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/:id/verify", withUserContext(userID, controller.VerifySub2APIPeerChannel))

	rr := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/verify", channel.Id), nil, `{}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("verify status = %d body=%s", rr.Code, rr.Body.String())
	}
	var reloaded model.Sub2APIPeerChannel
	if err := db.First(&reloaded, channel.Id).Error; err != nil {
		t.Fatalf("reload peer channel: %v", err)
	}
	if reloaded.OwnershipVerifiedAt == 0 {
		t.Fatalf("ownership_verified_at not recorded: %+v", reloaded)
	}
	if reloaded.HealthStatus != model.Sub2APIPeerChannelHealthOnline {
		t.Fatalf("health_status = %q, want %q", reloaded.HealthStatus, model.Sub2APIPeerChannelHealthOnline)
	}
	if reloaded.LastHealthCheckAt == 0 || reloaded.LastSuccessAt == 0 || reloaded.LastFailureAt != 0 {
		t.Fatalf("health timestamps not recorded correctly: %+v", reloaded)
	}
	if reloaded.VerificationStatus != model.Sub2APIPeerChannelVerificationOwnershipVerified {
		t.Fatalf("verification_status = %q, want %q", reloaded.VerificationStatus, model.Sub2APIPeerChannelVerificationOwnershipVerified)
	}
	if reloaded.RoutingStatus != model.Sub2APIPeerChannelRoutingDisabled {
		t.Fatalf("ownership verification must not enable routing; channel=%+v", reloaded)
	}
}

func TestVerifySub2APIPeerChannel_RejectsModelMismatch(t *testing.T) {
	_ = openSub2APITestDB(t)
	t.Setenv("SUB2API_PEER_ALLOW_PRIVATE_ENDPOINTS", "1")
	userID := seedSub2APITestUser(t, model.DB, 100)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	var channelID int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sub2api/internal/verify-challenge":
			var req map[string]string
			if err := common.DecodeJson(r.Body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			message := strings.Join([]string{req["challenge_id"], req["challenge_nonce"], req["backend_id"], req["channel_id"]}, "\n")
			signature := ed25519.Sign(privateKey, []byte(message))
			_, _ = w.Write([]byte(fmt.Sprintf(`{"challenge_id":%q,"signature_scheme":"ed25519-v1","signature":%q}`, req["challenge_id"], base64.StdEncoding.EncodeToString(signature))))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"online"}`))
		case "/models":
			_, _ = w.Write([]byte(`{"models":["peer-other-model"]}`))
		case "/capacity":
			_, _ = w.Write([]byte(`{"capacity":{"rpm":60}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)

	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "mismatch peer",
		PeerEndpointURL:    peer.URL,
		BackendID:          "backend-mismatch",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(publicKey),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{"gpt-4o-mini":"provider-model"}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "verify-peer-model-mismatch",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}
	channelID = channel.Id
	_ = channelID

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/:id/verify", withUserContext(userID, controller.VerifySub2APIPeerChannel))

	rr := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/verify", channel.Id), nil, `{}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":false`) {
		t.Fatalf("verify mismatch status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "model") {
		t.Fatalf("verify mismatch response missing model error: %s", rr.Body.String())
	}
	var reloaded model.Sub2APIPeerChannel
	if err := model.DB.First(&reloaded, channel.Id).Error; err != nil {
		t.Fatalf("reload peer channel: %v", err)
	}
	if reloaded.OwnershipVerifiedAt != 0 || reloaded.VerificationStatus != model.Sub2APIPeerChannelVerificationPending {
		t.Fatalf("model mismatch must not mark ownership verified; channel=%+v", reloaded)
	}
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 || reloaded.DepositLockId != 0 {
		t.Fatalf("pre-activation model mismatch should unlock deposit; user=%+v channel=%+v", user, reloaded)
	}
}

func TestVerifySub2APIPeerChannel_RecordsHealthFailureMetadata(t *testing.T) {
	_ = openSub2APITestDB(t)
	t.Setenv("SUB2API_PEER_ALLOW_PRIVATE_ENDPOINTS", "1")
	userID := seedSub2APITestUser(t, model.DB, 100)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate peer key: %v", err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sub2api/internal/verify-challenge":
			var req map[string]string
			if err := common.DecodeJson(r.Body, &req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			message := strings.Join([]string{req["challenge_id"], req["challenge_nonce"], req["backend_id"], req["channel_id"]}, "\n")
			signature := ed25519.Sign(privateKey, []byte(message))
			_, _ = w.Write([]byte(fmt.Sprintf(`{"challenge_id":%q,"signature_scheme":"ed25519-v1","signature":%q}`, req["challenge_id"], base64.StdEncoding.EncodeToString(signature))))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"offline"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)

	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "offline peer",
		PeerEndpointURL:    peer.URL,
		BackendID:          "backend-offline",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(publicKey),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "verify-peer-health-failure",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/:id/verify", withUserContext(userID, controller.VerifySub2APIPeerChannel))

	rr := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/verify", channel.Id), nil, `{}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":false`) {
		t.Fatalf("verify offline status = %d body=%s", rr.Code, rr.Body.String())
	}
	var reloaded model.Sub2APIPeerChannel
	if err := model.DB.First(&reloaded, channel.Id).Error; err != nil {
		t.Fatalf("reload peer channel: %v", err)
	}
	if reloaded.HealthStatus != model.Sub2APIPeerChannelHealthOffline {
		t.Fatalf("health_status = %q, want %q", reloaded.HealthStatus, model.Sub2APIPeerChannelHealthOffline)
	}
	if reloaded.LastHealthCheckAt == 0 || reloaded.LastFailureAt == 0 || reloaded.LastSuccessAt != 0 {
		t.Fatalf("health failure timestamps not recorded correctly: %+v", reloaded)
	}
	if reloaded.OwnershipVerifiedAt != 0 || reloaded.VerificationStatus != model.Sub2APIPeerChannelVerificationPending {
		t.Fatalf("health failure must not mark ownership verified; channel=%+v", reloaded)
	}
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 || reloaded.DepositLockId != 0 {
		t.Fatalf("pre-activation health failure should unlock deposit; user=%+v channel=%+v", user, reloaded)
	}
}

func TestPauseAndResumeSub2APIPeerChannel_UpdateRoutingStatus(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "pausable peer",
		PeerEndpointURL:    "https://pausable-peer.example.com/sub2api",
		BackendID:          "backend-pausable",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "pause-resume-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/:id/pause", withUserContext(userID, controller.PauseSub2APIPeerChannel))
	r.POST("/api/sub2api/peer-channels/:id/resume", withUserContext(userID, controller.ResumeSub2APIPeerChannel))

	pause := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/pause", channel.Id), nil, `{}`)
	if pause.Code != http.StatusOK || !strings.Contains(pause.Body.String(), `"success":true`) {
		t.Fatalf("pause status = %d body=%s", pause.Code, pause.Body.String())
	}
	var paused model.Sub2APIPeerChannel
	if err := db.First(&paused, channel.Id).Error; err != nil {
		t.Fatalf("reload paused peer channel: %v", err)
	}
	if paused.RoutingStatus != model.Sub2APIPeerChannelRoutingPaused {
		t.Fatalf("routing_status after pause = %q, want %q", paused.RoutingStatus, model.Sub2APIPeerChannelRoutingPaused)
	}

	resume := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/resume", channel.Id), nil, `{}`)
	if resume.Code != http.StatusOK || !strings.Contains(resume.Body.String(), `"success":true`) {
		t.Fatalf("resume status = %d body=%s", resume.Code, resume.Body.String())
	}
	var resumed model.Sub2APIPeerChannel
	if err := db.First(&resumed, channel.Id).Error; err != nil {
		t.Fatalf("reload resumed peer channel: %v", err)
	}
	if resumed.RoutingStatus != model.Sub2APIPeerChannelRoutingDisabled {
		t.Fatalf("routing_status after resume = %q, want %q", resumed.RoutingStatus, model.Sub2APIPeerChannelRoutingDisabled)
	}
}

func TestResumeSub2APIPeerChannel_ActivatesVerifiedHealthyChannel(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	channel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "activatable peer",
		PeerEndpointURL:    "https://activatable-peer.example.com/sub2api",
		BackendID:          "backend-activatable",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "activate-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}
	if err := model.MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
		t.Fatalf("mark ownership verified: %v", err)
	}
	if err := model.UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, model.Sub2APIPeerChannelRoutingPaused); err != nil {
		t.Fatalf("mark paused: %v", err)
	}

	r := gin.New()
	r.POST("/api/sub2api/peer-channels/:id/resume", withUserContext(userID, controller.ResumeSub2APIPeerChannel))

	rr := postJSON(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d/resume", channel.Id), nil, `{}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("resume status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resumed model.Sub2APIPeerChannel
	if err := db.First(&resumed, channel.Id).Error; err != nil {
		t.Fatalf("reload resumed peer channel: %v", err)
	}
	if resumed.RoutingStatus != model.Sub2APIPeerChannelRoutingActive {
		t.Fatalf("routing_status after verified resume = %q, want %q", resumed.RoutingStatus, model.Sub2APIPeerChannelRoutingActive)
	}
}

func TestDeleteSub2APIPeerChannel_UnlocksDeposit(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	channel, lock, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "deletable peer",
		PeerEndpointURL:    "https://deletable-peer.example.com/sub2api",
		BackendID:          "backend-deletable",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "delete-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed peer channel: %v", err)
	}

	r := gin.New()
	r.DELETE("/api/sub2api/peer-channels/:id", withUserContext(userID, controller.DeleteSub2APIPeerChannel))

	rr := deleteHTTP(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d", channel.Id))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":true`) {
		t.Fatalf("delete status = %d body=%s", rr.Code, rr.Body.String())
	}
	var user model.User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 {
		t.Fatalf("provider_locked_quota after delete = %d, want 0", user.ProviderLockedQuota)
	}
	var reloadedLock model.ProviderEndpointDepositLock
	if err := db.First(&reloadedLock, lock.Id).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if reloadedLock.Status != model.ProviderEndpointDepositStatusUnlocked || reloadedLock.UnlockedAmount != 40 {
		t.Fatalf("lock after delete = %+v, want unlocked amount 40", reloadedLock)
	}
	var count int64
	if err := db.Model(&model.Sub2APIPeerChannel{}).Where("id = ?", channel.Id).Count(&count).Error; err != nil {
		t.Fatalf("count peer channel: %v", err)
	}
	if count != 0 {
		t.Fatalf("peer channel count after delete = %d, want 0", count)
	}
}

func TestDeleteSub2APIPeerChannel_RejectsOtherUserChannel(t *testing.T) {
	db := openSub2APITestDB(t)
	userID := seedSub2APITestUser(t, db, 100)
	otherUserID := seedSub2APITestUser(t, db, 100)
	otherChannel, _, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     otherUserID,
		ProviderAccountId:  otherUserID,
		DisplayName:        "other deletable peer",
		PeerEndpointURL:    "https://other-deletable-peer.example.com/sub2api",
		BackendID:          "backend-other-deletable",
		PeerPublicKey:      base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{}`,
		CapacityConfig:     `{}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		IdempotencyKey:     "delete-other-peer-channel",
	})
	if err != nil {
		t.Fatalf("seed other peer channel: %v", err)
	}

	r := gin.New()
	r.DELETE("/api/sub2api/peer-channels/:id", withUserContext(userID, controller.DeleteSub2APIPeerChannel))

	rr := deleteHTTP(t, r, fmt.Sprintf("/api/sub2api/peer-channels/%d", otherChannel.Id))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"success":false`) {
		t.Fatalf("delete other status = %d body=%s", rr.Code, rr.Body.String())
	}
	var count int64
	if err := db.Model(&model.Sub2APIPeerChannel{}).Where("id = ?", otherChannel.Id).Count(&count).Error; err != nil {
		t.Fatalf("count other peer channel: %v", err)
	}
	if count != 1 {
		t.Fatalf("other peer channel count after rejected delete = %d, want 1", count)
	}
	var otherUser model.User
	if err := db.First(&otherUser, otherUserID).Error; err != nil {
		t.Fatalf("reload other user: %v", err)
	}
	if otherUser.ProviderLockedQuota != 40 {
		t.Fatalf("other provider_locked_quota after rejected delete = %d, want 40", otherUser.ProviderLockedQuota)
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
