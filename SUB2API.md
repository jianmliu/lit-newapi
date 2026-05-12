# Sub2API Marketplace on New API

This document describes the Sub2API-marketplace surface added on top of upstream new-api in the `jianmliu/lit-newapi` fork. The upstream new-api code, identity, README, and module path are preserved unchanged per its project policy (AGENTS.md Rule 5); everything described below is additive.

## Layout

```
constant/channel.go                       ChannelTypeSub2API = 58
constant/api_type.go                      APITypeSub2API     (before APITypeDummy)
controller/skill_docs/                    Agent-discoverable skill markdown (llms.txt + 7 skills)
controller/sub2api_skill_docs.go          //go:embed serving of skill_docs
controller/sub2api_skill_docs_test.go     Endpoint smoke test
controller/sub2api_source.go              (pending) source registry + marketplace quote + buy
controller/withdrawal.go                  (pending) seller payout submit + admin processing
controller/payment_provider.go            (pending) x402 payment provider interface
dto/channel_settings.go                   +Sub2APIEndpointID, +Sub2APIRuntimeKeyEnv on ChannelOtherSettings
model/sub2api_source.go                   Sub2APISource + Sub2APISourceGrant + helpers
model/withdrawal.go                       Withdrawal four-state machine
model/token.go                            +Sub2APISourceId column on Token
relay/channel/sub2api/adaptor.go          Embeds openai.Adaptor + Sub2API GetChannelName
relay/channel/sub2api/config.go           RuntimeConfig + FromChannelOther + RuntimeRequestURL
middleware/sub2api_runtime.go             (pending) per-request runtime key issue/revoke against Sub2API admin API
router/api-router.go                      /api/sub2api/llms.txt, /api/sub2api/skills/:name registered
                                          (pending) /api/sub2api/{pricing,sources,grants,quota,usage,buy,marketplace/quote}, /api/user/withdrawals, /api/withdrawal
web/{default,classic}/dist/index.html     Frontend embed placeholders (replaced by the Dockerfile bun build)
SUB2API.md                                This file
```

## Auth surface

| Route | Auth | Purpose |
|---|---|---|
| `GET /api/sub2api/llms.txt` | public | Agent-discoverable umbrella spec listing every skill |
| `GET /api/sub2api/skills/:name` | public | Per-skill markdown spec (path-traversal protected) |
| `GET /api/sub2api/pricing` | user bearer | quota-per-USDC and commission rate |
| `GET /api/sub2api/marketplace/quote` | user bearer | Cheapest-source quote for a requested model |
| `POST /api/sub2api/buy` | user bearer + `X-PAYMENT` x402 EIP-3009 | Top up balance with Base USDC |
| `GET /api/sub2api/sources` | user bearer | List sources owned by the caller |
| `GET /api/sub2api/available-sources` | user bearer | List owned + granted sources eligible for routing |
| `POST /api/sub2api/sources` | user bearer | Register a new source against the Sub2API admin API |
| `DELETE /api/sub2api/sources/:id` | user bearer | Revoke the upstream key and delete the row |
| `GET /api/sub2api/sources/:id/grants` | user bearer (owner) | List grants on a source |
| `POST /api/sub2api/sources/:id/grants` | user bearer (owner) | Share a source with another user |
| `DELETE /api/sub2api/sources/:id/grants/:user_id` | user bearer (owner) | Revoke a grant |
| `GET /api/sub2api/sources/:id/quota` | user bearer (owner or grantee) | Quota snapshot for the backing endpoint API key |
| `GET /api/sub2api/sources/:id/usage` | user bearer (owner or grantee) | Recent call records (hashes only, no bodies) |
| `POST /v1/chat/completions` | one-api token bearer | Standard OpenAI-compatible relay; routed through Sub2API when token / cheapest match selects a Sub2API channel |
| `GET /sub2api/v1/attest/{call_id}` | Sub2API endpoint API key (on the Sub2API server, not New API) | TEE evidence retrieval (`evidence_hash` + secp256k1 signature + `tee_image_hash` + `quote_hash`) |
| `POST /api/user/withdrawals` | user bearer | Submit a seller payout request |
| `GET /api/user/withdrawals` | user bearer | List the caller's withdrawal history |
| `GET /api/withdrawal` | admin bearer | Paginated admin view |
| `PUT /api/withdrawal/:id` | admin bearer | Approve / reject (with Base USDC payout when payout_method=`base_usdc`) |

## Channel configuration

A Sub2API channel uses upstream new-api's ChannelOtherSettings JSON column plus two new fields:

```json
{
  "sub2api_endpoint_id": "endpoint-a",
  "sub2api_runtime_key_env": "SUB2API_ENDPOINT_A_RUNTIME_KEY"
}
```

`base_url` points at the Sub2API server root. The channel's `key` column is empty for Sub2API channels; the runtime key is read from the named environment variable at request time. The env-var name must match `^[A-Z][A-Z0-9_]*$` to prevent the channel from accidentally reading process secrets such as `PATH`.

## Secret boundary

New API never persists:
- Sub2API subscription OAuth tokens or provider API keys (held by the Sub2API server only)
- Sub2API endpoint API key raw bytes (the channel row stores only its env-var name)
- Sub2API runtime keys (minted per request, revoked after the relay completes)
- Sealed credential references' contents (only opaque IDs)
- Raw request or response bodies (Sub2API stores SHA-256 hashes only)

## Agent self-learning entry point

```bash
curl -s $ONEAPI_BASE/api/sub2api/llms.txt
curl -s $ONEAPI_BASE/api/sub2api/skills/sub2api-marketplace
```

The seven skills cover the full marketplace surface (`sub2api-marketplace`, `sub2api-sources`, `sub2api-grants`, `sub2api-quota-usage`, `sub2api-inference`, `sub2api-withdrawal`, `sub2api-attest`) and are served verbatim from `controller/skill_docs/` via `//go:embed`.

## Upstream sync

The fork tracks `QuantumNous/new-api` via the `upstream` git remote. When pulling upstream changes:

```bash
git fetch upstream
git merge upstream/main
```

Conflicts only occur in the additive surfaces above (constant tables, dto.ChannelOtherSettings, model/main.go AutoMigrate list, router/api-router.go). All upstream identifiers in those files remain untouched.
