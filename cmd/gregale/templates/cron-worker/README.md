# cron-worker

A Node.js function invoked by
[Upstash QStash](https://upstash.com/docs/qstash) on a schedule (or
on demand). Unlike the app templates (Express on `:8080`), this
template exports the Fetch API form (`export default { fetch }`) that
the platform's node22 runner invokes directly — that form receives the
exact request bytes, which the QStash signature covers.

**This is a scaffold, not a production cron system.** It demonstrates
the QStash signature verification + Upstash Redis counter pattern, so
the customer can `gregale logs <slug>` and see structured progress across
cold boots.

## Managed services

- **Upstash QStash** — schedules and signs every delivery with a JWT
  in `Upstash-Signature` (HS256, keyed by your QStash signing keys,
  carrying a SHA-256 of the raw body). The handler verifies it —
  accepting either the current or the next signing key, so key
  rotation doesn't break deliveries — and answers 401 otherwise.
- **Upstash Redis** — durable counter keyed per invocation, so the
  customer can answer "how many times has my cron fired?" with a
  single Redis GET.

## First deploy with secrets

Create the file outside this directory, restrict it to your user, and
deploy. Gregale creates the app, seals the credentials, and only then
starts the runtime:

```sh
cat > ../cron-worker.secrets <<'EOF'
QSTASH_CURRENT_SIGNING_KEY=<qstash-current-signing-key>
QSTASH_NEXT_SIGNING_KEY=<qstash-next-signing-key>
UPSTASH_REDIS_REST_URL=https://<instance>.upstash.io
UPSTASH_REDIS_REST_TOKEN=<redis-rest-token>
EOF
chmod 600 ../cron-worker.secrets
gregale deploy --secrets-file ../cron-worker.secrets
```

## Reserve then configure separately

If you prefer to set secrets through the app API, reserve the app first:

```sh
gregale deploy --create-only --template cron-worker --name <slug>
gregale secrets set --app <slug> QSTASH_CURRENT_SIGNING_KEY=<current-signing-key> QSTASH_NEXT_SIGNING_KEY=<next-signing-key> UPSTASH_REDIS_REST_URL=https://<instance>.upstash.io UPSTASH_REDIS_REST_TOKEN=<redis-rest-token>
cd <this-directory> && gregale deploy
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> QSTASH_CURRENT_SIGNING_KEY=<current-signing-key> \
  QSTASH_NEXT_SIGNING_KEY=<next-signing-key> \
  UPSTASH_REDIS_REST_URL=https://<instance>.upstash.io \
  UPSTASH_REDIS_REST_TOKEN=<redis-rest-token>
```

If any of `QSTASH_CURRENT_SIGNING_KEY`, `QSTASH_NEXT_SIGNING_KEY`, `UPSTASH_REDIS_REST_URL`,
`UPSTASH_REDIS_REST_TOKEN` are missing, the handler throws on the
first invocation with the exact `gregale secrets set` command — the
runtime surfaces it as a 500 to QStash (which logs it) and the
customer sees the actionable hint in `gregale logs <slug>`.

## Deploy

From this directory:

```sh
gregale deploy
```

The CLI forces `--runtime node22 --handler handler.handler` for
function templates (commands2.go:298), so no extra flags are needed.

## Wire QStash

Copy both signing keys from the QStash console ("Signing Keys"). Then,
in the console or via curl with your QStash API token (`QSTASH_TOKEN`
here authenticates you to QStash; the function never needs it), publish
to your function URL:

```sh
curl -X POST https://qstash.upstash.io/v2/publish/<slug>.gregale.dev \
  -H "Authorization: Bearer $QSTASH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"task":"tick"}'
```

The handler responds 200 with `{ ok, invocation_id, count }`.
The `count` is the per-invocation Redis counter, incremented every
time the handler fires. Logs include the payload byte count but never the
payload itself. Select and log individual non-secret fields only when the job
requires them.

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
