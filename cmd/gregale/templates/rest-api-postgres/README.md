# rest-api-postgres

A minimal port-8080 Node.js REST API (`GET /notes`, `POST /notes`)
backed by a managed PostgreSQL provider. **This is a scaffold, not a
production API** — it auto-creates a `notes` table on first boot so
the customer can `curl` it without running migrations, and uses a
small connection pool sized for cold-boot wake latency.

## Managed service

Plug in any managed PostgreSQL:

- **Neon** — serverless, free tier. URL looks like
  `postgres://user:pass@ep-xxx.us-east-1.aws.neon.tech/db?sslmode=require`.
- **Supabase** — `postgres://postgres:pass@db.xxx.supabase.co:5432/postgres?sslmode=require`.
- **PlanetScale (Postgres beta)** — provider-supplied URL.
- **CockroachDB Cloud** — `postgres://user:pass@free-tier.gcp-us-central1.cockroachlabs.cloud:26257/db?sslmode=require`.

**Don't** run `postgres:16` as your base image — the platform's
deny-list rejects it at accept time (Wave 0 PR-A).

## First deploy with secrets

Create the file outside this directory, restrict it to your user, and
deploy. Gregale creates the app, seals the database URL, and only then
starts the runtime:

```sh
cat > ../rest-api-postgres.secrets <<'EOF'
DATABASE_URL=postgres://user:pass@host:port/db?sslmode=require
EOF
chmod 600 ../rest-api-postgres.secrets
gregale deploy --secrets-file ../rest-api-postgres.secrets
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> DATABASE_URL='postgres://user:pass@host:port/db?sslmode=require'
```

If `DATABASE_URL` is missing, the handler exits at startup with the
exact `gregale secrets set` command.

> **SSL — keep `?sslmode=require` (or `verify-full`) on the URL.**
> The pool reads the query string and sets `rejectUnauthorized: true`
> only when one of those is present; without it, the pool silently
> falls back to an unencrypted connection. Every managed Postgres
> provider above ships TLS by default — keep the suffix on your URL.

## Deploy

From this directory:

```sh
gregale deploy
```

## Try it

```sh
curl https://<slug>.gregale.dev/healthz       # → {"ok":true,"db":"ok"}
curl -X POST -H 'content-type: application/json' \
     -d '{"body":"hello from gregale"}' \
     https://<slug>.gregale.dev/notes          # → {"ok":true,"note":{...}}
curl https://<slug>.gregale.dev/notes          # → {"ok":true,"notes":[{...}]}
```

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
