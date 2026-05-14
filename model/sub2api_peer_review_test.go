package model

import "testing"

func TestOpenSub2APIPeerChannelRiskReviewPausesWithoutSlashing(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 100, 0)
	channel, lock, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "review-peer"))
	if err != nil {
		t.Fatalf("create peer channel: %v", err)
	}
	if err := MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if err := UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, Sub2APIPeerChannelRoutingActive); err != nil {
		t.Fatalf("mark active: %v", err)
	}

	review, err := OpenSub2APIPeerChannelRiskReview(channel.Id, userID, 15, "usage discrepancy")
	if err != nil {
		t.Fatalf("open risk review: %v", err)
	}
	if review.Status != Sub2APIPeerReviewStatusOpen || review.PendingSlashAmount != 15 {
		t.Fatalf("review = %+v", review)
	}
	var reloadedLock ProviderEndpointDepositLock
	if err := db.First(&reloadedLock, lock.Id).Error; err != nil {
		t.Fatalf("reload lock: %v", err)
	}
	if reloadedLock.PendingSlashAmount != 15 || reloadedLock.SlashedAmount != 0 || reloadedLock.Status != ProviderEndpointDepositStatusLocked {
		t.Fatalf("lock after review = %+v", reloadedLock)
	}
	var reloadedChannel Sub2APIPeerChannel
	if err := db.First(&reloadedChannel, channel.Id).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reloadedChannel.RoutingStatus != Sub2APIPeerChannelRoutingPaused {
		t.Fatalf("routing_status = %q, want paused", reloadedChannel.RoutingStatus)
	}
}
