# Delivered secrets

Secrets are write-only to the API, sealed at rest, and delivered only to the
app they belong to. Gregale injects them into the guest at wake; plaintext
values are never returned, logged, or included in deployment receipts.

```bash
gregale secrets set --app my-api STRIPE_SECRET_KEY="$STRIPE_SECRET_KEY"
gregale secrets set --app my-api DATABASE_URL="$DATABASE_URL" --restart
gregale secrets list --app my-api
gregale secrets unset --app my-api STRIPE_SECRET_KEY
```

Use stdin or an environment variable when setting a value in automation, and
grant CI only `secrets:write` plus the scopes it needs to deploy. Secret names
follow the same uppercase key contract as [environment variables](env.md).
Rotate a value by writing the same key; the operation is audited without
recording the value. Every mutation invalidates snapshots that already exist;
the `--restart` path additionally prevents the live process with the old value
from being captured into a replacement snapshot.

By default, a running process keeps its current environment and the new value
arrives on the next cold wake. Add `--restart` to `secrets set` or `secrets
rotate` to apply immediately. Gregale durably queues a configuration restart,
destroys live VMs without snapshotting their old environment, and cold-boots a
replacement with the current secret set. This restarts the app; in-process
reload without restart is not yet supported.
