# sub2api-withdrawal

## Goal
A Sub2API source owner ("seller") submits a payout request for accrued source-owner revenue and tracks its status until settlement.

## Endpoint sequence

### Submit a withdrawal request
```http
POST /api/user/withdrawals
Authorization: Bearer <one_api_access_token>
Content-Type: application/json

{
  "quota":          1000000,
  "currency":       "USDC",
  "payout_method":  "base_usdc",
  "payout_account": "0xYourReceivingAddress"
}
```
- `quota`: integer One API quota units to withdraw; deducted from the seller's accrued balance on submit.
- `currency`: target settlement currency (e.g. `USDC`).
- `payout_method`: `base_usdc` to settle on Base via the operator hot wallet, or other configured methods.
- `payout_account`: recipient address / external account ID for the chosen method.

Returns the created `Withdrawal` row: `id`, `user_id`, `quota`, `currency`, `payout_method`, `payout_account`, `status` (initially `pending`), `created_at`.

### List your withdrawals
```http
GET /api/user/withdrawals
Authorization: Bearer <one_api_access_token>
```
Returns all withdrawal rows for the current user, including ones already approved, rejected, or settled. Status transitions are monotonic: `pending → approved → settled` or `pending → rejected → refunded`.

### (Admin) inspect any withdrawal
```http
GET /api/withdrawal?p=<page>&status=<filter>
Authorization: Bearer <one_api_admin_token>
```
Admin-only. `p` is the page number, `status` filters by literal status string.

### (Admin) approve / reject / settle a withdrawal
```http
PUT /api/withdrawal/:id
Authorization: Bearer <one_api_admin_token>
Content-Type: application/json

{ "status": "approved", "remark": "manual review ok" }
```
Approving with `payout_method=base_usdc` triggers an automatic Base USDC payout from the operator hot wallet (see `controller/withdrawal.go:shouldPayoutBaseUSDC`); the resulting Ethereum tx hash is appended to the withdrawal record. Rejecting refunds the locked quota back to the seller.

## Response envelope
All endpoints return `{ "success": bool, "message": string, "data": ... }`. Errors set `success=false`.

## Safety rules
- The withdrawal endpoint debits the seller's balance on submit, not on approve. Cancelling a pending request is admin-only via the reject path; agents must not retry on transient errors.
- `payout_account` is opaque to One API — the agent is responsible for supplying a valid address / account ID for the chosen `payout_method`.
- Base USDC payouts are sent from a single hot wallet configured server-side. If the wallet is unfunded the payout fails closed; the withdrawal stays `approved` without a tx hash and an operator must retry.
- Treat the `id` of a created withdrawal as the only reliable handle; status polling is idempotent and safe.

## Related skills
- `sub2api-quota-usage` for the revenue side: usage records that accrued the balance you are now withdrawing.
- `sub2api-marketplace` for the buyer-side `POST /api/sub2api/buy` flow (opposite direction of value).
