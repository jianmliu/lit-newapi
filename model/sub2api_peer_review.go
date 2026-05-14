package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const Sub2APIPeerReviewStatusOpen = "open"

type Sub2APIPeerReview struct {
	Id                 int    `json:"id"`
	PeerChannelID      int    `json:"peer_channel_id" gorm:"index"`
	ProviderUserID     int    `json:"provider_user_id" gorm:"index"`
	DepositLockID      int    `json:"deposit_lock_id" gorm:"index"`
	PendingSlashAmount int    `json:"pending_slash_amount" gorm:"type:int;default:0"`
	Reason             string `json:"reason" gorm:"type:varchar(512)"`
	Status             string `json:"status" gorm:"type:varchar(32);index"`
	CreatedTime        int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64  `json:"updated_time" gorm:"bigint"`
}

func OpenSub2APIPeerChannelRiskReview(peerChannelID int, providerUserID int, pendingSlashAmount int, reason string) (*Sub2APIPeerReview, error) {
	if pendingSlashAmount < 0 {
		return nil, errors.New("pending slash amount must be non-negative")
	}
	var review Sub2APIPeerReview
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Sub2APIPeerChannel
		if err := tx.First(&channel, "id = ? AND provider_user_id = ?", peerChannelID, providerUserID).Error; err != nil {
			return err
		}
		if channel.DepositLockId <= 0 {
			return errors.New("peer channel has no deposit lock")
		}
		now := common.GetTimestamp()
		if err := tx.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ?", channel.DepositLockId, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
			"pending_slash_amount": pendingSlashAmount,
			"updated_time":         now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Sub2APIPeerChannel{}).Where("id = ?", peerChannelID).Updates(map[string]interface{}{
			"routing_status": Sub2APIPeerChannelRoutingPaused,
			"updated_time":   now,
		}).Error; err != nil {
			return err
		}
		review = Sub2APIPeerReview{
			PeerChannelID:      peerChannelID,
			ProviderUserID:     providerUserID,
			DepositLockID:      channel.DepositLockId,
			PendingSlashAmount: pendingSlashAmount,
			Reason:             reason,
			Status:             Sub2APIPeerReviewStatusOpen,
			CreatedTime:        now,
			UpdatedTime:        now,
		}
		return tx.Create(&review).Error
	})
	if err != nil {
		return nil, err
	}
	return &review, nil
}
