package sub2api

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// runtimeKeyEnvPattern enforces an UPPER_SNAKE_CASE env-var name so a Sub2API
// channel cannot accidentally read an arbitrary user-supplied environment
// value (e.g. PATH) as the upstream bearer token.
var runtimeKeyEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// RuntimeConfig is the per-request Sub2API binding resolved from the channel
// row plus its associated runtime key. RuntimeKey is populated only inside
// the middleware that issued it; it must never be persisted to SQL or logs.
type RuntimeConfig struct {
	BaseURL    string
	EndpointID string
	RuntimeKey string
}

// FromChannelOther reads the Sub2API binding (endpoint id + runtime-key env
// var name) from a channel's OtherSettings JSON column, validates the env var
// shape, and looks up the actual runtime key. Returns a populated
// RuntimeConfig or a descriptive error suitable for surfacing through the
// One API error envelope.
func FromChannelOther(baseURL string, runtimeKey string, settings dto.ChannelOtherSettings) (RuntimeConfig, error) {
	endpointID := strings.TrimSpace(settings.Sub2APIEndpointID)
	runtimeKeyEnv := strings.TrimSpace(settings.Sub2APIRuntimeKeyEnv)
	cfg := RuntimeConfig{
		BaseURL:    strings.TrimSpace(baseURL),
		EndpointID: endpointID,
		RuntimeKey: strings.TrimSpace(runtimeKey),
	}
	if cfg.BaseURL == "" {
		return RuntimeConfig{}, errors.New("sub2api base url is required")
	}
	if cfg.EndpointID == "" {
		return RuntimeConfig{}, errors.New("sub2api endpoint id is required")
	}
	if cfg.RuntimeKey == "" {
		if runtimeKeyEnv == "" {
			return RuntimeConfig{}, errors.New("sub2api runtime key is required")
		}
		if !runtimeKeyEnvPattern.MatchString(runtimeKeyEnv) {
			return RuntimeConfig{}, fmt.Errorf("sub2api runtime key env must match %s", runtimeKeyEnvPattern.String())
		}
		val := strings.TrimSpace(os.Getenv(runtimeKeyEnv))
		if val == "" {
			return RuntimeConfig{}, fmt.Errorf("sub2api runtime key environment variable %q is not set", runtimeKeyEnv)
		}
		cfg.RuntimeKey = val
	}
	return cfg, nil
}

// RuntimeRequestURL rewrites the relay target to the Sub2API runtime path
// /sub2api/v1/endpoints/{endpoint_id}/chat/completions, preserving the
// channel's BaseURL host and any subpath prefix it carries.
func RuntimeRequestURL(cfg RuntimeConfig) (string, error) {
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return "", fmt.Errorf("sub2api base url is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("sub2api base url scheme %q is unsupported", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("sub2api base url host is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/sub2api/v1/endpoints/" + url.PathEscape(cfg.EndpointID) + "/chat/completions"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}
