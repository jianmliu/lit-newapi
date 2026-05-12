package model

import (
	"github.com/QuantumNous/new-api/common"
)

const (
	Sub2APISourceStatusActive   = "active"
	Sub2APISourceStatusDisabled = "disabled"
	Sub2APISourceStatusError    = "error"
)

type Sub2APISource struct {
	Id              int     `json:"id"`
	UserId          int     `json:"user_id" gorm:"index"`
	Name            string  `json:"name" gorm:"index"`
	TenantID        string  `json:"tenant_id" gorm:"index"`
	CredentialID    string  `json:"credential_id" gorm:"index"`
	EndpointID      string  `json:"endpoint_id" gorm:"index"`
	KeyID           string  `json:"key_id" gorm:"index"`
	Provider        string  `json:"provider"`
	Model           string  `json:"model" gorm:"index"`
	BaseURL         string  `json:"base_url"`
	PriceMultiplier float64 `json:"price_multiplier" gorm:"default:1"`
	Status          string  `json:"status" gorm:"default:'active'"`
	ErrorMessage    string  `json:"error_message,omitempty"`
	CreatedTime     int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime     int64   `json:"updated_time" gorm:"bigint"`
}

type Sub2APISourceGrant struct {
	Id            int   `json:"id"`
	SourceID      int   `json:"source_id" gorm:"uniqueIndex:idx_sub2api_source_grant"`
	OwnerUserID   int   `json:"owner_user_id" gorm:"index"`
	GranteeUserID int   `json:"grantee_user_id" gorm:"uniqueIndex:idx_sub2api_source_grant;index"`
	CreatedTime   int64 `json:"created_time" gorm:"bigint"`
}

func GetUserSub2APISources(userID int) ([]*Sub2APISource, error) {
	var sources []*Sub2APISource
	err := DB.Order("id desc").Where("user_id = ?", userID).Find(&sources).Error
	return sources, err
}

func GetAvailableSub2APISources(userID int) ([]*Sub2APISource, error) {
	var sources []*Sub2APISource
	err := DB.Order("price_multiplier asc, id desc").Where(
		"status = ? and (user_id = ? or id in (?))",
		Sub2APISourceStatusActive,
		userID,
		DB.Model(&Sub2APISourceGrant{}).Select("source_id").Where("grantee_user_id = ?", userID),
	).Find(&sources).Error
	return sources, err
}

func GetBestAvailableSub2APISourceForUser(userID int, model string) (*Sub2APISource, error) {
	source := Sub2APISource{}
	err := DB.Where(
		"status = ? and model = ? and (user_id = ? or id in (?))",
		Sub2APISourceStatusActive,
		model,
		userID,
		DB.Model(&Sub2APISourceGrant{}).Select("source_id").Where("grantee_user_id = ?", userID),
	).Order("price_multiplier asc, id desc").First(&source).Error
	return &source, err
}

func GetSub2APISourceByIds(id int, userID int) (*Sub2APISource, error) {
	source := Sub2APISource{Id: id, UserId: userID}
	err := DB.First(&source, "id = ? and user_id = ?", id, userID).Error
	return &source, err
}

func GetSub2APISourceByEndpointID(endpointID string) (*Sub2APISource, error) {
	source := Sub2APISource{}
	err := DB.Where("endpoint_id = ?", endpointID).First(&source).Error
	return &source, err
}

func GetUsableSub2APISourceForUser(id int, userID int) (*Sub2APISource, error) {
	source := Sub2APISource{Id: id}
	err := DB.Where(
		"id = ? and status = ? and (user_id = ? or id in (?))",
		id,
		Sub2APISourceStatusActive,
		userID,
		DB.Model(&Sub2APISourceGrant{}).Select("source_id").Where("grantee_user_id = ?", userID),
	).First(&source).Error
	return &source, err
}

func GetSub2APISourceGrants(sourceID int, ownerUserID int) ([]*Sub2APISourceGrant, error) {
	var grants []*Sub2APISourceGrant
	err := DB.Order("id desc").Where("source_id = ? and owner_user_id = ?", sourceID, ownerUserID).Find(&grants).Error
	return grants, err
}

func GrantSub2APISource(sourceID int, ownerUserID int, granteeUserID int) error {
	grant := Sub2APISourceGrant{
		SourceID:      sourceID,
		OwnerUserID:   ownerUserID,
		GranteeUserID: granteeUserID,
		CreatedTime:   common.GetTimestamp(),
	}
	return DB.Where("source_id = ? and grantee_user_id = ?", sourceID, granteeUserID).FirstOrCreate(&grant).Error
}

func RevokeSub2APISourceGrant(sourceID int, ownerUserID int, granteeUserID int) error {
	return DB.Where(
		"source_id = ? and owner_user_id = ? and grantee_user_id = ?",
		sourceID, ownerUserID, granteeUserID,
	).Delete(&Sub2APISourceGrant{}).Error
}

func (source *Sub2APISource) Insert() error {
	now := common.GetTimestamp()
	if source.PriceMultiplier <= 0 {
		source.PriceMultiplier = 1
	}
	source.CreatedTime = now
	source.UpdatedTime = now
	return DB.Create(source).Error
}

func (source *Sub2APISource) EffectivePriceMultiplier() float64 {
	if source == nil || source.PriceMultiplier <= 0 {
		return 1
	}
	return source.PriceMultiplier
}

func (source *Sub2APISource) UpdateStatus(status string, message string) error {
	source.Status = status
	source.ErrorMessage = message
	source.UpdatedTime = common.GetTimestamp()
	return DB.Model(source).Select("status", "error_message", "updated_time").Updates(source).Error
}

func DeleteSub2APISourceById(id int, userID int) error {
	return DB.Where("id = ? and user_id = ?", id, userID).Delete(&Sub2APISource{}).Error
}
