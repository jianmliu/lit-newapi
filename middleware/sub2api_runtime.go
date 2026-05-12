package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const sub2APIRequestRuntimeKeyTimeout = 20 * time.Second

// IssueSub2APIRuntimeKey mints a per-request runtime key against the Sub2API
// management backend. The minted key has a unique key_id (oneapi-token-<tokenId>-<uuid>),
// is scoped to a single model via model_allowlist, and inherits the source's
// tenant. The runtime key is returned to the caller and MUST be passed to
// RevokeSub2APIRuntimeKey when the relay completes (success or failure) to
// prevent long-lived leaked keys.
//
// Wiring is intentionally manual rather than a gin handler: it is invoked by
// the relay distributor at the moment a Sub2API channel is selected, and the
// revoke call happens in a deferred cleanup. The endpoint_id, tenant_id, and
// source identity are read from the *model.Sub2APISource the distributor
// already resolved from the token's Sub2APISourceId or the cheapest-source
// search.
func IssueSub2APIRuntimeKey(c *gin.Context, token *model.Token, source *model.Sub2APISource) (runtimeKey string, keyID string, err error) {
	if token == nil || source == nil {
		return "", "", errors.New("sub2api runtime key issue requires token + source")
	}
	client, err := newSub2APIRequestRuntimeClient()
	if err != nil {
		return "", "", err
	}
	keyID = fmt.Sprintf("oneapi-token-%d-%s", token.Id, common.GetUUID())
	data, err := client.post(c, "/sub2api/v1/endpoints/"+url.PathEscape(source.EndpointID)+"/keys", map[string]any{
		"tenant_id":             source.TenantID,
		"key_id":                keyID,
		"name":                  fmt.Sprintf("One API token %d request", token.Id),
		"model_allowlist":       []string{source.Model},
		"rate_limit_per_minute": 0,
	})
	if err != nil {
		return "", "", err
	}
	runtimeKey, ok := data["token"].(string)
	if !ok || strings.TrimSpace(runtimeKey) == "" {
		return "", "", errors.New("Sub2API management did not return a runtime token")
	}
	return strings.TrimSpace(runtimeKey), keyID, nil
}

// RevokeSub2APIRuntimeKey attempts to revoke a previously minted runtime key
// on the Sub2API management backend. It is invoked from a deferred cleanup
// after the relay completes; errors are logged via common.SysLog and never
// surfaced to the caller because a failed revoke must not turn a successful
// inference response into a user-visible failure.
func RevokeSub2APIRuntimeKey(c *gin.Context, source *model.Sub2APISource, keyID string) {
	if source == nil || strings.TrimSpace(keyID) == "" {
		return
	}
	client, err := newSub2APIRequestRuntimeClient()
	if err != nil {
		common.SysLog("Sub2API runtime key revoke skipped: " + err.Error())
		return
	}
	if _, err := client.post(c, "/sub2api/v1/keys/"+url.PathEscape(keyID)+"/revoke", map[string]any{
		"tenant_id": source.TenantID,
	}); err != nil {
		common.SysLog("Sub2API runtime key revoke failed: " + err.Error())
	}
}

type sub2APIRequestRuntimeClient struct {
	baseURL string
	bearer  string
	client  *http.Client
}

func newSub2APIRequestRuntimeClient() (*sub2APIRequestRuntimeClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SUB2API_MANAGEMENT_BASE_URL")), "/")
	bearer := strings.TrimSpace(os.Getenv("SUB2API_MANAGEMENT_ADMIN_BEARER"))
	if baseURL == "" || bearer == "" {
		return nil, errors.New("Sub2API management backend is not configured")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Sub2API management backend URL is invalid")
	}
	return &sub2APIRequestRuntimeClient{
		baseURL: baseURL,
		bearer:  bearer,
		client:  &http.Client{Timeout: sub2APIRequestRuntimeKeyTimeout},
	}, nil
}

func (client *sub2APIRequestRuntimeClient) post(c *gin.Context, path string, payload any) (map[string]any, error) {
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+client.bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.client.Do(req)
	if err != nil {
		return nil, errors.New("Sub2API management request failed")
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("Sub2API management response could not be read")
	}
	var decoded map[string]any
	if len(bytes.TrimSpace(responseBody)) > 0 {
		_ = common.Unmarshal(responseBody, &decoded)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := "Sub2API management request failed"
		if errText, ok := decoded["error"].(string); ok && errText != "" {
			message = errText
		}
		return nil, fmt.Errorf("%s", message)
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}
