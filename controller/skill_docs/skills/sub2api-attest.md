# sub2api-attest

## Goal
Fetch the TEE evidence record for a specific Sub2API call and verify off-chain that the call really executed inside the expected TEE image with the expected signing key. This skill talks to the **Sub2API server directly**, not to the One API gateway.

## Why two surfaces
One API strips the `X-Sub2API-Call-ID` and `X-Sub2API-Attestation-ID` response headers before returning to agents. To verify a specific call, the agent (or the source owner) must call the Sub2API runtime/attest endpoint themselves using the endpoint API key, not the One API token.

## Endpoint
```http
GET /sub2api/v1/attest/{call_id}
Authorization: Bearer <key_id>.<raw_key>
```
Host = the Sub2API server (the `base_url` configured for the channel; not the One API gateway). Auth uses the Sub2API endpoint API key, which is HMAC-SHA256 protected on the server side.

## Response shape
```json
{
  "attestation_id":             "att_...",
  "call_id":                    "call_...",
  "endpoint_id":                "endpoint-a",
  "kind":                       "ATTESTED_REQUEST",
  "request_hash":               "<hex sha256>",
  "response_hash":              "<hex sha256>",
  "sealed_credential_ref_hash": "<hex sha256>",
  "evidence_hash":              "<hex sha256>",
  "signature":                  "<hex 65-byte secp256k1>",
  "tee_image_hash":             "<hex 32-byte image digest>",
  "quote_hash":                 "<hex 32-byte sha256 of the boot TDX quote>",
  "created_at":                 1900000000
}
```

## Verification recipe
1. Decode `evidence_hash` and `signature` from hex.
2. Run `crypto.Ecrecover(evidence_hash, signature)` (go-ethereum) to recover the 65-byte SEC1 public key.
3. Convert to an Ethereum address via `crypto.PubkeyToAddress(crypto.UnmarshalPubkey(pubkey))`.
4. Compare against the address the operator published as the TEE signing-key address for `tee_image_hash`. They must match.
5. (Optional) Compare `quote_hash` against the boot-time TDX quote SHA-256 the operator publicly attested. They must match.
6. `evidence_hash` is built from `sha256(framed(request_hash, response_hash, sealed_credential_ref_hash, tee_image_hash, quote_hash))`; recomputing it locally from the row's columns proves no column was tampered after signing.

## Safety rules
- `signature` is missing (NULL / omitted) on Sub2API deployments that have not yet attached a TEE signing key — those calls have weaker guarantees and the field is omitted via `omitempty`. Treat absent signatures as "not cryptographically verifiable", not "verified false".
- Do not treat `tee_image_hash` as canonical without external attestation; the operator must publish the image hash through a trustworthy channel.
- The endpoint API key authenticates this read; never share it. Anyone with the key can retrieve evidence for any call on the same endpoint.

## Related skills
- `sub2api-quota-usage` to discover `call_id`s.
- `sub2api-inference` for how the call_id is established at request time.
