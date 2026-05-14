package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	Sub2APIPeerUsageStatusAccepted       = "accepted"
	Sub2APIPeerUsageStatusReviewRequired = "review_required"
	Sub2APIPeerUsageStatusSettled        = "settled"

	sub2APIPeerUsageAllowedDiscrepancyMultiplier = 1.20
)

type Sub2APIPeerUsage struct {
	Id                             int     `json:"id"`
	RequestID                      string  `json:"request_id" gorm:"type:varchar(191);uniqueIndex"`
	ConsumerUserID                 int     `json:"consumer_user_id" gorm:"index"`
	ConsumerTokenID                int     `json:"consumer_token_id" gorm:"index"`
	PeerChannelID                  int     `json:"peer_channel_id" gorm:"index"`
	ProviderUserID                 int     `json:"provider_user_id" gorm:"index"`
	Model                          string  `json:"model" gorm:"type:varchar(191);index"`
	MappedUpstreamModel            string  `json:"mapped_upstream_model" gorm:"type:varchar(191)"`
	LocalEstimatedPromptTokens     int     `json:"local_estimated_prompt_tokens" gorm:"type:int;default:0"`
	LocalEstimatedCompletionTokens int     `json:"local_estimated_completion_tokens" gorm:"type:int;default:0"`
	PeerReportedPromptTokens       int     `json:"peer_reported_prompt_tokens" gorm:"type:int;default:0"`
	PeerReportedCompletionTokens   int     `json:"peer_reported_completion_tokens" gorm:"type:int;default:0"`
	BillablePromptTokens           int     `json:"billable_prompt_tokens" gorm:"type:int;default:0"`
	BillableCompletionTokens       int     `json:"billable_completion_tokens" gorm:"type:int;default:0"`
	UsageDiscrepancyRatio          float64 `json:"usage_discrepancy_ratio" gorm:"type:double;default:0"`
	ChargedAmount                  int     `json:"charged_amount" gorm:"type:int;default:0"`
	ProviderRevenue                int     `json:"provider_revenue" gorm:"type:int;default:0"`
	PlatformFee                    int     `json:"platform_fee" gorm:"type:int;default:0"`
	Status                         string  `json:"status" gorm:"type:varchar(32);index"`
	CreatedTime                    int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime                    int64   `json:"updated_time" gorm:"bigint"`
}

func SettleSub2APIPeerUsageBilling(usageID int, chargedAmount int, providerRevenue int) error {
	if usageID <= 0 {
		return gorm.ErrRecordNotFound
	}
	if chargedAmount < 0 || providerRevenue < 0 || providerRevenue > chargedAmount {
		return errors.New("invalid peer usage settlement amounts")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var usage Sub2APIPeerUsage
		if err := tx.First(&usage, usageID).Error; err != nil {
			return err
		}
		if usage.Status == Sub2APIPeerUsageStatusSettled {
			return nil
		}
		if chargedAmount > 0 {
			result := tx.Model(&User{}).Where("id = ? AND quota - provider_locked_quota >= ?", usage.ConsumerUserID, chargedAmount).Update("quota", gorm.Expr("quota - ?", chargedAmount))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrInsufficientProviderEndpointDepositQuota
			}
		}
		if providerRevenue > 0 {
			if err := tx.Model(&User{}).Where("id = ?", usage.ProviderUserID).Update("quota", gorm.Expr("quota + ?", providerRevenue)).Error; err != nil {
				return err
			}
		}
		now := common.GetTimestamp()
		return tx.Model(&Sub2APIPeerUsage{}).Where("id = ?", usageID).Updates(map[string]interface{}{
			"charged_amount":   chargedAmount,
			"provider_revenue": providerRevenue,
			"platform_fee":     chargedAmount - providerRevenue,
			"status":           Sub2APIPeerUsageStatusSettled,
			"updated_time":     now,
		}).Error
	})
}

type Sub2APIPeerUsageRecordRequest struct {
	RequestID                      string
	ConsumerUserID                 int
	ConsumerTokenID                int
	PeerChannelID                  int
	ProviderUserID                 int
	Model                          string
	MappedUpstreamModel            string
	LocalEstimatedPromptTokens     int
	LocalEstimatedCompletionTokens int
	PeerReportedPromptTokens       int
	PeerReportedCompletionTokens   int
}

func RecordSub2APIPeerUsage(request Sub2APIPeerUsageRecordRequest) (*Sub2APIPeerUsage, error) {
	localTotal := request.LocalEstimatedPromptTokens + request.LocalEstimatedCompletionTokens
	peerTotal := request.PeerReportedPromptTokens + request.PeerReportedCompletionTokens
	status := Sub2APIPeerUsageStatusAccepted
	billablePrompt := request.PeerReportedPromptTokens
	billableCompletion := request.PeerReportedCompletionTokens
	if peerTotal == 0 || request.PeerReportedPromptTokens < 0 || request.PeerReportedCompletionTokens < 0 {
		billablePrompt = request.LocalEstimatedPromptTokens
		billableCompletion = request.LocalEstimatedCompletionTokens
	}
	ratio := 0.0
	if localTotal > 0 && peerTotal > 0 {
		ratio = float64(peerTotal) / float64(localTotal)
		if ratio > sub2APIPeerUsageAllowedDiscrepancyMultiplier {
			status = Sub2APIPeerUsageStatusReviewRequired
			billablePrompt = request.LocalEstimatedPromptTokens
			billableCompletion = request.LocalEstimatedCompletionTokens
		}
	}
	now := common.GetTimestamp()
	usage := Sub2APIPeerUsage{
		RequestID:                      request.RequestID,
		ConsumerUserID:                 request.ConsumerUserID,
		ConsumerTokenID:                request.ConsumerTokenID,
		PeerChannelID:                  request.PeerChannelID,
		ProviderUserID:                 request.ProviderUserID,
		Model:                          request.Model,
		MappedUpstreamModel:            request.MappedUpstreamModel,
		LocalEstimatedPromptTokens:     request.LocalEstimatedPromptTokens,
		LocalEstimatedCompletionTokens: request.LocalEstimatedCompletionTokens,
		PeerReportedPromptTokens:       request.PeerReportedPromptTokens,
		PeerReportedCompletionTokens:   request.PeerReportedCompletionTokens,
		BillablePromptTokens:           billablePrompt,
		BillableCompletionTokens:       billableCompletion,
		UsageDiscrepancyRatio:          ratio,
		Status:                         status,
		CreatedTime:                    now,
		UpdatedTime:                    now,
	}
	if err := DB.Create(&usage).Error; err != nil {
		return nil, err
	}
	return &usage, nil
}
