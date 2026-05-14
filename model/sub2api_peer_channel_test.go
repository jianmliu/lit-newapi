package model

import (
	"errors"
	"testing"
)

func testPeerChannelCreateRequest(userID int, deposit int, idempotencyKey string) Sub2APIPeerChannelCreateRequest {
	return Sub2APIPeerChannelCreateRequest{
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
		RequiredDeposit:    deposit,
		IdempotencyKey:     idempotencyKey,
	}
}

func TestCreateSub2APIPeerChannelLocksDepositAndAttachesLock(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)

	channel, lock, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "peer-create-1"))
	if err != nil {
		t.Fatalf("create peer channel: %v", err)
	}
	if channel.Id == 0 || lock.Id == 0 {
		t.Fatalf("expected persisted channel and lock, channel=%+v lock=%+v", channel, lock)
	}
	if channel.DepositLockId != lock.Id {
		t.Fatalf("channel deposit_lock_id = %d, want %d", channel.DepositLockId, lock.Id)
	}
	if lock.PeerChannelId != channel.Id || lock.SourceId != 0 || lock.Amount != 40 {
		t.Fatalf("lock not attached to peer channel: %+v", lock)
	}
	if channel.RoutingStatus != Sub2APIPeerChannelRoutingDisabled || channel.VerificationStatus != Sub2APIPeerChannelVerificationPending || channel.HealthStatus != Sub2APIPeerChannelHealthUnknown {
		t.Fatalf("unexpected initial statuses: %+v", channel)
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 40 {
		t.Fatalf("provider_locked_quota = %d, want 40", user.ProviderLockedQuota)
	}
}

func TestCreateSub2APIPeerChannelRejectsInsufficientQuotaWithoutRows(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 50, 0)

	_, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 60, "peer-create-too-large"))
	if !errors.Is(err, ErrInsufficientProviderEndpointDepositQuota) {
		t.Fatalf("create peer channel error = %v, want ErrInsufficientProviderEndpointDepositQuota", err)
	}
	var channelCount int64
	if err := db.Model(&Sub2APIPeerChannel{}).Count(&channelCount).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if channelCount != 0 {
		t.Fatalf("channel count = %d, want 0", channelCount)
	}
	var lockCount int64
	if err := db.Model(&ProviderEndpointDepositLock{}).Count(&lockCount).Error; err != nil {
		t.Fatalf("count locks: %v", err)
	}
	if lockCount != 0 {
		t.Fatalf("lock count = %d, want 0", lockCount)
	}
}

func TestCreateSub2APIPeerChannelIdempotentReplayReturnsExistingChannel(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 100, 0)
	req := testPeerChannelCreateRequest(userID, 40, "peer-create-replay")

	firstChannel, firstLock, err := CreateSub2APIPeerChannelWithDeposit(req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	secondChannel, secondLock, err := CreateSub2APIPeerChannelWithDeposit(req)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if secondChannel.Id != firstChannel.Id || secondLock.Id != firstLock.Id {
		t.Fatalf("replay returned different rows: first channel=%+v lock=%+v second channel=%+v lock=%+v", firstChannel, firstLock, secondChannel, secondLock)
	}

	var user User
	if err := DB.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 40 {
		t.Fatalf("provider_locked_quota after replay = %d, want 40", user.ProviderLockedQuota)
	}
}

func TestGetEligibleSub2APIPeerChannelsForModelFiltersActiveVerifiedHealthy(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 200, 0)
	eligible, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "eligible-peer"))
	if err != nil {
		t.Fatalf("create eligible peer: %v", err)
	}
	if err := MarkSub2APIPeerChannelOwnershipVerified(eligible.Id, userID); err != nil {
		t.Fatalf("mark eligible verified: %v", err)
	}
	if err := UpdateSub2APIPeerChannelRoutingStatus(eligible.Id, userID, Sub2APIPeerChannelRoutingActive); err != nil {
		t.Fatalf("mark eligible active: %v", err)
	}

	paused, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "paused-peer"))
	if err != nil {
		t.Fatalf("create paused peer: %v", err)
	}
	if err := MarkSub2APIPeerChannelOwnershipVerified(paused.Id, userID); err != nil {
		t.Fatalf("mark paused verified: %v", err)
	}

	channels, err := GetEligibleSub2APIPeerChannelsForModel("gpt-4o-mini")
	if err != nil {
		t.Fatalf("get eligible peer channels: %v", err)
	}
	if len(channels) != 1 || channels[0].Id != eligible.Id {
		t.Fatalf("eligible channels = %+v, want only channel %d", channels, eligible.Id)
	}
}

func TestGetEligibleSub2APIPeerChannelsForModelAutoPausesUnderCovered(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 200, 0)
	first, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 80, "coverage-peer-first"))
	if err != nil {
		t.Fatalf("create first peer: %v", err)
	}
	second, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 80, "coverage-peer-second"))
	if err != nil {
		t.Fatalf("create second peer: %v", err)
	}
	for _, channel := range []Sub2APIPeerChannel{*first, *second} {
		if err := MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
			t.Fatalf("mark verified: %v", err)
		}
		if err := UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, Sub2APIPeerChannelRoutingActive); err != nil {
			t.Fatalf("mark active: %v", err)
		}
	}
	if err := db.Model(&User{}).Where("id = ?", userID).Update("provider_locked_quota", 80).Error; err != nil {
		t.Fatalf("simulate undercoverage: %v", err)
	}

	channels, err := GetEligibleSub2APIPeerChannelsForModel("gpt-4o-mini")
	if err != nil {
		t.Fatalf("get eligible peer channels: %v", err)
	}
	if len(channels) != 1 || channels[0].Id != first.Id {
		t.Fatalf("eligible channels = %+v, want only first covered channel %d", channels, first.Id)
	}
	var reloadedSecond Sub2APIPeerChannel
	if err := db.First(&reloadedSecond, second.Id).Error; err != nil {
		t.Fatalf("reload second: %v", err)
	}
	if reloadedSecond.RoutingStatus != Sub2APIPeerChannelRoutingPaused {
		t.Fatalf("second routing status = %q, want paused", reloadedSecond.RoutingStatus)
	}
}

func TestGetEligibleSub2APIPeerChannelsForModelSkipsInvalidCapacity(t *testing.T) {
	_ = openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, DB, 100, 0)
	req := testPeerChannelCreateRequest(userID, 40, "invalid-capacity-peer")
	req.CapacityConfig = `{"rpm":0}`
	channel, _, err := CreateSub2APIPeerChannelWithDeposit(req)
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	if err := MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if err := UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, Sub2APIPeerChannelRoutingActive); err != nil {
		t.Fatalf("mark active: %v", err)
	}
	channels, err := GetEligibleSub2APIPeerChannelsForModel("gpt-4o-mini")
	if err != nil {
		t.Fatalf("get eligible: %v", err)
	}
	if len(channels) != 0 {
		t.Fatalf("eligible channels = %+v, want none", channels)
	}
}

func TestDeleteSub2APIPeerChannelByIdAndUnlockDepositRejectsOtherUser(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	otherUserID := seedProviderEndpointDepositUser(t, db, 100, 0)
	channel, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(otherUserID, 40, "other-peer-delete-direct"))
	if err != nil {
		t.Fatalf("create other peer channel: %v", err)
	}

	if err := DeleteSub2APIPeerChannelByIdAndUnlockDeposit(channel.Id, userID); err == nil {
		t.Fatal("delete with wrong user succeeded")
	}
	var other User
	if err := db.First(&other, otherUserID).Error; err != nil {
		t.Fatalf("reload other user: %v", err)
	}
	if other.ProviderLockedQuota != 40 {
		t.Fatalf("other provider_locked_quota = %d, want 40", other.ProviderLockedQuota)
	}
}

func TestUnlockSub2APIPeerChannelDepositReleasesPreActivationLock(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	channel, lock, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "peer-unlock-preactivation"))
	if err != nil {
		t.Fatalf("create peer channel: %v", err)
	}

	if err := UnlockSub2APIPeerChannelDeposit(channel.Id, userID); err != nil {
		t.Fatalf("unlock peer deposit: %v", err)
	}
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.ProviderLockedQuota != 0 {
		t.Fatalf("provider_locked_quota = %d, want 0", user.ProviderLockedQuota)
	}
	var reloadedLock ProviderEndpointDepositLock
	if err := db.First(&reloadedLock, lock.Id).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if reloadedLock.Status != ProviderEndpointDepositStatusUnlocked || reloadedLock.UnlockedAmount != 40 {
		t.Fatalf("lock after unlock = %+v", reloadedLock)
	}
	var reloadedChannel Sub2APIPeerChannel
	if err := db.First(&reloadedChannel, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloadedChannel.DepositLockId != 0 {
		t.Fatalf("deposit_lock_id after unlock = %d, want 0", reloadedChannel.DepositLockId)
	}
}
