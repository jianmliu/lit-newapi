package model

import "testing"

func TestRecordSub2APIPeerUsageCapsInflatedPeerReport(t *testing.T) {
	db := openProviderEndpointDepositTestDB(t)
	usage, err := RecordSub2APIPeerUsage(Sub2APIPeerUsageRecordRequest{
		RequestID:                      "request-1",
		ConsumerUserID:                 10,
		ConsumerTokenID:                20,
		PeerChannelID:                  30,
		ProviderUserID:                 40,
		Model:                          "gpt-4o-mini",
		MappedUpstreamModel:            "provider-model",
		LocalEstimatedPromptTokens:     100,
		LocalEstimatedCompletionTokens: 50,
		PeerReportedPromptTokens:       200,
		PeerReportedCompletionTokens:   100,
	})
	if err != nil {
		t.Fatalf("record peer usage: %v", err)
	}
	if usage.BillablePromptTokens != 100 || usage.BillableCompletionTokens != 50 {
		t.Fatalf("billable tokens = %d/%d, want capped local estimate 100/50", usage.BillablePromptTokens, usage.BillableCompletionTokens)
	}
	if usage.Status != Sub2APIPeerUsageStatusReviewRequired {
		t.Fatalf("usage status = %q, want %q", usage.Status, Sub2APIPeerUsageStatusReviewRequired)
	}
	if usage.UsageDiscrepancyRatio <= 1.20 {
		t.Fatalf("usage_discrepancy_ratio = %f, want > 1.20", usage.UsageDiscrepancyRatio)
	}

	var count int64
	if err := db.Model(&Sub2APIPeerUsage{}).Where("request_id = ?", "request-1").Count(&count).Error; err != nil {
		t.Fatalf("count usage records: %v", err)
	}
	if count != 1 {
		t.Fatalf("usage record count = %d, want 1", count)
	}
}
