# Sealed secrets

Secrets are write-only to the API and are injected into the guest at wake.
Plaintext values are never returned, logged, or included in deployment
receipts.

```bash
gregale secrets set --app my-api STRIPE_SECRET_KEY="$STRIPE_SECRET_KEY"
gregale secrets list --app my-api
gregale secrets unset --app my-api STRIPE_SECRET_KEY
```

Use stdin or an environment variable when setting a value in automation, and
grant CI only `secrets:write` plus the scopes it needs to deploy. Secret names
follow the same uppercase key contract as [environment variables](env.md).
Rotate a value by writing the same key; the operation is audited without
recording the value.
