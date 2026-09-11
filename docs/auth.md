# Authentication

Log in locally with the CLI; it stores a short-lived session in the operating-system keychain.

```bash
gregale login
gregale whoami
gregale logout
```

For CI, create a scoped project or organization token and expose it as `FAAS_TOKEN`, or pass it once with `gregale login --token "$FAAS_TOKEN"`. Prefer the narrowest scope and a short expiration. Do not commit tokens or pass them as command-line arguments where process listings can expose them.

Interactive users can enable MFA from the account settings page. A `401` means the session or token is missing/expired; a `403` means the identity is valid but lacks the required project or organization scope. Rotate a compromised token immediately and review the audit log.
