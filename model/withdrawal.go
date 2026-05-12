package model

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

const (
	WithdrawalStatusPending    = "pending"
	WithdrawalStatusProcessing = "processing"
	WithdrawalStatusApproved   = "approved"
	WithdrawalStatusRejected   = "rejected"
)

type Withdrawal struct {
	Id            int     `json:"id"`
	UserId        int     `json:"user_id" gorm:"index"`
	Quota         int64   `json:"quota" gorm:"bigint;default:0"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency" gorm:"type:varchar(16);default:'USD'"`
	PayoutMethod  string  `json:"payout_method" gorm:"type:varchar(64)"`
	PayoutAccount string  `json:"payout_account" gorm:"type:text"`
	TxHash        string  `json:"tx_hash" gorm:"type:varchar(128)"`
	Status        string  `json:"status" gorm:"type:varchar(16);default:'pending';index"`
	Remark        string  `json:"remark" gorm:"type:text"`
	CreatedTime   int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime   int64   `json:"updated_time" gorm:"bigint"`
}

func quotaToCurrencyAmount(quota int64) float64 {
	if common.QuotaPerUnit <= 0 {
		return float64(quota)
	}
	return float64(quota) / common.QuotaPerUnit
}

func CreateWithdrawal(ctx context.Context, userID int, quota int64, currency string, payoutMethod string, payoutAccount string) (*Withdrawal, error) {
	_ = ctx
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	if quota <= 0 {
		return nil, errors.New("withdrawal quota must be positive")
	}
	if currency == "" {
		currency = "USD"
	}
	if payoutMethod == "" || payoutAccount == "" {
		return nil, errors.New("payout method and account are required")
	}
	now := common.GetTimestamp()
	withdrawal := &Withdrawal{
		UserId:        userID,
		Quota:         quota,
		Amount:        quotaToCurrencyAmount(quota),
		Currency:      currency,
		PayoutMethod:  payoutMethod,
		PayoutAccount: payoutAccount,
		Status:        WithdrawalStatusPending,
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var currentQuota int64
		if err := tx.Model(&User{}).Where("id = ?", userID).Select("quota").Scan(&currentQuota).Error; err != nil {
			return err
		}
		if currentQuota < quota {
			return errors.New("insufficient quota")
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).Update("quota", gorm.Expr("quota - ?", quota)).Error; err != nil {
			return err
		}
		return tx.Create(withdrawal).Error
	})
	if err != nil {
		return nil, err
	}
	return withdrawal, nil
}

func GetUserWithdrawals(userID int) ([]*Withdrawal, error) {
	var withdrawals []*Withdrawal
	err := DB.Order("id desc").Where("user_id = ?", userID).Find(&withdrawals).Error
	return withdrawals, err
}

func GetWithdrawalByID(id int) (*Withdrawal, error) {
	var withdrawal Withdrawal
	err := DB.First(&withdrawal, "id = ?", id).Error
	return &withdrawal, err
}

func ClaimWithdrawalForPayout(id int) (*Withdrawal, error) {
	var withdrawal Withdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Withdrawal{}).Where("id = ? AND status = ?", id, WithdrawalStatusPending).Updates(map[string]any{
			"status":       WithdrawalStatusProcessing,
			"updated_time": common.GetTimestamp(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("withdrawal is not pending")
		}
		return tx.First(&withdrawal, "id = ?", id).Error
	})
	return &withdrawal, err
}

func ResetWithdrawalPayout(ctx context.Context, id int, remark string) error {
	_ = ctx
	return DB.Model(&Withdrawal{}).Where("id = ? AND status = ?", id, WithdrawalStatusProcessing).Updates(map[string]any{
		"status":       WithdrawalStatusPending,
		"remark":       remark,
		"updated_time": common.GetTimestamp(),
	}).Error
}

func GetAllWithdrawals(startIdx int, num int, status string) ([]*Withdrawal, error) {
	var withdrawals []*Withdrawal
	tx := DB.Order("id desc").Limit(num).Offset(startIdx)
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	err := tx.Find(&withdrawals).Error
	return withdrawals, err
}

func UpdateWithdrawalStatus(ctx context.Context, id int, status string, remark string, txHash string) (*Withdrawal, error) {
	_ = ctx
	if status != WithdrawalStatusApproved && status != WithdrawalStatusRejected {
		return nil, errors.New("invalid withdrawal status")
	}
	var withdrawal Withdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&withdrawal, "id = ?", id).Error; err != nil {
			return err
		}
		if withdrawal.Status != WithdrawalStatusPending && !(status == WithdrawalStatusApproved && withdrawal.Status == WithdrawalStatusProcessing) {
			return errors.New("withdrawal is not pending")
		}
		withdrawal.Status = status
		withdrawal.Remark = remark
		withdrawal.TxHash = txHash
		withdrawal.UpdatedTime = common.GetTimestamp()
		if err := tx.Model(&withdrawal).Select("status", "remark", "tx_hash", "updated_time").Updates(&withdrawal).Error; err != nil {
			return err
		}
		if status == WithdrawalStatusRejected {
			return tx.Model(&User{}).Where("id = ?", withdrawal.UserId).Update("quota", gorm.Expr("quota + ?", withdrawal.Quota)).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &withdrawal, nil
}
