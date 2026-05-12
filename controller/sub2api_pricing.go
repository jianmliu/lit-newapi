package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

func GetSub2APIPricing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"currency":         "USDC",
			"network":          "base",
			"quota_per_usdc":   common.QuotaPerUnit,
			"min_purchase_usd": 0.01,
		},
	})
}
