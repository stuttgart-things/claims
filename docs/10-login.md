# Interactive Login (`claims login`)

`claims login` logs a human in to a claim-machinery-api endpoint with the
OAuth2 **device authorization flow** (RFC 8628) against the endpoint's ZITADEL.
`render` and `encrypt` then send the cached access token as
`Authorization: Bearer …` to that API URL. Pipelines keep using a static token
or client credentials (`CLAIM_API_TOKEN`, `CLAIM_API_OIDC_*`), which win over a
cached login.

## Flow

```
claims login --api-url <api>
  → GET  <issuer>/.well-known/openid-configuration
  → POST device_authorization_endpoint      (client_id, scope)
  → print verification URL + user code       (user approves in the browser)
  → poll token_endpoint                      (device_code grant)
  → cache tokens for <api> in ~/.config/claims/credentials.json (0600)

claims render --api-url <api>
  → access token expired? refresh_token grant, save the new tokens
  → POST /api/v1/claim-templates/{name}/order with the bearer token
```

## Settings

| Flag | Environment | Description |
|------|-------------|-------------|
| `--api-url` / `-a` | `CLAIM_API_URL` (exactly one endpoint) | API URL the login is for; key of the cached login |
| `--issuer` | `CLAIM_API_LOGIN_ISSUER` | ZITADEL issuer URL |
| `--client-id` | `CLAIM_API_LOGIN_CLIENT_ID` | Client ID of the native application |
| `--scope` | `CLAIM_API_LOGIN_SCOPE` | Scopes (default `openid profile email offline_access`) |
| `--ca-file` | `CLAIM_API_LOGIN_CA_FILE` | Extra PEM roots for the issuer (lab Vault PKI) |
| | `CLAIMS_CREDENTIALS_FILE` | Credentials file (default `~/.config/claims/credentials.json`) |

Issuer, client ID, scope and CA file are stored with the login, so a later
`claims login --api-url <api>` needs no further flags. Each API URL has its own
login, because each lab may run its own ZITADEL.

## Commands

```bash
claims login  --api-url https://claims.example.com/claims --issuer https://zitadel.example.com --client-id <client-id> \
  --scope "openid profile email offline_access urn:zitadel:iam:org:project:id:<project-id>:aud urn:zitadel:iam:org:projects:roles"
claims whoami --api-url https://claims.example.com/claims
claims logout --api-url https://claims.example.com/claims   # or: claims logout --all
```

`claims whoami` shows the user, the roles and groups in the token, the
audience, the expiry and the user reference claim-machinery-api stamps into
`requested_by` (`user:default/<preferred_username before @>`). It warns when the
access token is not a JWT, because claim-machinery-api only accepts JWTs.

`claims` never sets `requested_by` itself: with auth on, the API binds
parameters marked `identity: user` to the login and checks `identity: owner`
against the user's groups (claim-machinery-api v0.27.0+).

## Setting up the ZITADEL application

Once per lab ZITADEL, in the project that claim-machinery-api accepts (its
`AUTH_OIDC_AUDIENCE`):

1. **Projects → <project> → Applications → New**, type **Native**,
   authentication method **Device Code**. Name it e.g. `claims-cli`.
2. In the application's **OIDC configuration**, enable the grant types
   **Device Code** and **Refresh Token**.
3. In its **Token settings**, set **Auth Token Type** to **JWT** (opaque access
   tokens are rejected by the API) and enable **User roles inside ID Token** /
   access token as offered, so the roles reach the API.
4. In the project's **General** settings, enable **Assert Roles on
   Authentication** so the project roles are added to the tokens.
5. Grant the users their roles on the project (`backstage-developer`,
   `backstage-team-<team>`, …), the same roles sthings-backstage uses; the
   API maps them to groups for `identity: owner` (`AUTH_ROLE_GROUPS`).
6. Note the application's **Client ID** and the **project ID**. Log in with

   ```
   --scope "openid profile email offline_access urn:zitadel:iam:org:project:id:<project-id>:aud urn:zitadel:iam:org:projects:roles"
   ```

   The `…:aud` scope puts the project ID into the token's `aud`, which must be
   listed in claim-machinery-api's `AUTH_OIDC_AUDIENCE`; `offline_access` gets a
   refresh token; `…:projects:roles` adds the roles claim.

## Errors

| Situation | Message |
|-----------|---------|
| Issuer or client ID missing | `login needs CLAIM_API_LOGIN_ISSUER (--issuer) and …` |
| Issuer without device flow | `issuer … offers no device_authorization_endpoint` |
| Refresh token expired/revoked | `login for <api> expired or was revoked, run claims login --api-url <api> again: invalid_grant` |
| API answers 401 | hint to run `claims login` or set `CLAIM_API_TOKEN` / `CLAIM_API_OIDC_*` |
| API answers 403 | missing order role or owner outside your groups; `claims whoami` shows the token's roles |

Token values are never printed or logged; the credentials file is created with
mode 0600 in a 0700 directory.
