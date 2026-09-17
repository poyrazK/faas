# Authentication

Log in locally with the CLI; it stores a short-lived session in the operating-system keychain.

```bash
gregale login
gregale whoami
gregale logout
```

Browser-approved logins mint a 30-day CLI key tied to this device. The CLI
stores that key's non-secret id and `gregale logout` revokes exactly that key
before clearing the local keychain. Use `gregale keys list` and
`gregale keys rm <id>` to remove a session from a lost device; CLI sessions are
labelled `cli-login` and never expose their plaintext after creation.

For CI, create a scoped project or organization token and expose it as `FAAS_TOKEN`, or pass it once with `gregale login --token "$FAAS_TOKEN"`. Prefer the narrowest scope and a short expiration. Do not commit tokens or pass them as command-line arguments where process listings can expose them.
Tokens supplied through `FAAS_TOKEN` or `gregale login --token` remain
non-owning: logout clears local state but does not revoke a shared CI key.

Interactive users can enable MFA from the account settings page. A `401` means the session or token is missing/expired; a `403` means the identity is valid but lacks the required project or organization scope. Rotate a compromised token immediately and review the audit log.

## Dashboard OAuth and PKCE

The Google and GitHub dashboard authorization-code redirects use RFC 7636
Proof Key for Code Exchange with the `S256` method. Gregale binds a random
code verifier to the browser callback and sends it to the provider token
endpoint, preventing an intercepted authorization code from being redeemed
without the originating browser. This profile covers dashboard sign-in only;
the separate GitHub App installation callback is not part of the PKCE contract.

## OIDC token exchange

Gregale publishes its supported OAuth token-service capabilities at the RFC
8414 well-known endpoint:

```bash
curl https://api.gregale.dev/.well-known/oauth-authorization-server
```

The metadata advertises the RFC 8693 token-exchange grant, the `deploy:write`
scope, and JWT subject tokens. It intentionally does not claim a general
customer authorization server, authorization-code flow, refresh tokens, or
dynamic client registration.

CI runners can exchange an IdP-issued JWT for a short-lived deploy bearer using
the RFC 8693 form profile:

```bash
curl -X POST https://api.example.com/v1/auth/oidc/exchange \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=urn:ietf:params:oauth:grant-type:token-exchange' \
  --data-urlencode 'subject_token_type=urn:ietf:params:oauth:token-type:jwt' \
  --data-urlencode "subject_token=$OIDC_TOKEN" \
  --data-urlencode 'audience=faas.example.com'
```

The response uses the OAuth token shape (`access_token`, `token_type`,
`issued_token_type`, `expires_in`, and `scope`). Gregale's profile issues only
`deploy:write` bearer tokens; the existing JSON body (`provider`, `token`, and
`aud`) remains available for clients that use the original contract.
