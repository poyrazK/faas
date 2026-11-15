# status.gregale.dev — off-platform status page

Decision: [ADR-20261115090000](../../../docs/adr/20261115090000-off-platform-status-page.md).

`/v1/status` and its page run on Gregale, so when Gregale is down the page
that should say so is down too. This directory is the copy that is not:

| Piece | Where it runs | What it does |
|---|---|---|
| `public/index.html`, `public/view.mjs` | Workers static assets on `status.gregale.dev` | The page. Reads the three JSON files below and decides what to show. |
| `v1/status.json` | R2 bucket `gregale-status`, written by apid every minute | The `/v1/status` document plus `published_at`. |
| `v1/probe.json` | Same bucket, written by `worker.mjs` cron every minute | Outside checks of Gregale endpoints, with consecutive-failure counts. |
| `v1/notices.json` | Same bucket, written by `gregalectl status offline` | Incidents posted while apid is down. |

The bucket is read publicly through its custom domain
`status-data.gregale.dev`, behind Cloudflare's cache.

## What the page shows

- apid's report is under 3 minutes old: it is shown as-is. Outside checks
  failing twice in a row and open offline notices can only make it worse.
- apid's report is older and outside checks are failing: **major outage**
  (or partial, if only some checks fail), with a banner saying Gregale is not
  reachable from outside. Components without evidence show **unknown**.
- apid's report is older and outside checks pass: **status unknown**, with a
  "telemetry delayed" banner. The page never keeps showing the last green
  state.
- Nothing fresh at all: **status unknown**, "monitoring unavailable".

The rules live in `public/view.mjs` and are tested by `view.test.mjs`.

## Free plan budget

Everything fits Cloudflare's free plan as long as viewers never run the
Worker script:

- Page requests are static assets: free and not counted as Worker requests.
  Unknown paths get `404.html` the same way.
- Data requests go to the R2 custom domain with `Cache-Control: max-age=15`
  (10 s for notices), so outage traffic spikes hit Cloudflare's cache, not R2.
- Writes: apid ~43k/month per replica, probe ~43k/month, against 1M free
  Class A operations. The cron is 1,440 Worker runs/day against 100k.

Workers Paid ($5/month) removes the request and cron limits if ever needed.

## First deploy

1. **Bucket.** Create `gregale-status` (Standard storage, which has the free
   tier). Connect the custom domain `status-data.gregale.dev` (R2 → bucket →
   Settings → Custom Domains). Leave the `r2.dev` URL disabled.
2. **CORS.** The page on `status.gregale.dev` reads from
   `status-data.gregale.dev`:

   ```bash
   npx wrangler r2 bucket cors set gregale-status --file r2-cors.json
   ```

3. **Cache rule.** Cloudflare does not cache `.json` by default. Add a Cache
   Rule: hostname equals `status-data.gregale.dev` → Eligible for cache, Edge
   TTL "use cache-control header if present". Without it every page poll is
   an R2 read.
4. **Tokens.** Create two R2 API tokens with *Object Read & Write* on
   `gregale-status` only:
   - apid: put it in `/etc/faas/sealed.env` as `FAAS_STATUS_MIRROR_R2_ACCESS_KEY`
     and `FAAS_STATUS_MIRROR_R2_SECRET_KEY`, and set
     `FAAS_STATUS_MIRROR_R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com`
     and `FAAS_STATUS_MIRROR_R2_BUCKET=gregale-status` for apid. Restart apid;
     `apid_status_mirror_publishes_total{outcome="ok"}` should start rising.
   - operators: keep it in the password manager for `gregalectl status
     offline` (see `docs/runbooks/StatusPageDegraded.md`).
5. **Worker.**

   ```bash
   cd deploy/cloudflare/status-worker
   cp wrangler.toml.example wrangler.toml   # local only, not committed
   npx wrangler deploy
   ```

   `gregale.dev`'s wildcard DNS record points at the platform; the Worker's
   custom domain creates a specific `status.gregale.dev` record, which takes
   precedence.
6. **Check.** Open `https://status.gregale.dev`. Within two minutes it should
   show a fresh platform report and a passing API check. Then link to it from
   the website's status page and from incident emails.

## Rehearse an outage

Revoke apid's R2 token (or stop the mirror env) and wait three minutes: the
page should switch to "telemetry delayed". Point a probe target at a URL that
returns 503: after two minutes the page should show an outage banner. Post and
resolve a notice with `gregalectl status offline`.

## Tests

```bash
node --test deploy/cloudflare/status-worker/worker.test.mjs deploy/cloudflare/status-worker/view.test.mjs
```
