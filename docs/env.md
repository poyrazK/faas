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

Project configuration keeps its own versions. A change to it is not
recorded in an app's config history, and no config guard watches it.

## Config history

Every change to an app's variables or secrets records a config revision of
the scope: who changed what, and when. `gregale env history` lists the newest
revisions with the config guard's verdict. The newest 100 per scope are
kept.

```bash
gregale env history --app my-api
gregale env rollback 12 --app my-api
```

`gregale env rollback <revision>` sets and deletes variables until the scope
matches that revision, as one change. Revisions never hold secret values, so a
rollback cannot restore a secret; it lists every secret that differs, with
both versions, so you can set it again with `gregale secrets set`.

## Config guard

A config change to a scope with a live deployment opens a config guard. The
guard compares the first traffic served with the new config against the same
app's traffic in the hour before the change. Several keys changed together,
as `gregale env push` does, are judged and restored together.

If the new config fails, the guard restores the previous config and restarts
the app, so instances still running the failed config are replaced. Failing
means at least 5 server errors that are at least half of the new-config
requests, or, on larger samples, the 5xx regression rule a canary rollout
aborts on. A change that fixes an already failing app is never reverted to
the failing config. The guard judges for 10 minutes from the first request
served with the new config, and gives up after 24 hours without traffic.

- Replaced secret values are kept, sealed, only while their guard is open, so
  the guard can restore them.
- A deleted or rotated secret is never restored automatically. If the app
  fails after such a change, the guard records the regression and leaves the
  config as it is.
- A release deployed while a guard is open is judged by its own rollout, not
  blamed on the config.

`gregale secrets set` and `gregale env push` say when a guard is watching.
Pass `--no-guard` to change config without one, for example while you fix a
deliberately broken staging app.
