# secret-reload-node

A Node.js + PostgreSQL reference app for in-process secret rotation. It handles
`SIGHUP`, reads Gregale's atomic secret snapshot, creates and tests a new
Postgres pool before swapping it in, and ACKs the opaque revision only after
the credentials work. If the new credentials fail, the old pool stays active
and the app reports only a non-sensitive `failed` acknowledgement.

This is a focused starter, not a production service. Add your own routes,
migrations, and operational policy without logging secret values or driver
errors that may include a connection URL.

## Configure and deploy

Create a secrets file outside this directory and restrict its permissions:

```sh
cat > ../secret-reload-node.secrets <<'EOF'
DATABASE_URL=postgres://user:password@host:5432/database?sslmode=require
EOF
chmod 600 ../secret-reload-node.secrets
gregale deploy --template secret-reload-node --name <slug> \
  --secrets-file ../secret-reload-node.secrets
```

The Dockerfile opts into `SIGHUP` delivery and sets
`com.gregale.secret-reload-readiness="required"`. The starter installs its
handler before any awaited initialization, then calls `markSecretReloadReady()`
to write the per-process marker advertised by `FAAS_SECRETS_RELOAD_READY_FILE`.
The marker permits notification; only the separate revision ACK attests that
the credentials were applied. Older guests without that variable need no marker.
Only secrets authorized for this app are projected into its runtime files.
The helper reads `FAAS_SECRETS_SNAPSHOT_FILE` as a single `{revision,secrets}`
envelope. An advertised snapshot must be readable and valid; failures never
fall back to separate files. Older guests without that environment variable
retain the previous separate-file reader, which cannot guarantee a consistent
pair during publication. Replace the guest to obtain the atomic contract.
The app validates the database URL without printing it, tests a candidate
connection, then swaps pools. `/healthz` returns a generic error if the active
database is unavailable.

On startup, `applyInitialSecretSnapshot()` waits for the first valid revision.
A valid secret map with an empty initial revision means the host has not yet
published its first version. The helper polls with delays from 100 ms to two
seconds, with a default 30-second deadline. It neither applies nor acknowledges
those unversioned values. Malformed data, invalid nonempty revisions and read
errors still fail immediately; an advertised atomic snapshot never falls back
to the legacy paths. Change the `startupTimeoutMs` option in `handler.js` if
your application's initial publication needs a different budget.

The deadline covers waiting for the initial projection. Database setup and ACK
transport retain their own behavior and timeouts. HTTP serving begins only
after application and an accepted ACK. A rotation while starting rereads the
latest projection after a stale ACK, using strict revision validation. Reloads
stay serialized behind startup. `SIGTERM` and `SIGINT` cancel the wait and
subsequent ACK work; a database candidate completing after shutdown is drained
without becoming active. Timeout or invalid startup data exits with a generic
error so credentials never enter logs.

## Rotate a credential

Read the new database URL without echoing it; the value stays out of shell
history. The CLI rotates it and waits for every active authorized runtime to
attest that the current revision was applied:

```sh
printf 'New database URL: ' >&2
read -r -s DATABASE_URL
echo "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app <slug> --from-stdin --wait-for-ack
unset DATABASE_URL
```

Use `--restart --wait-for-ack` if you want to replace the process rather than
reload it. To edit the local scaffold, run `gregale init --template
secret-reload-node --path secret-reload-node` and redeploy with
`gregale deploy --name <slug>`. See the [secret delivery and reload
guide](https://gregale.dev/docs/secrets) for the revision, stale-ACK, and
retry contract.

## Run the helper tests

```sh
npm test
```

The tests cover snapshot consistency, bootstrap delay and timeout, shutdown,
startup/reload serialization, stale revisions, transient ACK retries, and the
rule that ACK payloads never include credential values or error text. Starter
process tests use a controlled database adapter and real local HTTP/ACK servers.

The helper includes the platform's `FAAS_SECRETS_RELOAD_GENERATION` in every ACK.
It captures that process's identity before transport retries and rejects malformed
advertised identities without logging their contents. A process restart registers
a fresh generation even at the same secret version, invalidating the previous
process's live ACK. Late ACKs receive `409`; the helper cannot borrow a replacement
process's generation. Legacy guests without the variable can send version-only
ACKs, but strict binding promotion reports their generation coverage as unknown.
Upgrade vmmd and apply the ADR-508 migration before upgrading guests, then redeploy
apps using this helper.
