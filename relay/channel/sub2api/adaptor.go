package sub2api

import (
	"net/http"
	"strings"

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
	return nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	stripPrivateResponseHeaders(resp.Header)
	return a.Adaptor.DoResponse(c, resp, info)
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

