# ADR-20261115090000: Off-platform status page

- **Status:** accepted
- **Date:** 2026-11-15
- **Milestone:** Public beta reliability
- **Amends:** [ADR-130](130-status-page-wiring.md)

## Context

The public status data (`GET /v1/status`), the incident store, the evaluator's
Prometheus and the website's status page all depend on Gregale. The website is
hosted elsewhere, but it fetches `/v1/status` from apid, so in a platform
outage it loads and then reports "Status source unavailable". Operators cannot
post an incident either, because `gregalectl status incident create` goes
through apid. The status page fails exactly when customers need it.

## Decision

Serve a second status page from Cloudflare that never calls Gregale:

1. **Mirror.** When `FAAS_STATUS_MIRROR_R2_*` is set, every apid replica
   writes the `/v1/status` document (the same value, built by one function)
   to `v1/status.json` in the R2 bucket `gregale-status` every minute, wrapped
   with `published_at`. Writes are unconditional; the newest wins.
2. **Outside probe.** A Cloudflare Worker cron (`deploy/cloudflare/status-worker`)
   checks configured Gregale endpoints every minute and writes `v1/probe.json`
   with per-check consecutive failures.
3. **Offline notices.** `gregalectl status offline post|update|resolve|list`
   writes incidents to `v1/notices.json` with conditional (ETag) writes,
   validated by the same rules as apid incidents. They appear only on the
   off-platform page; once apid recovers the operator records the incident
   through the normal path.
4. **Page.** `status.gregale.dev` is static Worker assets. It reads the three
   files from the bucket's custom domain `status-data.gregale.dev` and
   combines them in `view.mjs`:
   - a platform report older than 3 minutes is stale; components are then
     unknown unless a probe or notice says otherwise;
   - stale report + outside checks failing twice in a row = outage banner;
   - stale report + passing checks = "telemetry delayed", status unknown;
   - outside failures and open notices can only make a fresh report worse.

Nothing on the viewer path runs Worker code: assets and cached R2 reads are
free and not counted toward the Workers free plan's 100k requests/day, so an
outage traffic spike cannot exhaust it. The whole system fits the free plan
(~86k of 1M monthly R2 writes per apid replica plus probe; 1,440 cron runs a day).

`FaasStatusMirrorPushFailing` warns when an apid's pushes fail for 15 minutes.

## Consequences

- The status page survives Gregale outages and says so without an operator.
- It shares Cloudflare with the platform's edge. A Cloudflare outage takes both
  down; accepted.
- One probe location only distinguishes up from down, not regional problems.
- Offline notices are not in `/v1/status` history; the runbook makes the
  operator re-post them through apid after recovery. Importing them
  automatically is a possible follow-up.
- R2 tokens are bucket-scoped, not key-scoped: apid's token could overwrite
  notices. Operators and apid use separate tokens so either can be revoked.
- Turning the mirror on is an operator step (bucket, custom domain, CORS, cache
  rule, tokens, Worker deploy); see `deploy/cloudflare/status-worker/README.md`.

## Alternatives

- **Hosted status page service.** Fastest and fully independent, but loses the
  component model, coverage and uptime history, or needs a second integration.
- **Worker serving the data.** Simpler routing, but every viewer request would
  run the script and count toward the free plan's daily request limit, which
  an outage spike could exhaust.
- **Workers KV instead of R2.** The free plan's 1,000 KV writes a day are fewer
  than one write a minute from apid alone.
