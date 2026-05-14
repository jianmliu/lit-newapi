package sub2api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// Adaptor is the relay channel adaptor for the Sub2API marketplace runtime.
// Sub2API endpoints speak OpenAI-compatible request/response, so we inherit
// openai.Adaptor verbatim for body conversion, streaming, and usage parsing
// and override only the three points where Sub2API differs:
//   - GetRequestURL rewrites to /sub2api/v1/endpoints/{endpoint_id}/chat/completions
//   - SetupRequestHeader injects the runtime key (resolved either from
//     info.ApiKey when a middleware has pre-minted one, or from the
//     channel's OtherSettings env-var name) as the upstream Bearer token
//   - DoResponse strips X-Sub2API-Call-ID and X-Sub2API-Attestation-ID from
//     the upstream response headers before they reach the agent caller. The
//     X-Sub2API-Request/Response-Metering-Hash pair is intentionally kept
//     because those hashes are agent-safe and useful for reconciliation.
type Adaptor struct {
	openai.Adaptor
}

var privateResponseHeaderNames = []string{
	"X-Sub2API-Call-ID",
	"X-Sub2API-Attestation-ID",
}

func (a *Adaptor) GetChannelName() string {
	return "Sub2API"
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	cfg, err := FromChannelOther(info.ChannelBaseUrl, info.ApiKey, info.ChannelOtherSettings)
	if err != nil {
		return "", err
	}
	return RuntimeRequestURL(cfg)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	cfg, err := FromChannelOther(info.ChannelBaseUrl, info.ApiKey, info.ChannelOtherSettings)
	if err != nil {
		return err
	}
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Authorization", "Bearer "+cfg.RuntimeKey)
	if err := setupPeerChannelSignature(c, header, cfg); err != nil {
		return err
	}
	return nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if common.GetContextKeyInt(c, constant.ContextKeySub2APIPeerChannelId) <= 0 || requestBody == nil {
		return a.Adaptor.DoRequest(c, info, requestBody)
	}
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	common.SetContextKey(c, constant.ContextKeySub2APIPeerBodyHash, sha256Hex(body))
	return a.Adaptor.DoRequest(c, info, bytes.NewReader(body))
}

func setupPeerChannelSignature(c *gin.Context, header *http.Header, cfg RuntimeConfig) error {
	peerChannelID := common.GetContextKeyInt(c, constant.ContextKeySub2APIPeerChannelId)
	if peerChannelID <= 0 {
		return nil
	}
	privateKeyText := strings.TrimSpace(os.Getenv("SUB2API_PEER_SIGNING_PRIVATE_KEY"))
	if privateKeyText == "" {
		return fmt.Errorf("SUB2API_PEER_SIGNING_PRIVATE_KEY is required for peer channel requests")
	}
	privateKey, err := base64.StdEncoding.DecodeString(privateKeyText)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("SUB2API_PEER_SIGNING_PRIVATE_KEY must be base64 ed25519 private key")
	}
	runtimeURL, err := RuntimeRequestURL(cfg)
	if err != nil {
		return err
	}
	parsedRuntimeURL, err := url.Parse(runtimeURL)
	if err != nil {
		return err
	}
	bodyHash := common.GetContextKeyString(c, constant.ContextKeySub2APIPeerBodyHash)
	if bodyHash == "" {
		var err error
		bodyHash, err = hashAndRestoreRequestBody(c.Request)
		if err != nil {
			return err
		}
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := common.GetUUID()
	peerID := strconv.Itoa(peerChannelID)
	requestID := common.GetUUID()
	queryHash := sha256Hex([]byte(parsedRuntimeURL.RawQuery))
	message := strings.Join([]string{c.Request.Method, parsedRuntimeURL.Path, queryHash, bodyHash, timestamp, nonce, cfg.EndpointID, peerID, requestID}, "\n")
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), []byte(message))
	header.Set("X-NewAPI-Signature-Scheme", "ed25519-v1")
	header.Set("X-NewAPI-Backend-ID", cfg.EndpointID)
	header.Set("X-NewAPI-Channel-ID", peerID)
	header.Set("X-NewAPI-Request-ID", requestID)
	header.Set("X-NewAPI-Timestamp", timestamp)
	header.Set("X-NewAPI-Nonce", nonce)
	header.Set("X-NewAPI-Body-Hash", bodyHash)
	header.Set("X-NewAPI-Signature", base64.StdEncoding.EncodeToString(signature))
	return nil
}

func hashAndRestoreRequestBody(req *http.Request) (string, error) {
	if req == nil || req.Body == nil {
		return sha256Hex(nil), nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return sha256Hex(body), nil
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	stripPrivateResponseHeaders(resp.Header)
	usage, newAPIError := a.Adaptor.DoResponse(c, resp, info)
	if newAPIError == nil {
		recordPeerUsage(c, info, usage)
	}
	return usage, newAPIError
}

func recordPeerUsage(c *gin.Context, info *relaycommon.RelayInfo, usage any) {
	peerChannelID := common.GetContextKeyInt(c, constant.ContextKeySub2APIPeerChannelId)
	if peerChannelID <= 0 || info == nil {
		return
	}
	var parsedUsage dto.Usage
	switch v := usage.(type) {
	case dto.Usage:
		parsedUsage = v
	case *dto.Usage:
		if v == nil {
			return
		}
		parsedUsage = *v
	default:
		return
	}
	var channel model.Sub2APIPeerChannel
	if err := model.DB.First(&channel, peerChannelID).Error; err != nil {
		return
	}
	requestID := info.RequestId
	if requestID == "" {
		requestID = common.GetUUID()
	}
	_, _ = model.RecordSub2APIPeerUsage(model.Sub2APIPeerUsageRecordRequest{
		RequestID:                      requestID,
		ConsumerUserID:                 info.UserId,
		ConsumerTokenID:                info.TokenId,
		PeerChannelID:                  peerChannelID,
		ProviderUserID:                 channel.ProviderUserId,
		Model:                          info.OriginModelName,
		MappedUpstreamModel:            channel.BackendID,
		LocalEstimatedPromptTokens:     parsedUsage.PromptTokens,
		LocalEstimatedCompletionTokens: parsedUsage.CompletionTokens,
		PeerReportedPromptTokens:       parsedUsage.PromptTokens,
		PeerReportedCompletionTokens:   parsedUsage.CompletionTokens,
	})
}

func stripPrivateResponseHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for _, name := range privateResponseHeaderNames {
		headers.Del(name)
	}
	for name := range headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-sub2api-call-") || strings.HasPrefix(lower, "x-sub2api-attest") {
			delete(headers, name)
		}
	}
}
