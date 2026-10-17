# secret-reload-node

A Node.js + PostgreSQL reference app for in-process secret rotation. It uses
Gregale's canonical `SecretReloadCoordinator` and `PostgresPoolBinding`, vendored
with their complete import dependencies so the generated app is self-contained.
`SIGHUP` reads one atomic secret snapshot, validates a fresh pool and acknowledges
the exact revision only after adoption. Failed adoption or revocation disables
new queries. A transaction already holding a lease may finish before its pool
is closed. SQL operations are never automatically replayed.

## Configure and deploy

Create a secrets file outside this directory and restrict its permissions:

```sh
cat > ../secret-reload-node.secrets <<'VALUES'
DATABASE_URL=postgres://user:password@host:5432/database?sslmode=require
VALUES
chmod 600 ../secret-reload-node.secrets
gregale deploy --template secret-reload-node --name <slug> \
  --secrets-file ../secret-reload-node.secrets
```

The included `gregale.yaml` declares the shared adapter's snapshot hooks.
`/internal/before-checkpoint` drains every SQL session before a capture, and
`/internal/after-restore` builds and validates a fresh pool from the latest
snapshot before readiness. Both answer only loopback requests carrying their
exact marker header. Declaring the checkpoint hook turns off warm captures.

The Dockerfile opts into `SIGHUP` and required reload readiness. The coordinator
installs its handler before writing the readiness marker or awaiting SQL. HTTP
serving begins after validation and an accepted applied ACK. `/healthz` runs a
read-only transaction and returns a generic 503 on database failure.

This starter requires the complete modern guest contract:
`FAAS_SECRETS_SNAPSHOT_FILE`, `FAAS_SECRETS_RELOAD_READY_FILE`,
`FAAS_SECRETS_RELOAD_ACK_ENDPOINT`, and `FAAS_SECRETS_RELOAD_GENERATION`.
Missing/partial contracts, malformed snapshots and legacy separate-file delivery
fail closed. Upgrade the guest and redeploy older applications to adopt it. It
never borrows credentials from process environment when a projection fails.

The initial versioned snapshot has a 30-second availability budget, configurable
through `startupTimeoutMs` in `createDatabase`. A valid envelope with an empty
revision is pending publication. Invalid data fails immediately. Stale ACKs
trigger a fresh snapshot read; transport retries preserve the revision and
execution generation. Startup and signal reloads share one serialized queue.

The URI must supply user, password, host, port and database, with
`sslmode=require` or `verify-full`. Certificate and hostname verification remain
enabled; private CAs can use `NODE_EXTRA_CA_CERTS`. Alternate URL TLS overrides
and certificate/key paths are rejected. Errors and ACK bodies contain no driver
diagnostics or credentials. Requests have five-second SQL deadlines, bounded
admission and transaction-local statement timeouts. Add application routes,
migrations and idempotency rules according to your workload.

Each query retains its pool lease through COMMIT or connection destruction. A
late connection acquired after cancellation is destroyed before releasing its
lease. SIGTERM/SIGINT stop acquisition, cancel active work and wait up to five
seconds for retirement. This shutdown policy may cancel transactions; credential
rotation alone lets admitted transactions finish. Leases do not extend database
credential validity or prevent server-side connection termination.

## Rotate a credential

Read the replacement without echoing it, then wait for authorized runtimes to
acknowledge adoption:

```sh
printf 'New database URL: ' >&2
read -r -s DATABASE_URL
echo "DATABASE_URL=$DATABASE_URL" | gregale secrets rotate --app <slug> --from-stdin --wait-for-ack
unset DATABASE_URL
```

Use `--restart --wait-for-ack` to replace the process. Every ACK includes the
captured process generation; an old process cannot borrow its replacement's
identity. Applied ACKs attest adoption, and do not replace platform binding
verification or prove ongoing database health.

## Tests and runtime updates

```sh
npm test
```

Tests exercise the actual handler with controlled SQL and real local HTTP/ACK
servers: bootstrap, signal serialization, ACK ordering, cancellation, late
candidate cleanup and invalid startup. The shared canonical coordinator/pool
suites cover the protocol and lease state machine. The PostgreSQL REST starter's
materialized TLS/SCRAM suite exercises the shared database adapter with real
credential rotation, transaction draining, revocation and restore races.

In the Gregale repository, regenerate vendored runtime files and this starter's
shared `database.js` with `npm --prefix sdk/node run build` followed by
`npm --prefix sdk/node run starter:sync`. The runtime drift gate checks all three
binding starters. Modify the adapter in `rest-api-postgres/database.js`, its
canonical source, before synchronizing. Customer projects can customize their
own generated copy.
