package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

type sub2APIPeerChannelDepositEstimateRequest struct {
	CommittedTokens int    `json:"committed_tokens"`
	DepositPolicyID string `json:"deposit_policy_id"`
}

type sub2APIPeerChannelCreateRequest struct {
	DisplayName        string         `json:"display_name"`
	PeerEndpointURL    string         `json:"peer_endpoint_url"`
	BackendID          string         `json:"backend_id"`
	PeerPublicKey      string         `json:"peer_public_key"`
	SignatureScheme    string         `json:"signature_scheme"`
	NonceWindowSeconds int            `json:"nonce_window_seconds"`
	SupportedModels    []string       `json:"supported_models"`
	ModelMapping       map[string]any `json:"model_mapping"`
	CapacityConfig     map[string]any `json:"capacity_config"`
	PricingTierID      string         `json:"pricing_tier_id"`
	DepositQuota       int            `json:"deposit_quota"`
	IdempotencyKey     string         `json:"idempotency_key"`
}

type sub2APIPeerChallengeRequest struct {
	ChallengeID    string `json:"challenge_id"`
	ChallengeNonce string `json:"challenge_nonce"`
	BackendID      string `json:"backend_id"`
	ChannelID      string `json:"channel_id"`
}

type sub2APIPeerChallengeResponse struct {
	ChallengeID     string `json:"challenge_id"`
	SignatureScheme string `json:"signature_scheme"`
	Signature       string `json:"signature"`
}

type sub2APIPeerHealthResponse struct {
	Status string `json:"status"`
}

type sub2APIPeerModelsResponse struct {
	Models []string `json:"models"`
}

type sub2APIPeerCapacityResponse struct {
	Capacity map[string]any `json:"capacity"`
}

func ListSub2APIPeerChannels(c *gin.Context) {
	channels, err := model.GetUserSub2APIPeerChannels(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": channels})
}

func loadUserSub2APIPeerChannel(c *gin.Context) (*model.Sub2APIPeerChannel, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid peer channel id"})
		return nil, false
	}
	channel, err := model.GetSub2APIPeerChannelByIds(id, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return nil, false
	}
	return channel, true
}

func GetSub2APIPeerChannel(c *gin.Context) {
	channel, ok := loadUserSub2APIPeerChannel(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": channel})
}

func VerifySub2APIPeerChannel(c *gin.Context) {
	channel, ok := loadUserSub2APIPeerChannel(c)
	if !ok {
		return
	}
	if err := verifySub2APIPeerOwnership(channel); err != nil {
		_ = model.MarkSub2APIPeerChannelHealthFailure(channel.Id, c.GetInt("id"))
		if channel.OwnershipVerifiedAt == 0 {
			if unlockErr := model.UnlockSub2APIPeerChannelDeposit(channel.Id, c.GetInt("id")); unlockErr != nil {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": unlockErr.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.MarkSub2APIPeerChannelOwnershipVerified(channel.Id, c.GetInt("id")); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	updated, err := model.GetSub2APIPeerChannelByIds(channel.Id, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": updated})
}

func PauseSub2APIPeerChannel(c *gin.Context) {
	updateSub2APIPeerChannelRoutingStatus(c, model.Sub2APIPeerChannelRoutingPaused)
}

func ResumeSub2APIPeerChannel(c *gin.Context) {
	channel, ok := loadUserSub2APIPeerChannel(c)
	if !ok {
		return
	}
	routingStatus := model.Sub2APIPeerChannelRoutingDisabled
	if channel.VerificationStatus == model.Sub2APIPeerChannelVerificationOwnershipVerified && channel.HealthStatus == model.Sub2APIPeerChannelHealthOnline && channel.DepositLockId > 0 {
		routingStatus = model.Sub2APIPeerChannelRoutingActive
	}
	updateLoadedSub2APIPeerChannelRoutingStatus(c, channel, routingStatus)
}

func updateSub2APIPeerChannelRoutingStatus(c *gin.Context, routingStatus string) {
	channel, ok := loadUserSub2APIPeerChannel(c)
	if !ok {
		return
	}
	updateLoadedSub2APIPeerChannelRoutingStatus(c, channel, routingStatus)
}

func updateLoadedSub2APIPeerChannelRoutingStatus(c *gin.Context, channel *model.Sub2APIPeerChannel, routingStatus string) {
	if err := model.UpdateSub2APIPeerChannelRoutingStatus(channel.Id, c.GetInt("id"), routingStatus); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	updated, err := model.GetSub2APIPeerChannelByIds(channel.Id, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": updated})
}

func DeleteSub2APIPeerChannel(c *gin.Context) {
	channel, ok := loadUserSub2APIPeerChannel(c)
	if !ok {
		return
	}
	if err := model.DeleteSub2APIPeerChannelByIdAndUnlockDeposit(channel.Id, c.GetInt("id")); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func EstimateSub2APIPeerChannelDeposit(c *gin.Context) {
	var req sub2APIPeerChannelDepositEstimateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if req.CommittedTokens <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "committed_tokens must be positive"})
		return
	}
	if strings.TrimSpace(req.DepositPolicyID) == "" {
		req.DepositPolicyID = "peer-default-v1"
	}
	available, err := model.GetUserAvailableQuota(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	locked, err := model.GetUserProviderLockedQuota(c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"required_deposit":  req.CommittedTokens,
		"available_balance": available,
		"locked_balance":    locked,
		"deposit_policy_id": req.DepositPolicyID,
	}})
}

func CreateSub2APIPeerChannel(c *gin.Context) {
	var req sub2APIPeerChannelCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := normalizeSub2APIPeerChannelCreateRequest(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("invalid request: %s", err.Error())})
		return
	}

	userID := c.GetInt("id")
	idempotentReplay := false
	var existingLock model.ProviderEndpointDepositLock
	result := model.DB.Where("user_id = ? AND idempotency_key = ?", userID, req.IdempotencyKey).Limit(1).Find(&existingLock)
	if result.Error != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": result.Error.Error()})
		return
	}
	if result.RowsAffected == 1 && existingLock.PeerChannelId > 0 {
		idempotentReplay = true
	}

	supportedModels, err := common.Marshal(req.SupportedModels)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	modelMapping, err := common.Marshal(req.ModelMapping)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	capacityConfig, err := common.Marshal(req.CapacityConfig)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	channel, lock, err := model.CreateSub2APIPeerChannelWithDeposit(model.Sub2APIPeerChannelCreateRequest{
		ProviderUserId:     userID,
		ProviderAccountId:  userID,
		DisplayName:        req.DisplayName,
		PeerEndpointURL:    req.PeerEndpointURL,
		BackendID:          req.BackendID,
		PeerPublicKey:      req.PeerPublicKey,
		SignatureScheme:    req.SignatureScheme,
		NonceWindowSeconds: req.NonceWindowSeconds,
		SupportedModels:    string(supportedModels),
		ModelMapping:       string(modelMapping),
		CapacityConfig:     string(capacityConfig),
		PricingTierID:      req.PricingTierID,
		RequiredDeposit:    req.DepositQuota,
		IdempotencyKey:     req.IdempotencyKey,
	})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{
		"channel":           channel,
		"deposit_lock":      lock,
		"idempotent_replay": idempotentReplay,
	}})
}

func normalizeSub2APIPeerChannelCreateRequest(req *sub2APIPeerChannelCreateRequest) error {
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.PeerEndpointURL = strings.TrimSpace(req.PeerEndpointURL)
	req.BackendID = strings.TrimSpace(req.BackendID)
	req.PeerPublicKey = strings.TrimSpace(req.PeerPublicKey)
	req.SignatureScheme = strings.ToLower(strings.TrimSpace(req.SignatureScheme))
	req.PricingTierID = strings.TrimSpace(req.PricingTierID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.DisplayName == "" || len(req.DisplayName) > 64 {
		return fmt.Errorf("display_name is required and must be at most 64 characters")
	}
	parsed, err := url.Parse(req.PeerEndpointURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("peer_endpoint_url must be an https origin/path without query or fragment")
	}
	if req.BackendID == "" || len(req.BackendID) > 191 {
		return fmt.Errorf("backend_id is required and must be at most 191 characters")
	}
	if req.PeerPublicKey == "" {
		return fmt.Errorf("peer_public_key is required")
	}
	if req.SignatureScheme == "" {
		req.SignatureScheme = "ed25519-v1"
	}
	if req.SignatureScheme != "ed25519-v1" {
		return fmt.Errorf("signature_scheme must be ed25519-v1")
	}
	if req.NonceWindowSeconds <= 0 {
		req.NonceWindowSeconds = 60
	}
	if req.NonceWindowSeconds < 30 || req.NonceWindowSeconds > 300 {
		return fmt.Errorf("nonce_window_seconds must be between 30 and 300")
	}
	if len(req.SupportedModels) == 0 {
		return fmt.Errorf("supported_models is required")
	}
	for i := range req.SupportedModels {
		req.SupportedModels[i] = strings.TrimSpace(req.SupportedModels[i])
		if req.SupportedModels[i] == "" {
			return fmt.Errorf("supported_models cannot contain empty values")
		}
	}
	if req.ModelMapping == nil {
		req.ModelMapping = map[string]any{}
	}
	if req.CapacityConfig == nil {
		req.CapacityConfig = map[string]any{}
	}
	if req.PricingTierID == "" {
		req.PricingTierID = "tier-default"
	}
	if req.DepositQuota <= 0 {
		return fmt.Errorf("deposit_quota must be positive")
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency_key is required")
	}
	return nil
}

func verifySub2APIPeerOwnership(channel *model.Sub2APIPeerChannel) error {
	if channel.SignatureScheme != "ed25519-v1" {
		return fmt.Errorf("unsupported signature_scheme %q", channel.SignatureScheme)
	}
	publicKey, err := base64.StdEncoding.DecodeString(channel.PeerPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("peer_public_key must be base64 ed25519 public key")
	}
	challenge := sub2APIPeerChallengeRequest{
		ChallengeID:    common.GetUUID(),
		ChallengeNonce: common.GetUUID(),
		BackendID:      channel.BackendID,
		ChannelID:      strconv.Itoa(channel.Id),
	}
	payload, err := common.Marshal(challenge)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(channel.PeerEndpointURL, "/") + "/sub2api/internal/verify-challenge"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client, err := newSub2APIPeerVerificationClient(channel.PeerEndpointURL, time.Duration(channel.NonceWindowSeconds)*time.Second)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer challenge returned status %d", resp.StatusCode)
	}
	var challengeResponse sub2APIPeerChallengeResponse
	if err := common.DecodeJson(resp.Body, &challengeResponse); err != nil {
		return err
	}
	if challengeResponse.ChallengeID != challenge.ChallengeID {
		return fmt.Errorf("peer challenge_id mismatch")
	}
	if challengeResponse.SignatureScheme != "ed25519-v1" {
		return fmt.Errorf("peer challenge signature_scheme mismatch")
	}
	signature, err := base64.StdEncoding.DecodeString(challengeResponse.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("peer challenge signature must be base64 ed25519 signature")
	}
	message := strings.Join([]string{challenge.ChallengeID, challenge.ChallengeNonce, challenge.BackendID, challenge.ChannelID}, "\n")
	if !ed25519.Verify(ed25519.PublicKey(publicKey), []byte(message), signature) {
		return fmt.Errorf("peer challenge signature verification failed")
	}
	if err := verifySub2APIPeerHealthModelsAndCapacity(client, channel); err != nil {
		return err
	}
	return nil
}

func newSub2APIPeerVerificationClient(peerEndpointURL string, timeout time.Duration) (http.Client, error) {
	parsed, err := url.Parse(peerEndpointURL)
	if err != nil {
		return http.Client{}, err
	}
	allowPrivate := strings.TrimSpace(strings.ToLower(os.Getenv("SUB2API_PEER_ALLOW_PRIVATE_ENDPOINTS"))) == "1"
	if !allowPrivate && parsed.Scheme != "https" {
		return http.Client{}, fmt.Errorf("peer endpoint must use https")
	}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if !allowPrivate {
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if isPrivateSub2APIPeerIP(ip) {
						return nil, fmt.Errorf("peer endpoint resolves to private address")
					}
				}
			}
			return dialer.DialContext(ctx, network, address)
		},
	}
	return http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func isPrivateSub2APIPeerIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func verifySub2APIPeerHealthModelsAndCapacity(client http.Client, channel *model.Sub2APIPeerChannel) error {
	baseURL := strings.TrimRight(channel.PeerEndpointURL, "/")
	var health sub2APIPeerHealthResponse
	if err := getSub2APIPeerJSON(client, baseURL+"/health", &health); err != nil {
		return err
	}
	if health.Status != "online" {
		return fmt.Errorf("peer health status %q is not online", health.Status)
	}
	var models sub2APIPeerModelsResponse
	if err := getSub2APIPeerJSON(client, baseURL+"/models", &models); err != nil {
		return err
	}
	if len(models.Models) == 0 {
		return fmt.Errorf("peer models response is empty")
	}
	var capacity sub2APIPeerCapacityResponse
	if err := getSub2APIPeerJSON(client, baseURL+"/capacity", &capacity); err != nil {
		return err
	}
	if capacity.Capacity == nil {
		return fmt.Errorf("peer capacity response is empty")
	}
	return verifySub2APIPeerModelIntersection(channel, models.Models)
}

func getSub2APIPeerJSON(client http.Client, endpoint string, target any) error {
	resp, err := client.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer endpoint %s returned status %d", endpoint, resp.StatusCode)
	}
	return common.DecodeJson(resp.Body, target)
}

func verifySub2APIPeerModelIntersection(channel *model.Sub2APIPeerChannel, peerModels []string) error {
	peerModelSet := map[string]bool{}
	for _, peerModel := range peerModels {
		peerModelSet[strings.TrimSpace(peerModel)] = true
	}
	var supportedModels []string
	if err := common.UnmarshalJsonStr(channel.SupportedModels, &supportedModels); err != nil {
		return err
	}
	if len(supportedModels) == 0 {
		return errors.New("registered supported_models is empty")
	}
	var modelMapping map[string]string
	if strings.TrimSpace(channel.ModelMapping) != "" {
		if err := common.UnmarshalJsonStr(channel.ModelMapping, &modelMapping); err != nil {
			return err
		}
	}
	for _, modelName := range supportedModels {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return errors.New("registered supported_models contains empty value")
		}
		peerModel := modelName
		if mappedModel := strings.TrimSpace(modelMapping[modelName]); mappedModel != "" {
			peerModel = mappedModel
		}
		if !peerModelSet[peerModel] {
			return fmt.Errorf("peer model %q for registered model %q is not advertised", peerModel, modelName)
		}
	}
	return nil
}
