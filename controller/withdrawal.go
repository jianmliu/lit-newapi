package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type createWithdrawalRequest struct {
	Quota         int64  `json:"quota"`
	Currency      string `json:"currency"`
	PayoutMethod  string `json:"payout_method"`
	PayoutAccount string `json:"payout_account"`
}

type updateWithdrawalRequest struct {
	Status string `json:"status"`
	Remark string `json:"remark"`
}

func CreateWithdrawal(c *gin.Context) {
	var req createWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	withdrawal, err := model.CreateWithdrawal(
		c.Request.Context(),
		c.GetInt("id"),
		req.Quota,
		strings.ToUpper(strings.TrimSpace(req.Currency)),
		strings.TrimSpace(req.PayoutMethod),
		strings.TrimSpace(req.PayoutAccount),
	)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": withdrawal})
}

func GetSelfWithdrawals(c *gin.Context) {
	withdrawals, err := model.GetUserWithdrawals(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": withdrawals})
}

func GetAllWithdrawals(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("p"))
	if page < 0 {
		page = 0
	}
	itemsPerPage := common.ItemsPerPage
	withdrawals, err := model.GetAllWithdrawals(page*itemsPerPage, itemsPerPage, strings.TrimSpace(c.Query("status")))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": withdrawals})
}

func UpdateWithdrawal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	var req updateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != model.WithdrawalStatusApproved && status != model.WithdrawalStatusRejected {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "status must be 'approved' or 'rejected'"})
		return
	}
	withdrawal, err := model.GetWithdrawalByID(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	txHash := ""
	if status == model.WithdrawalStatusApproved {
		hash, err := dispatchWithdrawalPayout(c, withdrawal)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		txHash = hash
	}
	updated, err := model.UpdateWithdrawalStatus(c.Request.Context(), id, status, strings.TrimSpace(req.Remark), txHash)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": updated})
}

// dispatchWithdrawalPayout is a placeholder for the future payment-provider
// router (Stripe / Creem / Base USDC). For now it only succeeds when the
// caller explicitly opts into a manual payout by setting payout_method to
// 'manual'; any other payout_method returns 'not configured' so admins
// cannot accidentally approve a withdrawal that has no settlement path.
// Base USDC + EIP-3009 capture lands together with sub2api_buy.go.
func dispatchWithdrawalPayout(c *gin.Context, withdrawal *model.Withdrawal) (string, error) {
	method := strings.ToLower(strings.TrimSpace(withdrawal.PayoutMethod))
	if method == "manual" {
		return "", nil
	}
	return "", errors.New("payout method '" + withdrawal.PayoutMethod + "' is not configured")
}
