# sub2api-quota-usage

## Goal
Inspect remaining quota and historical usage for a single Sub2API source. Use this skill to plan whether to top up, switch sources, or warn the user before a cost-sensitive call.

## Endpoint sequence

### Quota snapshot
```http
GET /api/sub2api/sources/:id/quota
Authorization: Bearer <one_api_access_token>
```
Returns the current quota state for the endpoint API key backing this source: total / daily limits, used / remaining counters, reset timestamp, and (if available) the latest upstream quota observation parsed from `x-ratelimit-*` headers.

### Usage list
```http
GET /api/sub2api/sources/:id/usage?limit=50
Authorization: Bearer <one_api_access_token>
```
Returns up to `limit` (default 50, max bounded server-side) of the most recent call records: `call_id`, `model_requested`, `model_used`, `input_tokens`, `output_tokens`, `quota_units`, `status`, `created_at`, and the request / response SHA-256 hashes (no raw bodies). Use `call_id` to fetch full TEE evidence via `sub2api-attest`.

## Auth rules
The caller must be the source owner or an active grantee (see `sub2api-grants`); admins can read any source. Non-owners and non-grantees receive an error envelope, never the data.

## Response envelope
All endpoints return `{ "success": bool, "message": string, "data": ... }`. Errors set `success=false`.

## Safety rules
- Treat the quota snapshot as advisory; upstream provider limits may override the cached numbers and the Sub2API runtime can still reject a call.
- `model_used` may differ from `model_requested` when the upstream provider remapped the request.
- Hashes are deliberate substitutes for raw content. Never request a body endpoint from Sub2API — there is none.
- If `status` is `UPSTREAM_FAILED` or `REJECTED`, the call still consumed `1` reserved unit; review your retry policy.

## Related skills
- `sub2api-marketplace` for forecasting quota cost.
- `sub2api-attest` for full per-call TEE evidence (signed `evidence_hash`).
- `sub2api-inference` for the live response headers that mirror these numbers.
