package controller

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

	"github.com/gin-gonic/gin"
)

const sub2APIManagementTimeout = 20 * time.Second

// sub2APIAdminClient is the privileged HTTP client that One API uses to mint
// and inspect Sub2API tenants, credentials, endpoints, and endpoint API keys
// on behalf of the user creating a Sub2API source. The admin bearer is read
// from the SUB2API_MANAGEMENT_ADMIN_BEARER env var; it must never leak into a
// per-user response, so all helpers return generic error messages and surface
// only the upstream JSON error string (which the Sub2API server is expected
// not to echo bearer material).
type sub2APIAdminClient struct {
	baseURL string
	bearer  string
	client  *http.Client
}

func newSub2APIAdminClient() (*sub2APIAdminClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SUB2API_MANAGEMENT_BASE_URL")), "/")
	bearer := strings.TrimSpace(os.Getenv("SUB2API_MANAGEMENT_ADMIN_BEARER"))
	if baseURL == "" || bearer == "" {
		return nil, errors.New("Sub2API management backend is not configured")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Sub2API management backend URL is invalid")
	}
	return &sub2APIAdminClient{
		baseURL: baseURL,
		bearer:  bearer,
		client:  &http.Client{Timeout: sub2APIManagementTimeout},
	}, nil
}

func (client *sub2APIAdminClient) post(c *gin.Context, path string, payload any) (map[string]any, error) {
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return client.do(req)
}

func (client *sub2APIAdminClient) get(c *gin.Context, path string, query url.Values) (map[string]any, error) {
	endpoint := client.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	return client.do(req)
}

func (client *sub2APIAdminClient) do(req *http.Request) (map[string]any, error) {
	req.Header.Set("Authorization", "Bearer "+client.bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.client.Do(req)
	if err != nil {
		return nil, errors.New("Sub2API management request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("Sub2API management response could not be read")
	}
	var decoded map[string]any
	if len(bytes.TrimSpace(body)) > 0 {
		_ = common.Unmarshal(body, &decoded)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
