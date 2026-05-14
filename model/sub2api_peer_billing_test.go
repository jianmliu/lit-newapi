package model

import "testing"

func TestSettleSub2APIPeerUsageBillingDebitsConsumerAndCreditsProvider(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	consumerID := seedProviderEndpointDepositUser(t, db, 1000, 0)
	providerID := seedProviderEndpointDepositUser(t, db, 100, 0)
	usage, err := RecordSub2APIPeerUsage(Sub2APIPeerUsageRecordRequest{
		RequestID:                      "billing-request-1",
		ConsumerUserID:                 consumerID,
		PeerChannelID:                  30,
		ProviderUserID:                 providerID,
		Model:                          "gpt-4o-mini",
		LocalEstimatedPromptTokens:     100,
		LocalEstimatedCompletionTokens: 50,
		PeerReportedPromptTokens:       100,
		PeerReportedCompletionTokens:   50,
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}

	if err := SettleSub2APIPeerUsageBilling(usage.Id, 60, 45); err != nil {
		t.Fatalf("settle peer usage billing: %v", err)
	}

	var consumer User
	if err := db.First(&consumer, consumerID).Error; err != nil {
		t.Fatalf("reload consumer: %v", err)
	}
	if consumer.Quota != 940 {
		t.Fatalf("consumer quota = %d, want 940", consumer.Quota)
	}
	var provider User
	if err := db.First(&provider, providerID).Error; err != nil {
		t.Fatalf("reload provider: %v", err)
	}
	if provider.Quota != 145 {
		t.Fatalf("provider quota = %d, want 145", provider.Quota)
	}
	var reloaded Sub2APIPeerUsage
	if err := db.First(&reloaded, usage.Id).Error; err != nil {
		t.Fatalf("reload usage: %v", err)
	}
	if reloaded.ChargedAmount != 60 || reloaded.ProviderRevenue != 45 || reloaded.PlatformFee != 15 || reloaded.Status != Sub2APIPeerUsageStatusSettled {
		t.Fatalf("settled usage mismatch: %+v", reloaded)
	}
}
