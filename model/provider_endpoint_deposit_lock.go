package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	ProviderEndpointDepositStatusLocked   = "locked"
	ProviderEndpointDepositStatusUnlocked = "unlocked"
)

var (
	ErrInsufficientProviderEndpointDepositQuota   = errors.New("insufficient available quota for provider endpoint deposit")
	ErrProviderEndpointDepositIdempotencyConflict = errors.New("provider endpoint deposit idempotency conflict")
	ErrProviderEndpointDepositProvisionInProgress = errors.New("provider endpoint deposit provisioning is already in progress")
	ErrProviderEndpointDepositAlreadyAttached     = errors.New("provider endpoint deposit lock is already attached")
)

type ProviderEndpointDepositLock struct {
	Id                 int    `json:"id"`
	UserId             int    `json:"user_id" gorm:"index;uniqueIndex:idx_provider_endpoint_deposit_idempotency"`
	SourceId           int    `json:"source_id" gorm:"index"`
	PeerChannelId      int    `json:"peer_channel_id" gorm:"index"`
	EndpointID         string `json:"endpoint_id" gorm:"type:varchar(191);index"`
	RecordID           string `json:"record_id" gorm:"type:varchar(191);index"`
	Amount             int    `json:"amount" gorm:"type:int;default:0"`
	UnlockedAmount     int    `json:"unlocked_amount" gorm:"type:int;default:0"`
	SlashedAmount      int    `json:"slashed_amount" gorm:"type:int;default:0"`
	PendingSlashAmount int    `json:"pending_slash_amount" gorm:"type:int;default:0"`
	Status             string `json:"status" gorm:"type:varchar(32);index"`
	IdempotencyKey     string `json:"idempotency_key" gorm:"type:varchar(191);uniqueIndex:idx_provider_endpoint_deposit_idempotency"`
	CreatedTime        int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime        int64  `json:"updated_time" gorm:"bigint"`
}

func GetUserAvailableQuota(userID int) (int, error) {
	var user User
	if err := DB.Select("quota", "provider_locked_quota").First(&user, "id = ?", userID).Error; err != nil {
		return 0, err
	}
	return user.Quota - user.ProviderLockedQuota, nil
}

func GetUserProviderLockedQuota(userID int) (int, error) {
	var locked int
	err := DB.Model(&User{}).Where("id = ?", userID).Select("provider_locked_quota").Find(&locked).Error
	return locked, err
}

func LockProviderEndpointDeposit(userID int, amount int, idempotencyKey string) (*ProviderEndpointDepositLock, error) {
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	if amount <= 0 {
		return nil, errors.New("provider endpoint deposit amount must be positive")
	}
	if idempotencyKey == "" {
		return nil, errors.New("idempotency key is required")
	}
	var lock ProviderEndpointDepositLock
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).Limit(1).Find(&lock)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if lock.Amount != amount {
				return ErrProviderEndpointDepositIdempotencyConflict
			}
			if lock.Status == ProviderEndpointDepositStatusLocked && lock.SourceId == 0 && lock.PeerChannelId == 0 {
				return ErrProviderEndpointDepositProvisionInProgress
			}
			return nil
		}

		result = tx.Model(&User{}).
			Where("id = ? AND quota - provider_locked_quota >= ?", userID, amount).
			Update("provider_locked_quota", gorm.Expr("provider_locked_quota + ?", amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrInsufficientProviderEndpointDepositQuota
		}

		now := common.GetTimestamp()
		lock = ProviderEndpointDepositLock{
			UserId:         userID,
			Amount:         amount,
			Status:         ProviderEndpointDepositStatusLocked,
			IdempotencyKey: idempotencyKey,
			CreatedTime:    now,
			UpdatedTime:    now,
		}
		return tx.Create(&lock).Error
	})
	if err != nil {
		return nil, err
	}
	return &lock, nil
}

func UnlockProviderEndpointDeposit(lockID int) error {
	if lockID <= 0 {
		return errors.New("invalid provider endpoint deposit lock id")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var lock ProviderEndpointDepositLock
		if err := tx.First(&lock, "id = ?", lockID).Error; err != nil {
			return err
		}
		if lock.Status == ProviderEndpointDepositStatusUnlocked {
			return nil
		}
		unlockAmount := lock.Amount - lock.UnlockedAmount - lock.SlashedAmount
		if unlockAmount < 0 {
			unlockAmount = 0
		}
		now := common.GetTimestamp()
		result := tx.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ?", lockID, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
			"status":          ProviderEndpointDepositStatusUnlocked,
			"unlocked_amount": lock.UnlockedAmount + unlockAmount,
			"updated_time":    now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if unlockAmount > 0 {
			if err := tx.Model(&User{}).Where("id = ?", lock.UserId).Update("provider_locked_quota", gorm.Expr("provider_locked_quota - ?", unlockAmount)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func AttachProviderEndpointDepositLock(lockID int, sourceID int, endpointID string, recordID string) error {
	if lockID <= 0 {
		return errors.New("invalid provider endpoint deposit lock id")
	}
	if sourceID <= 0 {
		return errors.New("invalid source id")
	}
	result := DB.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ? AND source_id = 0 AND peer_channel_id = 0", lockID, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
		"source_id":    sourceID,
		"endpoint_id":  endpointID,
		"record_id":    recordID,
		"updated_time": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrProviderEndpointDepositAlreadyAttached
	}
	return nil
}

func AttachProviderEndpointDepositLockToPeerChannel(lockID int, peerChannelID int, endpointID string, recordID string) error {
	if lockID <= 0 {
		return errors.New("invalid provider endpoint deposit lock id")
	}
	if peerChannelID <= 0 {
		return errors.New("invalid peer channel id")
	}
	result := DB.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ? AND source_id = 0 AND peer_channel_id = 0", lockID, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
		"peer_channel_id": peerChannelID,
		"endpoint_id":     endpointID,
		"record_id":       recordID,
		"updated_time":    common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrProviderEndpointDepositAlreadyAttached
	}
	return nil
}

func UnlockProviderEndpointDepositForSource(sourceID int) error {
	if sourceID <= 0 {
		return nil
	}
	var lock ProviderEndpointDepositLock
	result := DB.Where("source_id = ? AND status = ?", sourceID, ProviderEndpointDepositStatusLocked).Limit(1).Find(&lock)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return UnlockProviderEndpointDeposit(lock.Id)
}

func UnlockProviderEndpointDepositForPeerChannel(peerChannelID int) error {
	if peerChannelID <= 0 {
		return nil
	}
	var lock ProviderEndpointDepositLock
	result := DB.Where("peer_channel_id = ? AND status = ?", peerChannelID, ProviderEndpointDepositStatusLocked).Limit(1).Find(&lock)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return UnlockProviderEndpointDeposit(lock.Id)
}
