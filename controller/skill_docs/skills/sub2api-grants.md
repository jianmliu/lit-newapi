# sub2api-grants

## Goal
Share a Sub2API source you own with other One API users so they can route their inference calls through it. Grants are per-source and per-grantee, revocable at any time.

## Endpoint sequence

### List current grants on a source
```http
GET /api/sub2api/sources/:id/grants
Authorization: Bearer <one_api_access_token>
```
Returns an array of `{ "user_id": <int>, "created_at": <unix> }`. Only the source owner may call this.

### Grant access to another user
```http
POST /api/sub2api/sources/:id/grants
Authorization: Bearer <one_api_access_token>
Content-Type: application/json

{ "user_id": 42 }
```
The grantee can now see the source in their `GET /api/sub2api/available-sources` and use it for inference. Granting does not transfer ownership or expose secrets.

### Revoke a grant
```http
DELETE /api/sub2api/sources/:id/grants/:user_id
Authorization: Bearer <one_api_access_token>
```
Removes the row. The grantee loses access on subsequent calls; in-flight calls finish normally.

## Response envelope
All endpoints return `{ "success": bool, "message": string, "data": ... }`. Errors set `success=false`.

## Safety rules
- Only the source owner may list, grant, or revoke. A grantee cannot re-grant onward.
- A user's deletion of a source automatically revokes all grants on it.
- Revoking does not refund any in-flight inference; metering still attributes the call to the original source.
- Do not infer the existence of other users from grant errors; the response is consistent regardless of whether `user_id` is a known user.

## Related skills
- `sub2api-sources` for source registration / deletion.
- `sub2api-marketplace` for how a grantee then quotes and uses the shared source.
