# sub2api-sources

## Goal
Register, list, inspect, and delete the Sub2API endpoint sources that a One API user owns or has been granted access to. A source is the marketplace-facing record for one Sub2API endpoint plus its price multiplier and rate limits.

## Endpoint sequence

### List sources you own or have been granted
```http
GET /api/sub2api/sources
Authorization: Bearer <one_api_access_token>
```

### List all sources eligible for routing for the current user
```http
GET /api/sub2api/available-sources
Authorization: Bearer <one_api_access_token>
```
Returns the same agent-safe fields as the marketplace quote: `id`, `name`, `provider`, `model`, `base_url`, `owner_user_id`, `owned_by_caller`, `price_multiplier`. No runtime keys or credential IDs are exposed.

### Register a new source
```http
POST /api/sub2api/sources
Authorization: Bearer <one_api_access_token>
Content-Type: application/json

{
  "name": "openai-discount",
  "provider": "openai",
  "model": "gpt-4.1-mini",
  "base_url": "https://api.openai.com/v1",
  "credential_type": "api_key",
  "quota_source": "subscription",
  "price_multiplier": 0.8,
  "quota_limit_total_units": 1000000,
  "quota_limit_day_units": 50000,
  "rate_limit_per_minute": 60,
  "credential": { "access_token": "<provider_credential>" }
}
```
Either `access_token` / `api_key` at the top level or a `credential` object with provider-specific keys is acceptable. Server-side this calls the Sub2API admin API to create tenant + credential + endpoint + endpoint-API-key and stores only non-secret IDs (`tenant_id`, `credential_id`, `endpoint_id`, `key_id`) plus the marketplace fields in One API's SQL. The Sub2API runtime key is returned once in the response and must be kept by the caller.

### Delete a source
```http
DELETE /api/sub2api/sources/:id
Authorization: Bearer <one_api_access_token>
```
Best-effort revokes the Sub2API endpoint API key, then deletes the One API row. Granted users lose access automatically.

## Response envelope
All endpoints return `{ "success": bool, "message": string, "data": ... }`. Errors set `success=false`.

## Safety rules
- Do not log or persist the `runtime_token` returned by `POST /api/sub2api/sources`; treat it as a one-shot bearer for a single backing Sub2API key.
- `price_multiplier` below 1.0 means you charge buyers less than the system default. Above 1.0 is premium pricing.
- Sources you create are tied to your One API user; transferring requires `sub2api-grants`.
- Deleting a source is irreversible.

## Related skills
- `sub2api-grants` for sharing a source with other users.
- `sub2api-quota-usage` for inspecting per-source consumption.
- `sub2api-marketplace` for the discovery + quote flow that consumes these sources.
