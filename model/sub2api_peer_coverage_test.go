package model

import "testing"

func TestAutoPauseUnderCoveredSub2APIPeerChannelsPausesNewestFirst(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	userID := seedProviderEndpointDepositUser(t, db, 200, 0)
	first, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "coverage-first"))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, _, err := CreateSub2APIPeerChannelWithDeposit(testPeerChannelCreateRequest(userID, 40, "coverage-second"))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	for _, channel := range []*Sub2APIPeerChannel{first, second} {
		if err := MarkSub2APIPeerChannelOwnershipVerified(channel.Id, userID); err != nil {
			t.Fatalf("mark verified: %v", err)
		}
		if err := UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, Sub2APIPeerChannelRoutingActive); err != nil {
			t.Fatalf("mark active: %v", err)
		}
	}
	if err := db.Model(&User{}).Where("id = ?", userID).Update("provider_locked_quota", 40).Error; err != nil {
		t.Fatalf("simulate coverage shrink: %v", err)
	}

	paused, err := AutoPauseUnderCoveredSub2APIPeerChannels(userID)
	if err != nil {
		t.Fatalf("auto pause under covered channels: %v", err)
	}
	if paused != 1 {
		t.Fatalf("paused count = %d, want 1", paused)
	}
	var reloadedFirst, reloadedSecond Sub2APIPeerChannel
	if err := db.First(&reloadedFirst, first.Id).Error; err != nil {
		t.Fatalf("reload first: %v", err)
	}
	if err := db.First(&reloadedSecond, second.Id).Error; err != nil {
		t.Fatalf("reload second: %v", err)
	}
	if reloadedFirst.RoutingStatus != Sub2APIPeerChannelRoutingActive || reloadedSecond.RoutingStatus != Sub2APIPeerChannelRoutingPaused {
		t.Fatalf("coverage pause order mismatch: first=%+v second=%+v", reloadedFirst, reloadedSecond)
	}
}
