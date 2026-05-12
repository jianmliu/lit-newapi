package controller

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type sub2APISourceGrantRequest struct {
	UserID int `json:"user_id"`
}

type sub2APISourceCreateRequest struct {
	Name                 string         `json:"name"`
	Provider             string         `json:"provider"`
	Model                string         `json:"model"`
	BaseURL              string         `json:"base_url"`
	AccessToken          string         `json:"access_token"`
	APIKey               string         `json:"api_key"`
	CredentialType       string         `json:"credential_type"`
	QuotaSource          string         `json:"quota_source"`
	PriceMultiplier      float64        `json:"price_multiplier"`
	QuotaLimitTotalUnits uint64         `json:"quota_limit_total_units"`
	QuotaLimitDayUnits   uint64         `json:"quota_limit_day_units"`
	RateLimitPerMinute   uint64         `json:"rate_limit_per_minute"`
	Credential           map[string]any `json:"credential"`
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

func CreateSub2APISource(c *gin.Context) {
	var req sub2APISourceCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := normalizeSub2APISourceCreateRequest(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("invalid request: %s", err.Error())})
		return
	}
	client, err := newSub2APIAdminClient()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	userID := c.GetInt("id")
	suffix := common.GetUUID()
	tenantID := fmt.Sprintf("oneapi-user-%d", userID)
	credentialID := "cred-" + suffix
	endpointID := "endpoint-" + suffix
	keyID := "key-" + suffix
	tenantName := fmt.Sprintf("One API user %d", userID)
	credential := req.Credential
	if credential == nil {
		credential = map[string]any{}
	}
	if req.AccessToken != "" {
		credential["access_token"] = req.AccessToken
	}
	if req.APIKey != "" {
		credential["api_key"] = req.APIKey
	}
	if _, err = client.post(c, "/sub2api/v1/credentials/import", map[string]any{
		"tenant_id":       tenantID,
		"tenant_name":     tenantName,
		"credential_id":   credentialID,
		"provider":        req.Provider,
		"credential_type": req.CredentialType,
		"quota_source":    req.QuotaSource,
		"credential":      credential,
	}); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if _, err = client.post(c, "/sub2api/v1/endpoints", map[string]any{
		"tenant_id":     tenantID,
		"endpoint_id":   endpointID,
		"credential_id": credentialID,
		"name":          req.Name,
		"provider":      req.Provider,
		"model":         req.Model,
		"base_url":      req.BaseURL,
	}); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	keyResponse, err := client.post(c, "/sub2api/v1/endpoints/"+url.PathEscape(endpointID)+"/keys", map[string]any{
		"tenant_id":               tenantID,
		"key_id":                  keyID,
		"name":                    req.Name,
		"model_allowlist":         []string{req.Model},
		"quota_limit_total_units": req.QuotaLimitTotalUnits,
		"quota_limit_day_units":   req.QuotaLimitDayUnits,
		"rate_limit_per_minute":   req.RateLimitPerMinute,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	source := model.Sub2APISource{
		UserId:          userID,
		Name:            req.Name,
		TenantID:        tenantID,
		CredentialID:    credentialID,
		EndpointID:      endpointID,
		KeyID:           keyID,
		Provider:        req.Provider,
		Model:           req.Model,
		BaseURL:         req.BaseURL,
		PriceMultiplier: req.PriceMultiplier,
		Status:          model.Sub2APISourceStatusActive,
	}
	if err = source.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"source":        source,
		"runtime_token": keyResponse["token"],
	}})
}

func DeleteSub2APISource(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	if client, err := newSub2APIAdminClient(); err == nil {
		_, _ = client.post(c, "/sub2api/v1/keys/"+url.PathEscape(source.KeyID)+"/revoke", map[string]any{
			"tenant_id": source.TenantID,
		})
	}
	if err := model.DeleteSub2APISourceById(source.Id, c.GetInt("id")); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func GetSub2APISourceQuota(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	client, err := newSub2APIAdminClient()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	data, err := client.get(c, "/sub2api/v1/endpoints/"+url.PathEscape(source.EndpointID)+"/keys/"+url.PathEscape(source.KeyID)+"/quota", nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func GetSub2APISourceUsage(c *gin.Context) {
	source, ok := loadUserSub2APISource(c)
	if !ok {
		return
	}
	client, err := newSub2APIAdminClient()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	query := url.Values{}
	if limit := strings.TrimSpace(c.Query("limit")); limit != "" {
		query.Set("limit", limit)
	}
	data, err := client.get(c, "/sub2api/v1/endpoints/"+url.PathEscape(source.EndpointID)+"/keys/"+url.PathEscape(source.KeyID)+"/usage", query)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func normalizeSub2APISourceCreateRequest(req *sub2APISourceCreateRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.Model = strings.TrimSpace(req.Model)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.AccessToken = strings.TrimSpace(req.AccessToken)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.CredentialType = strings.ToLower(strings.TrimSpace(req.CredentialType))
	req.QuotaSource = strings.ToLower(strings.TrimSpace(req.QuotaSource))
	if req.Name == "" || len(req.Name) > 64 {
		return fmt.Errorf("source name is required and must be at most 64 characters")
	}
	if req.Provider == "" {
		req.Provider = "openai"
	}
	if req.Provider != "openai" && req.Provider != "gemini" {
		return fmt.Errorf("provider must be openai or gemini")
	}
	if req.Model == "" || len(req.Model) > 128 {
		return fmt.Errorf("model is required and must be at most 128 characters")
	}
	if req.BaseURL == "" {
		req.BaseURL = defaultSub2APIProviderBaseURL(req.Provider)
	}
	parsed, err := url.Parse(req.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("base_url must be an https origin/path without query or fragment")
	}
	if req.CredentialType == "" {
		req.CredentialType = "oauth"
	}
	if req.CredentialType != "oauth" && req.CredentialType != "api_key" {
		return fmt.Errorf("credential_type must be oauth or api_key")
	}
	if req.QuotaSource == "" {
		req.QuotaSource = "subscription"
	}
	if req.QuotaSource != "subscription" && req.QuotaSource != "provider_api_quota" && req.QuotaSource != "unknown" {
		return fmt.Errorf("quota_source is invalid")
	}
	if req.PriceMultiplier == 0 {
		req.PriceMultiplier = 1
	}
	if req.PriceMultiplier < 0.01 || req.PriceMultiplier > 10 {
		return fmt.Errorf("price_multiplier must be between 0.01 and 10")
	}
	if req.AccessToken == "" && req.APIKey == "" && credentialSecret(req.Credential) == "" {
		return fmt.Errorf("access_token or api_key is required")
	}
	return nil
}

func defaultSub2APIProviderBaseURL(provider string) string {
	if provider == "gemini" {
		return "https://generativelanguage.googleapis.com"
	}
	return "https://api.openai.com/v1"
}

func credentialSecret(credential map[string]any) string {
	if credential == nil {
		return ""
	}
	for _, key := range []string{"access_token", "api_key"} {
		if value, ok := credential[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
