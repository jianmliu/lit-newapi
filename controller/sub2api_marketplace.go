package controller

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const sub2APISystemCommissionRate = 0.05

type sub2APIMarketplaceSourceQuote struct {
	Id                  int     `json:"id"`
	Name                string  `json:"name"`
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
	BaseURL             string  `json:"base_url"`
	OwnerUserID         int     `json:"owner_user_id"`
	OwnedByCaller       bool    `json:"owned_by_caller"`
	PriceMultiplier     float64 `json:"price_multiplier"`
	EstimatedQuota      int64   `json:"estimated_quota,omitempty"`
	EstimatedUSDC       float64 `json:"estimated_usdc,omitempty"`
	SelectionPreference int     `json:"selection_preference"`
}

func QuoteSub2APIMarketplace(c *gin.Context) {
	userID := c.GetInt("id")
	requestedModel := strings.TrimSpace(c.Query("model"))
	estimatedBaseQuota := parseSub2APIEstimatedQuota(c.Query("estimated_quota"))
	sources, err := model.GetAvailableSub2APISources(userID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	quotes := make([]sub2APIMarketplaceSourceQuote, 0, len(sources))
	for _, source := range sources {
		if requestedModel != "" && source.Model != requestedModel {
			continue
		}
		quotes = append(quotes, buildSub2APIMarketplaceSourceQuote(source, userID, estimatedBaseQuota, len(quotes)+1))
	}
	var selected any
	if len(quotes) > 0 {
		selected = quotes[0]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"currency":               "USDC",
		"network":                "base",
		"quota_per_usdc":         common.QuotaPerUnit,
		"system_commission_rate": sub2APISystemCommissionRate,
		"requested_model":        requestedModel,
		"estimated_base_quota":   estimatedBaseQuota,
		"selection_policy":       "Explicit token sub2_api_source_id binding wins; otherwise One API selects the lowest price_multiplier active source for the requested model.",
		"selected_source":        selected,
		"sources":                quotes,
	}})
}

func parseSub2APIEstimatedQuota(raw string) int64 {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func buildSub2APIMarketplaceSourceQuote(source *model.Sub2APISource, userID int, estimatedBaseQuota int64, preference int) sub2APIMarketplaceSourceQuote {
	multiplier := source.EffectivePriceMultiplier()
	quote := sub2APIMarketplaceSourceQuote{
		Id:                  source.Id,
		Name:                source.Name,
		Provider:            source.Provider,
		Model:               source.Model,
		BaseURL:             source.BaseURL,
		OwnerUserID:         source.UserId,
		OwnedByCaller:       source.UserId == userID,
		PriceMultiplier:     multiplier,
		SelectionPreference: preference,
	}
	if estimatedBaseQuota > 0 {
		estimatedQuota := int64(math.Round(float64(estimatedBaseQuota) * multiplier))
		if estimatedQuota <= 0 {
			estimatedQuota = 1
		}
		quote.EstimatedQuota = estimatedQuota
		if common.QuotaPerUnit > 0 {
			quote.EstimatedUSDC = float64(estimatedQuota) / common.QuotaPerUnit
		}
	}
	return quote
}
