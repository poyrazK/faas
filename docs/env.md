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
local workflows with sealed secrets: pull downloads a key-only skeleton with
blank values, never secret plaintext.

The effective precedence is OS environment, manifest environment, API
environment, then sealed secrets. Changes invalidate cached snapshots so old
configuration cannot return through restore. A running process keeps its
current environment unless `--restart` requests a fresh cold boot.

The dashboard can explicitly import mutable plaintext env from a file or
pasted block after a dry-run review. The current API writes one key at a
time; the dashboard reports partial failures rather than claiming a
transaction. Unmentioned variables are preserved.

An authorized dashboard export uses the dedicated POST env-export endpoint,
requires acknowledgement of sensitive values, and downloads only the selected
app scope's mutable plaintext env. Metadata lists continue to omit values.
Sealed secrets, manifest env, and image defaults are excluded. Keep the
download private and out of version control.
