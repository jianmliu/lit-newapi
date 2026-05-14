package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	Sub2APIPeerChannelHealthUnknown = "unknown"

	Sub2APIPeerChannelHealthOnline = "online"

	Sub2APIPeerChannelHealthOffline = "offline"

	Sub2APIPeerChannelRoutingDisabled = "disabled"

	Sub2APIPeerChannelRoutingPaused = "paused"

	Sub2APIPeerChannelRoutingActive = "active"

	Sub2APIPeerChannelVerificationPending = "pending_verification"

	Sub2APIPeerChannelVerificationOwnershipVerified = "ownership_verified"
)

type Sub2APIPeerChannel struct {
	Id                  int    `json:"id"`
	ProviderUserId      int    `json:"provider_user_id" gorm:"index"`
	ProviderAccountId   int    `json:"provider_account_id" gorm:"index"`
	DisplayName         string `json:"display_name" gorm:"type:varchar(191)"`
	PeerEndpointURL     string `json:"peer_endpoint_url" gorm:"type:varchar(512);index"`
	BackendID           string `json:"backend_id" gorm:"type:varchar(191);index"`
	PeerPublicKey       string `json:"peer_public_key" gorm:"type:text"`
	SignatureScheme     string `json:"signature_scheme" gorm:"type:varchar(64)"`
	NonceWindowSeconds  int    `json:"nonce_window_seconds" gorm:"type:int;default:60"`
	SupportedModels     string `json:"supported_models" gorm:"type:text"`
	ModelMapping        string `json:"model_mapping" gorm:"type:text"`
	CapacityConfig      string `json:"capacity_config" gorm:"type:text"`
	PricingTierID       string `json:"pricing_tier_id" gorm:"type:varchar(191);index"`
	RequiredDeposit     int    `json:"required_deposit" gorm:"type:int;default:0"`
	DepositLockId       int    `json:"deposit_lock_id" gorm:"index"`
	HealthStatus        string `json:"health_status" gorm:"type:varchar(32);index"`
	RoutingStatus       string `json:"routing_status" gorm:"type:varchar(32);index"`
	VerificationStatus  string `json:"verification_status" gorm:"type:varchar(32);index"`
	OwnershipVerifiedAt int64  `json:"ownership_verified_at" gorm:"bigint;default:0"`
	LastHealthCheckAt   int64  `json:"last_health_check_at" gorm:"bigint;default:0"`
	LastSuccessAt       int64  `json:"last_success_at" gorm:"bigint;default:0"`
	LastFailureAt       int64  `json:"last_failure_at" gorm:"bigint;default:0"`
	CreatedTime         int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime         int64  `json:"updated_time" gorm:"bigint"`
}

type Sub2APIPeerChannelCreateRequest struct {
	ProviderUserId     int
	ProviderAccountId  int
	DisplayName        string
	PeerEndpointURL    string
	BackendID          string
	PeerPublicKey      string
	SignatureScheme    string
	NonceWindowSeconds int
	SupportedModels    string
	ModelMapping       string
	CapacityConfig     string
	PricingTierID      string
	RequiredDeposit    int
	IdempotencyKey     string
}

func CreateSub2APIPeerChannelWithDeposit(request Sub2APIPeerChannelCreateRequest) (*Sub2APIPeerChannel, *ProviderEndpointDepositLock, error) {
	if request.ProviderUserId <= 0 {
		return nil, nil, errors.New("invalid provider user id")
	}
	if request.ProviderAccountId <= 0 {
		request.ProviderAccountId = request.ProviderUserId
	}
	if request.RequiredDeposit <= 0 {
		return nil, nil, errors.New("required deposit must be positive")
	}
	if request.IdempotencyKey == "" {
		return nil, nil, errors.New("idempotency key is required")
	}

	var channel Sub2APIPeerChannel
	var lock ProviderEndpointDepositLock
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("user_id = ? AND idempotency_key = ?", request.ProviderUserId, request.IdempotencyKey).Limit(1).Find(&lock)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			if lock.Amount != request.RequiredDeposit {
				return ErrProviderEndpointDepositIdempotencyConflict
			}
			if lock.PeerChannelId == 0 {
				return ErrProviderEndpointDepositProvisionInProgress
			}
			return tx.First(&channel, "id = ? AND provider_user_id = ?", lock.PeerChannelId, request.ProviderUserId).Error
		}

		update := tx.Model(&User{}).
			Where("id = ? AND quota - provider_locked_quota >= ?", request.ProviderUserId, request.RequiredDeposit).
			Update("provider_locked_quota", gorm.Expr("provider_locked_quota + ?", request.RequiredDeposit))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrInsufficientProviderEndpointDepositQuota
		}

		now := common.GetTimestamp()
		channel = Sub2APIPeerChannel{
			ProviderUserId:     request.ProviderUserId,
			ProviderAccountId:  request.ProviderAccountId,
			DisplayName:        request.DisplayName,
			PeerEndpointURL:    request.PeerEndpointURL,
			BackendID:          request.BackendID,
			PeerPublicKey:      request.PeerPublicKey,
			SignatureScheme:    request.SignatureScheme,
			NonceWindowSeconds: request.NonceWindowSeconds,
			SupportedModels:    request.SupportedModels,
			ModelMapping:       request.ModelMapping,
			CapacityConfig:     request.CapacityConfig,
			PricingTierID:      request.PricingTierID,
			RequiredDeposit:    request.RequiredDeposit,
			HealthStatus:       Sub2APIPeerChannelHealthUnknown,
			RoutingStatus:      Sub2APIPeerChannelRoutingDisabled,
			VerificationStatus: Sub2APIPeerChannelVerificationPending,
			CreatedTime:        now,
			UpdatedTime:        now,
		}
		if channel.NonceWindowSeconds <= 0 {
			channel.NonceWindowSeconds = 60
		}
		if err := tx.Create(&channel).Error; err != nil {
			return err
		}

		lock = ProviderEndpointDepositLock{
			UserId:         request.ProviderUserId,
			PeerChannelId:  channel.Id,
			EndpointID:     request.PeerEndpointURL,
			Amount:         request.RequiredDeposit,
			Status:         ProviderEndpointDepositStatusLocked,
			IdempotencyKey: request.IdempotencyKey,
			CreatedTime:    now,
			UpdatedTime:    now,
		}
		if err := tx.Create(&lock).Error; err != nil {
			return err
		}
		return tx.Model(&Sub2APIPeerChannel{}).Where("id = ?", channel.Id).Updates(map[string]interface{}{
			"deposit_lock_id": lock.Id,
			"updated_time":    now,
		}).Error
	})
	if err != nil {
		return nil, nil, err
	}
	channel.DepositLockId = lock.Id
	return &channel, &lock, nil
}

func GetUserSub2APIPeerChannels(userID int) ([]Sub2APIPeerChannel, error) {
	var channels []Sub2APIPeerChannel
	err := DB.Where("provider_user_id = ?", userID).Order("id desc").Find(&channels).Error
	return channels, err
}

func GetSub2APIPeerChannelByIds(id int, userID int) (*Sub2APIPeerChannel, error) {
	var channel Sub2APIPeerChannel
	if err := DB.First(&channel, "id = ? AND provider_user_id = ?", id, userID).Error; err != nil {
		return nil, err
	}
	return &channel, nil
}

func GetRoutableSub2APIPeerChannels() ([]Sub2APIPeerChannel, error) {
	var channels []Sub2APIPeerChannel
	err := DB.Where("routing_status = ?", Sub2APIPeerChannelRoutingActive).Find(&channels).Error
	return channels, err
}

func GetEligibleSub2APIPeerChannelsForModel(modelName string) ([]Sub2APIPeerChannel, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil, errors.New("model name is required")
	}
	if err := AutoPauseUnderCoveredSub2APIPeerChannelsForAllProviders(); err != nil {
		return nil, err
	}
	var candidates []Sub2APIPeerChannel
	err := DB.Where("routing_status = ? AND verification_status = ? AND health_status = ? AND deposit_lock_id > 0", Sub2APIPeerChannelRoutingActive, Sub2APIPeerChannelVerificationOwnershipVerified, Sub2APIPeerChannelHealthOnline).Order("id asc").Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	channels := make([]Sub2APIPeerChannel, 0, len(candidates))
	for _, channel := range candidates {
		if peerChannelSupportsModel(channel, modelName) && peerChannelHasCapacity(channel) {
			channels = append(channels, channel)
		}
	}
	return channels, nil
}

func AutoPauseUnderCoveredSub2APIPeerChannelsForAllProviders() error {
	var providerIDs []int
	if err := DB.Model(&Sub2APIPeerChannel{}).Where("routing_status = ?", Sub2APIPeerChannelRoutingActive).Distinct().Pluck("provider_user_id", &providerIDs).Error; err != nil {
		return err
	}
	for _, providerID := range providerIDs {
		if _, err := AutoPauseUnderCoveredSub2APIPeerChannels(providerID); err != nil {
			return err
		}
	}
	return nil
}

func peerChannelSupportsModel(channel Sub2APIPeerChannel, modelName string) bool {
	var supportedModels []string
	if err := common.UnmarshalJsonStr(channel.SupportedModels, &supportedModels); err != nil {
		return false
	}
	for _, supportedModel := range supportedModels {
		if strings.TrimSpace(supportedModel) == modelName {
			return true
		}
	}
	return false
}

func peerChannelHasCapacity(channel Sub2APIPeerChannel) bool {
	if strings.TrimSpace(channel.CapacityConfig) == "" || strings.TrimSpace(channel.CapacityConfig) == "{}" {
		return true
	}
	var capacity map[string]int
	if err := common.UnmarshalJsonStr(channel.CapacityConfig, &capacity); err != nil {
		return false
	}
	if rpm, ok := capacity["rpm"]; ok && rpm <= 0 {
		return false
	}
	return true
}

func MarkSub2APIPeerChannelOwnershipVerified(id int, userID int) error {
	now := common.GetTimestamp()
	result := DB.Model(&Sub2APIPeerChannel{}).Where("id = ? AND provider_user_id = ?", id, userID).Updates(map[string]interface{}{
		"verification_status":   Sub2APIPeerChannelVerificationOwnershipVerified,
		"ownership_verified_at": now,
		"health_status":         Sub2APIPeerChannelHealthOnline,
		"last_health_check_at":  now,
		"last_success_at":       now,
		"updated_time":          now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func MarkSub2APIPeerChannelHealthFailure(id int, userID int) error {
	now := common.GetTimestamp()
	result := DB.Model(&Sub2APIPeerChannel{}).Where("id = ? AND provider_user_id = ?", id, userID).Updates(map[string]interface{}{
		"health_status":        Sub2APIPeerChannelHealthOffline,
		"last_health_check_at": now,
		"last_failure_at":      now,
		"updated_time":         now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func MarkSub2APIPeerChannelHealthSuccess(id int, userID int) error {
	now := common.GetTimestamp()
	result := DB.Model(&Sub2APIPeerChannel{}).Where("id = ? AND provider_user_id = ?", id, userID).Updates(map[string]interface{}{
		"health_status":        Sub2APIPeerChannelHealthOnline,
		"last_health_check_at": now,
		"last_success_at":      now,
		"updated_time":         now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func UpdateSub2APIPeerChannelRoutingStatus(id int, userID int, routingStatus string) error {
	now := common.GetTimestamp()
	result := DB.Model(&Sub2APIPeerChannel{}).Where("id = ? AND provider_user_id = ?", id, userID).Updates(map[string]interface{}{
		"routing_status": routingStatus,
		"updated_time":   now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func DeleteSub2APIPeerChannelByIdAndUnlockDeposit(id int, userID int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Sub2APIPeerChannel
		if err := tx.First(&channel, "id = ? AND provider_user_id = ?", id, userID).Error; err != nil {
			return err
		}
		var lock ProviderEndpointDepositLock
		result := tx.Where("peer_channel_id = ? AND user_id = ? AND status = ?", id, userID, ProviderEndpointDepositStatusLocked).Limit(1).Find(&lock)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			unlockAmount := lock.Amount - lock.UnlockedAmount - lock.SlashedAmount
			if unlockAmount < 0 {
				unlockAmount = 0
			}
			now := common.GetTimestamp()
			update := tx.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ?", lock.Id, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
				"status":          ProviderEndpointDepositStatusUnlocked,
				"unlocked_amount": lock.UnlockedAmount + unlockAmount,
				"updated_time":    now,
			})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected == 1 && unlockAmount > 0 {
				if err := tx.Model(&User{}).Where("id = ?", lock.UserId).Update("provider_locked_quota", gorm.Expr("provider_locked_quota - ?", unlockAmount)).Error; err != nil {
					return err
				}
			}
		}
		result = tx.Where("id = ? AND provider_user_id = ?", id, userID).Delete(&Sub2APIPeerChannel{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func UnlockSub2APIPeerChannelDeposit(id int, userID int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Sub2APIPeerChannel
		if err := tx.First(&channel, "id = ? AND provider_user_id = ?", id, userID).Error; err != nil {
			return err
		}
		var lock ProviderEndpointDepositLock
		result := tx.Where("peer_channel_id = ? AND user_id = ? AND status = ?", id, userID, ProviderEndpointDepositStatusLocked).Limit(1).Find(&lock)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		unlockAmount := lock.Amount - lock.UnlockedAmount - lock.SlashedAmount
		if unlockAmount < 0 {
			unlockAmount = 0
		}
		now := common.GetTimestamp()
		update := tx.Model(&ProviderEndpointDepositLock{}).Where("id = ? AND status = ?", lock.Id, ProviderEndpointDepositStatusLocked).Updates(map[string]interface{}{
			"status":          ProviderEndpointDepositStatusUnlocked,
			"unlocked_amount": lock.UnlockedAmount + unlockAmount,
			"updated_time":    now,
		})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 1 && unlockAmount > 0 {
			if err := tx.Model(&User{}).Where("id = ?", lock.UserId).Update("provider_locked_quota", gorm.Expr("provider_locked_quota - ?", unlockAmount)).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Sub2APIPeerChannel{}).Where("id = ? AND provider_user_id = ?", id, userID).Updates(map[string]interface{}{
			"deposit_lock_id": 0,
			"updated_time":    now,
		}).Error
	})
}

func AutoPauseUnderCoveredSub2APIPeerChannels(userID int) (int, error) {
	var user User
	if err := DB.Select("provider_locked_quota").First(&user, "id = ?", userID).Error; err != nil {
		return 0, err
	}
	var channels []Sub2APIPeerChannel
	if err := DB.Where("provider_user_id = ? AND routing_status = ?", userID, Sub2APIPeerChannelRoutingActive).Order("id asc").Find(&channels).Error; err != nil {
		return 0, err
	}
	required := 0
	for _, channel := range channels {
		required += channel.RequiredDeposit
	}
	paused := 0
	for i := len(channels) - 1; required > user.ProviderLockedQuota && i >= 0; i-- {
		channel := channels[i]
		if err := UpdateSub2APIPeerChannelRoutingStatus(channel.Id, userID, Sub2APIPeerChannelRoutingPaused); err != nil {
			return paused, err
		}
		required -= channel.RequiredDeposit
		paused++
	}
	return paused, nil
}
