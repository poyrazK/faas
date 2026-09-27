# secret-reload-node

A Node.js + PostgreSQL reference app for in-process secret rotation. It handles
`SIGHUP`, reads Gregale's version-fenced secret files, creates and tests a new
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

The Dockerfile sets the OCI label that opts the app into `SIGHUP` delivery.
Only secrets authorized for this app are projected into its runtime files.
The app validates the database URL without printing it, tests a candidate
connection, then swaps pools. `/healthz` returns a generic error if the active
database is unavailable.

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

The tests cover snapshot consistency, stale revisions, transient ACK retries,
and the rule that ACK payloads never include credential values or error text.
