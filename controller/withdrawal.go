package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type createWithdrawalRequest struct {
	Quota         int64  `json:"quota"`
	Currency      string `json:"currency"`
	PayoutMethod  string `json:"payout_method"`
	PayoutAccount string `json:"payout_account"`
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
