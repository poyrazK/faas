# cron-worker

An exported Node.js handler that runs as a function — invoked by
[Upstash QStash](https://upstash.com/docs/qstash) on a schedule (or
on demand). Unlike the app templates (Express on `:8080`), this
template exports a single `async function handler(event, ctx)` that
the platform's node22 runner invokes directly.

**This is a scaffold, not a production cron system.** It demonstrates
the QStash signature verification + Upstash Redis counter pattern, so
the customer can `gregale logs <slug>` and see structured progress across
cold boots.

## Managed services

- **Upstash QStash** — schedules + signs every invocation with
  HMAC-SHA256 over the raw body. The handler verifies the signature
  before doing anything else.
- **Upstash Redis** — durable counter keyed per invocation, so the
  customer can answer "how many times has my cron fired?" with a
  single Redis GET.

## First deploy with secrets

Create the file outside this directory, restrict it to your user, and
deploy. Gregale creates the app, seals the credentials, and only then
starts the runtime:

```sh
cat > ../cron-worker.secrets <<'EOF'
QSTASH_TOKEN=<qstash-signing-key>
UPSTASH_REDIS_REST_URL=https://<instance>.upstash.io
UPSTASH_REDIS_REST_TOKEN=<redis-rest-token>
EOF
chmod 600 ../cron-worker.secrets
gregale deploy --secrets-file ../cron-worker.secrets
```

## Rotate or update secrets

```sh
gregale secrets set --app <slug> QSTASH_TOKEN=<qstash-signing-key> \
  UPSTASH_REDIS_REST_URL=https://<instance>.upstash.io \
  UPSTASH_REDIS_REST_TOKEN=<redis-rest-token>
```

If any of `QSTASH_TOKEN`, `UPSTASH_REDIS_REST_URL`,
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

In the QStash console (or via curl), publish to your function URL:

```sh
curl -X POST https://qstash.upstash.io/v2/publish/<slug>.gregale.dev \
  -H "Authorization: Bearer $QSTASH_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"task":"tick"}'
```

The handler responds 200 with `{ ok, invocation_id, count, received }`.
The `count` is the per-invocation Redis counter, incremented every
time the handler fires.

## Re-deploy after edits

Edit `handler.js`, then `gregale deploy` from this directory.
