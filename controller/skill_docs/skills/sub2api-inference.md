# sub2api-inference

## Goal
Run an OpenAI-compatible inference call through One API and read the Sub2API metering headers attached to the response. This skill assumes you already have a valid One API access token and (if you want explicit source control) a token bound to a `sub2_api_source_id`.

## Endpoint
```http
POST /v1/chat/completions
Authorization: Bearer <one_api_access_token>
Content-Type: application/json

{
  "model": "gpt-4.1-mini",
  "messages": [
    { "role": "user", "content": "hello" }
  ]
}
```
Any other OpenAI-compatible endpoint (`/v1/completions`, `/v1/embeddings`, etc.) works the same way as long as the underlying channel supports it.

## Source selection
1. If the One API token was created with a specific `sub2_api_source_id`, that source is used regardless of price.
2. Otherwise, One API selects the lowest `price_multiplier` active source for the requested model among sources owned by the caller plus all sources granted to the caller.
3. If no eligible source exists, One API returns `{ "success": false, "message": "no available sub2api source" }`.

To bind a token to a source ahead of time, use the One API stock token API (`POST /api/token` with `sub2_api_source_id`).

## Runtime headers
On success, One API forwards the upstream response body unchanged and adds the following response headers (originating from the Sub2API runtime), which agents can record off-band:

| Header | Meaning |
|---|---|
| `X-Sub2API-Request-Metering-Hash` | hex(sha256(request_body)) |
| `X-Sub2API-Response-Metering-Hash` | hex(sha256(response_body)) |

The Sub2API server's `X-Sub2API-Call-ID` and `X-Sub2API-Attestation-ID` are stripped at the One API gateway before they reach the agent (privacy boundary). To retrieve the full call evidence including those IDs, see `sub2api-attest` and call the Sub2API server directly with the endpoint API key.

## Body limits
Sub2API enforces an 8 MiB cap on both request and response bodies. Streaming is supported when the upstream model supports SSE.

## Safety rules
- Do not embed `X-PAYMENT` or `Authorization` headers from the token issuer / payer into the request body; the gateway handles them out-of-band.
- Treat `4xx` and `5xx` errors as OpenAI-compatible: parse `error.message`. `429` indicates either One API token quota exhaustion or upstream rate limit; honor `Retry-After` when present.
- Body checksums in the response headers are agent-safe — Sub2API never logs the bodies themselves.

## Related skills
- `sub2api-marketplace` for forecasting before this call.
- `sub2api-quota-usage` for after-the-fact usage history.
- `sub2api-attest` for cryptographically verifiable evidence of this call.
