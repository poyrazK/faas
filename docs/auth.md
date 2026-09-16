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
