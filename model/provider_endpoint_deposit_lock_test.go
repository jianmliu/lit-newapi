package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openProviderEndpointDepositTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := DB
	oldLOGDB := LOG_DB
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &Sub2APISource{}, &Sub2APIPeerChannel{}, &Sub2APIPeerUsage{}, &Sub2APIPeerReview{}, &ProviderEndpointDepositLock{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLOGDB
	})
	return db
}

func seedProviderEndpointDepositUser(t *testing.T, db *gorm.DB, quota int, usedQuota int) int {
	t.Helper()
	suffix := common.GetUUID()
	user := User{Username: "provider-" + suffix, AffCode: suffix, Quota: quota, UsedQuota: usedQuota}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user.Id
}

func TestLockProviderEndpointDepositUsesOnlyAvailableQuota(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 30)

	lock, err := LockProviderEndpointDeposit(userID, 50, "source-create-1")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	if lock.Status != ProviderEndpointDepositStatusLocked {
		t.Fatalf("lock status = %q, want %q", lock.Status, ProviderEndpointDepositStatusLocked)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != 100 || user.UsedQuota != 30 {
		t.Fatalf("lock must not mutate quota/used_quota; user=%+v", user)
	}
	if user.ProviderLockedQuota != 50 {
		t.Fatalf("provider_locked_quota = %d, want 50", user.ProviderLockedQuota)
	}
	available, err := GetUserAvailableQuota(userID)
	if err != nil {
		t.Fatalf("available quota: %v", err)
	}
	if available != 50 {
		t.Fatalf("available quota = %d, want 50", available)
	}

	_, err = LockProviderEndpointDeposit(userID, 51, "source-create-2")
	if !errors.Is(err, ErrInsufficientProviderEndpointDepositQuota) {
		t.Fatalf("second lock error = %v, want ErrInsufficientProviderEndpointDepositQuota", err)
	}
}

func TestProviderEndpointDepositIdempotencyRejectsReplayWhileProvisioning(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)

	_, err := LockProviderEndpointDeposit(userID, 40, "same-source-create")
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	_, err = LockProviderEndpointDeposit(userID, 40, "same-source-create")
	if !errors.Is(err, ErrProviderEndpointDepositProvisionInProgress) {
		t.Fatalf("idempotent in-progress lock error = %v, want ErrProviderEndpointDepositProvisionInProgress", err)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 40 {
		t.Fatalf("provider_locked_quota = %d, want 40", user.ProviderLockedQuota)
	}
}

func TestUnlockProviderEndpointDepositReleasesLockedQuota(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 10)
	lock, err := LockProviderEndpointDeposit(userID, 70, "remove-source")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}

	if err := UnlockProviderEndpointDeposit(lock.Id); err != nil {
		t.Fatalf("unlock deposit: %v", err)
	}
	if err := UnlockProviderEndpointDeposit(lock.Id); err != nil {
		t.Fatalf("idempotent unlock deposit: %v", err)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 {
		t.Fatalf("provider_locked_quota = %d, want 0", user.ProviderLockedQuota)
	}
	available, err := GetUserAvailableQuota(userID)
	if err != nil {
		t.Fatalf("available quota: %v", err)
	}
	if available != 100 {
		t.Fatalf("available quota = %d, want 100", available)
	}
}

func TestDecreaseUserQuotaCannotSpendProviderLockedQuota(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	_, err := LockProviderEndpointDeposit(userID, 80, "locked-source")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}

	err = DecreaseUserQuota(userID, 30, true)
	if !errors.Is(err, ErrInsufficientProviderEndpointDepositQuota) {
		t.Fatalf("decrease quota error = %v, want ErrInsufficientProviderEndpointDepositQuota", err)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != 100 || user.ProviderLockedQuota != 80 {
		t.Fatalf("failed spend should not mutate quota or lock; user=%+v", user)
	}
}

func TestBatchDecreaseUserQuotaCannotSpendProviderLockedQuota(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	_, err := LockProviderEndpointDeposit(userID, 80, "batch-locked-source")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	oldBatch := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })

	err = DecreaseUserQuota(userID, 30, false)
	if !errors.Is(err, ErrInsufficientProviderEndpointDepositQuota) {
		t.Fatalf("batch decrease quota error = %v, want ErrInsufficientProviderEndpointDepositQuota", err)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.Quota != 100 || user.ProviderLockedQuota != 80 {
		t.Fatalf("failed batch spend should not mutate quota or lock; user=%+v", user)
	}
}

func TestAttachProviderEndpointDepositLockCannotOverwriteSource(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 100, 0)
	lock, err := LockProviderEndpointDeposit(userID, 40, "attach-once")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	if err := AttachProviderEndpointDepositLock(lock.Id, 10, "endpoint-1", ""); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	if err := AttachProviderEndpointDepositLock(lock.Id, 11, "endpoint-2", ""); !errors.Is(err, ErrProviderEndpointDepositAlreadyAttached) {
		t.Fatalf("second attach error = %v, want ErrProviderEndpointDepositAlreadyAttached", err)
	}
	var reloaded ProviderEndpointDepositLock
	if err := DB.First(&reloaded, lock.Id).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if reloaded.SourceId != 10 || reloaded.EndpointID != "endpoint-1" {
		t.Fatalf("second attach overwrote lock: %+v", reloaded)
	}
}

func TestAttachProviderEndpointDepositLockToPeerChannelCannotOverwriteOrMixSource(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 100, 0)
	lock, err := LockProviderEndpointDeposit(userID, 40, "attach-peer-once")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	channel := Sub2APIPeerChannel{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        "peer",
		PeerEndpointURL:    "https://peer.example.com",
		BackendID:          "backend-1",
		PeerPublicKey:      "public-key",
		SignatureScheme:    "ed25519-v1",
		NonceWindowSeconds: 60,
		SupportedModels:    `["gpt-4o-mini"]`,
		ModelMapping:       `{"gpt-4o-mini":"provider-model"}`,
		CapacityConfig:     `{"rpm":60}`,
		PricingTierID:      "tier-default",
		RequiredDeposit:    40,
		HealthStatus:       Sub2APIPeerChannelHealthUnknown,
		RoutingStatus:      Sub2APIPeerChannelRoutingDisabled,
		VerificationStatus: Sub2APIPeerChannelVerificationPending,
	}
	if err := DB.Create(&channel).Error; err != nil {
		t.Fatalf("create peer channel: %v", err)
	}

	if err := AttachProviderEndpointDepositLockToPeerChannel(lock.Id, channel.Id, channel.PeerEndpointURL, ""); err != nil {
		t.Fatalf("attach peer channel: %v", err)
	}
	if err := AttachProviderEndpointDepositLockToPeerChannel(lock.Id, channel.Id+1, "https://other.example.com", ""); !errors.Is(err, ErrProviderEndpointDepositAlreadyAttached) {
		t.Fatalf("second peer attach error = %v, want ErrProviderEndpointDepositAlreadyAttached", err)
	}
	if err := AttachProviderEndpointDepositLock(lock.Id, 10, "endpoint-source", ""); !errors.Is(err, ErrProviderEndpointDepositAlreadyAttached) {
		t.Fatalf("source attach after peer attach error = %v, want ErrProviderEndpointDepositAlreadyAttached", err)
	}

	var reloaded ProviderEndpointDepositLock
	if err := DB.First(&reloaded, lock.Id).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if reloaded.PeerChannelId != channel.Id || reloaded.SourceId != 0 || reloaded.EndpointID != channel.PeerEndpointURL {
		t.Fatalf("peer attach state mismatch: %+v", reloaded)
	}
}

func TestProviderEndpointDepositIdempotencyAllowsReplayAfterPeerAttach(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 100, 0)
	lock, err := LockProviderEndpointDeposit(userID, 40, "same-peer-create")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	if err := AttachProviderEndpointDepositLockToPeerChannel(lock.Id, 12, "https://peer.example.com", ""); err != nil {
		t.Fatalf("attach peer channel: %v", err)
	}

	replayed, err := LockProviderEndpointDeposit(userID, 40, "same-peer-create")
	if err != nil {
		t.Fatalf("replay after peer attach: %v", err)
	}
	if replayed.Id != lock.Id || replayed.PeerChannelId != 12 {
		t.Fatalf("replay returned wrong lock: %+v", replayed)
	}
}

func TestDeleteSub2APISourceByIdUnlocksDepositAtomically(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	lock, err := LockProviderEndpointDeposit(userID, 40, "delete-source")
	if err != nil {
		t.Fatalf("lock deposit: %v", err)
	}
	source := Sub2APISource{UserId: userID, Name: "source", EndpointID: "endpoint-delete", PriceMultiplier: 1, Status: Sub2APISourceStatusActive}
	if err := source.Insert(); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if err := AttachProviderEndpointDepositLock(lock.Id, source.Id, source.EndpointID, ""); err != nil {
		t.Fatalf("attach lock: %v", err)
	}

	if err := DeleteSub2APISourceByIdAndUnlockDeposit(source.Id, userID); err != nil {
		t.Fatalf("delete source and unlock: %v", err)
	}
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 {
		t.Fatalf("provider_locked_quota = %d, want 0", user.ProviderLockedQuota)
	}
	var sourceCount int64
	if err := db.Model(&Sub2APISource{}).Where("id = ?", source.Id).Count(&sourceCount).Error; err != nil {
		t.Fatalf("count source: %v", err)
	}
	if sourceCount != 0 {
		t.Fatalf("source count = %d, want deleted", sourceCount)
	}
}
