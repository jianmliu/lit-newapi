# sub2api-marketplace

## Goal
Use One API as a quota marketplace: buy balance with Base USDC, quote available Sub2API endpoint prices, then call OpenAI-compatible inference through the cheapest eligible source unless the token is explicitly bound to another source.

## Endpoint sequence
1. `GET /api/sub2api/pricing` with `Authorization: Bearer <one_api_access_token>`.
2. `GET /api/sub2api/marketplace/quote?model=<model>&estimated_quota=<quota>` before a cost-sensitive call.
3. If balance is too low, `POST /api/sub2api/buy` with JSON `{ "usdPaid": <amount> }` and `X-PAYMENT`.
4. `POST /v1/chat/completions` with the caller's One API token and a normal OpenAI-compatible request body. See `sub2api-inference` for the response shape and the source-binding rule.

## Quote response fields
- `quota_per_usdc`: One API quota units granted per 1 USDC.
- `system_commission_rate`: One API marketplace commission retained from source-owner revenue.
- `selection_policy`: Human-readable routing rule.
- `selected_source`: The first source One API would choose for an unbound token and requested model.
- `sources`: Agent-safe source list sorted by `price_multiplier` ascending.
- `price_multiplier`: `1.0` standard, `<1.0` discount, `>1.0` premium.
- `estimated_quota`: `estimated_base_quota * price_multiplier`, rounded like runtime settlement.
- `estimated_usdc`: `estimated_quota / quota_per_usdc`.

## Safety rules
- Do not ask for or store Sub2API runtime keys, credential IDs, tenant IDs, endpoint IDs, or key IDs.
- Treat quote results as advisory until the inference call settles; actual token usage determines final quota.
- If the One API token has `sub2_api_source_id`, explicit binding overrides the cheapest-source recommendation (see `sub2api-inference`).
- Retry inference only according to One API and upstream HTTP status. Do not retry `POST /api/sub2api/buy` without a fresh x402 authorization unless the client implements its own idempotency boundary.

## x402 payment header
- If `POST /api/sub2api/buy` is missing `X-PAYMENT`, One API returns HTTP 402 plus `X-PAYMENT-REQUIRED` JSON containing `version: "x402-1"`, `scheme: "eip-3009"`, `amount`, and, when configured, `chain_id`, `usdc_address`, and `pay_to`.
- Retry with `X-PAYMENT` set to a JSON EIP-3009 authorization signed for the exact `amount` in USDC atoms; underpayment and overpayment are both rejected.
- The authorization recipient (`to`) must be the `pay_to` hot wallet and the EIP-712 domain must use the configured USDC contract as `verifyingContract`.

## Example quote request
```http
GET /api/sub2api/marketplace/quote?model=gpt-4.1-mini&estimated_quota=3000
Authorization: Bearer <one_api_access_token>
```

## Example quote response
```json
{
  "success": true,
  "data": {
    "currency": "USDC",
    "network": "base",
    "quota_per_usdc": 500000,
    "system_commission_rate": 0.05,
    "requested_model": "gpt-4.1-mini",
    "estimated_base_quota": 3000,
    "selection_policy": "Explicit token sub2_api_source_id binding wins; otherwise One API selects the lowest price_multiplier active source for the requested model.",
    "selected_source": {
      "id": 7,
      "name": "discount source",
      "provider": "openai",
      "model": "gpt-4.1-mini",
      "base_url": "https://api.openai.com/v1",
      "owner_user_id": 12,
      "owned_by_caller": false,
      "price_multiplier": 0.5,
      "estimated_quota": 1500,
      "estimated_usdc": 0.003,
      "selection_preference": 1
    },
    "sources": []
  }
}
```
