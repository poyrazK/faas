# Environment variables

Environment variables are mutable, non-secret configuration applied on the
next wake. Store credentials with [sealed secrets](secrets.md) instead.

```bash
gregale env pull --app my-api
printf 'LOG_LEVEL=info\n' | gregale env push --app my-api --from-stdin
gregale env diff --app my-api
```

Keys must be uppercase and begin with a letter (`^[A-Z][A-Z0-9_]*$`). Values
are bounded by the plan. `gregale env pull` and `gregale env push` support
local workflows; pull never returns sealed secret values.

The effective precedence is OS environment, manifest environment, API
environment, then sealed secrets. Changes do not invalidate a snapshot; the
new values are staged on the next wake.
