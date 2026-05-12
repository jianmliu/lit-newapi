package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type sub2APISourceGrantRequest struct {
	UserID int `json:"user_id"`
}

func ListSub2APISources(c *gin.Context) {
	sources, err := model.GetUserSub2APISources(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": sources})
}

func ListAvailableSub2APISources(c *gin.Context) {
	sources, err := model.GetAvailableSub2APISources(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": sources})
}

func loadUserSub2APISource(c *gin.Context) (*model.Sub2APISource, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid source id"})
		return nil, false
	}
	source, err := model.GetSub2APISourceByIds(id, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	return source, true
}

func ListSub2APISourceGrants(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	grants, err := model.GetSub2APISourceGrants(source.Id, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": grants})
}

func GrantSub2APISource(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	var req sub2APISourceGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.UserID <= 0 || req.UserID == c.GetInt("id") {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid grantee user_id"})
		return
	}
	if err := model.GrantSub2APISource(source.Id, c.GetInt("id"), req.UserID); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func RevokeSub2APISourceGrant(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	granteeUserID, err := strconv.Atoi(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.RevokeSub2APISourceGrant(source.Id, c.GetInt("id"), granteeUserID); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
