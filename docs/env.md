# Environment variables

Environment variables are mutable, non-secret configuration applied on the
next cold wake. Store credentials with [sealed secrets](secrets.md) instead.

```bash
gregale env pull --app my-api
printf 'LOG_LEVEL=info\n' | gregale env push --app my-api --from-stdin
printf 'LOG_LEVEL=debug\n' | gregale env push --app my-api --from-stdin --restart
gregale env diff --app my-api
```

Keys must be uppercase and begin with a letter (`^[A-Z][A-Z0-9_]*$`). Values
are bounded by the plan. `gregale env pull` and `gregale env push` support
local workflows; pull never returns sealed secret values.

The effective precedence is OS environment, manifest environment, shared
project configuration, the app's own variables, then sealed secrets. Changes
invalidate cached snapshots so old configuration cannot return through restore.
A running process keeps its current environment unless `--restart` requests a
fresh cold boot.

## Shared project configuration

Every service of a project environment inherits the variables in that
environment's configuration, so values the services share are set once:

```bash
cat > production.json <<'EOF'
{"LOG_LEVEL": "info", "ORDERS_URL": "http://orders.svc.gregale", "WORKERS": 4}
EOF
gregale projects environments config set shop production --file production.json --yes
```

`config set` replaces the whole document, so include every key you want to
keep. A top-level key becomes an environment variable when it is a valid
variable name (`^[A-Z][A-Z0-9_]*$`) and its value is a string, number or
boolean; numbers keep their JSON text and booleans become `true` or `false`.
Other keys, and objects, arrays and nulls, stay in the document without
reaching the services. Secret-shaped names such as `*_TOKEN` or `*_PASSWORD`
are rejected; keep credentials in [sealed secrets](secrets.md).

A service's own variable with the same key wins, so any service can override a
shared value with `gregale env push`. Deployments without an environment read
`production`. Apps outside a project inherit nothing. Scheduled Jobs managed
from Git run exactly the variables in their reviewed definition.

A change that alters the delivered variables invalidates the cached snapshots
of every service in that environment, so each one's next wake boots with the
new values. A change to `production` also refreshes deployments without an
environment. Changing only document-only keys refreshes nothing.
