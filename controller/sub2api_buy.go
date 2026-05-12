package controller

import (
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const usdcAtomsPerDollar = int64(1_000_000)

type sub2APIBuyRequest struct {
	USDPaid float64 `json:"usdPaid"`
}

// BuySub2APIQuota top-ups the caller's One API quota in exchange for an
// x402-protected USDC payment. The flow has two phases:
//   - When the caller has not presented an X-PAYMENT header, respond
//     HTTP 402 with a JSON challenge so the caller can mint the EIP-3009
//     transfer authorization and retry.
//   - When X-PAYMENT is present, hand it to the configured Sub2APIPaymentProvider
//     for capture. On a successful settlement increment the caller's quota
//     by usdPaid * common.QuotaPerUnit and record a topup-log entry that
//     references the on-chain settlement tx hash so post-hoc reconciliation
//     against the hot wallet is possible.
//
// All sensitive crypto (EIP-3009 signature recovery, nonce replay
// protection, on-chain submit) lives in the provider implementation
// reached through sub2APINewPaymentProvider; this controller only enforces
// the request shape and the response envelope contract documented in the
// sub2api-marketplace agent skill.
func BuySub2APIQuota(c *gin.Context) {
	var req sub2APIBuyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.USDPaid <= 0 || math.IsNaN(req.USDPaid) || math.IsInf(req.USDPaid, 0) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "usdPaid must be positive"})
		return
	}
	expectedAtoms := int64(math.Round(req.USDPaid * float64(usdcAtomsPerDollar)))
	if expectedAtoms <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "usdPaid is too small"})
		return
	}
	paymentHeader := c.GetHeader("X-PAYMENT")
	if paymentHeader == "" {
		writeSub2APIX402Required(c, expectedAtoms, req.USDPaid)
		return
	}
	provider, err := sub2APINewPaymentProvider(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": err.Error()})
		return
	}
	defer provider.Close()
	txHash, err := provider.CaptureX402(c.Request.Context(), paymentHeader, big.NewInt(expectedAtoms))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	quotaGranted := int64(math.Round(req.USDPaid * common.QuotaPerUnit))
	if quotaGranted <= 0 {
		quotaGranted = 1
	}
	userID := c.GetInt("id")
	if err := model.IncreaseUserQuota(userID, int(quotaGranted), true); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	model.RecordTopupLog(
		userID,
		fmt.Sprintf("x402 Base USDC purchase %.6f USDC, settlement tx %s", req.USDPaid, txHash),
		c.ClientIP(),
		"sub2api_x402",
		"",
	)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"quota_granted":      quotaGranted,
		"amount_usdc":        req.USDPaid,
		"amount_usdc_atoms":  expectedAtoms,
		"currency":           "USDC",
		"network":            "base",
		"settlement_tx_hash": txHash,
	}})
}

func writeSub2APIX402Required(c *gin.Context, expectedAtoms int64, expectedUSD float64) {
	payload := gin.H{
		"version":     "1",
		"scheme":      "eip-3009",
		"asset":       "USDC",
		"network":     "base",
		"amount":      strconv.FormatInt(expectedAtoms, 10),
		"amount_usdc": expectedUSD,
		"message":     "x402 payment required",
	}
	if encoded, err := common.Marshal(payload); err == nil {
		c.Header("X-PAYMENT-REQUIRED", string(encoded))
	}
	c.JSON(http.StatusPaymentRequired, gin.H{
		"success": false,
		"message": "x402 payment required",
		"payment": payload,
	})
}
