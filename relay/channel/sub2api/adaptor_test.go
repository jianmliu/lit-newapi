package sub2api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

func TestSetupRequestHeaderSignsPeerChannelRequests(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	t.Setenv("SUB2API_PEER_SIGNING_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	gin.SetMode(gin.TestMode)
	body := `{"model":"gpt-4o-mini"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	common.SetContextKey(c, constant.ContextKeySub2APIPeerChannelId, 7)
	header := http.Header{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl: "https://peer.example.com",
		ApiKey:         "runtime-token",
		ChannelOtherSettings: dto.ChannelOtherSettings{
			Sub2APIEndpointID: "backend-route",
		},
	}}

	if err := (&Adaptor{}).SetupRequestHeader(c, &header, info); err != nil {
		t.Fatalf("setup headers: %v", err)
	}
	if header.Get("X-NewAPI-Signature-Scheme") != "ed25519-v1" || header.Get("X-NewAPI-Channel-ID") != "7" || header.Get("X-NewAPI-Backend-ID") != "backend-route" || header.Get("X-NewAPI-Request-ID") == "" {
		t.Fatalf("missing signature metadata headers: %v", header)
	}
	digest := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(digest[:])
	queryDigest := sha256.Sum256(nil)
	if header.Get("X-NewAPI-Body-Hash") != bodyHash {
		t.Fatalf("body hash = %q, want %q", header.Get("X-NewAPI-Body-Hash"), bodyHash)
	}
	restoredBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(restoredBody) != body {
		t.Fatalf("request body was not restored: %q", string(restoredBody))
	}
	signature, err := base64.StdEncoding.DecodeString(header.Get("X-NewAPI-Signature"))
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	message := strings.Join([]string{
		req.Method,
		"/sub2api/v1/endpoints/backend-route/chat/completions",
		hex.EncodeToString(queryDigest[:]),
		bodyHash,
		header.Get("X-NewAPI-Timestamp"),
		header.Get("X-NewAPI-Nonce"),
		"backend-route",
		"7",
		header.Get("X-NewAPI-Request-ID"),
	}, "\n")
	if !ed25519.Verify(publicKey, []byte(message), signature) {
		t.Fatalf("signature did not verify over peer request metadata")
	}
}
