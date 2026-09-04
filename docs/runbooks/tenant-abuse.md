# FaasTenantAbuse

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`,
the `FaasTenantAbuse` alert block (issue #300, ADR-041).

Metric: `apid_top_tenant_rps{account_id}` — a 5s-sampled presentation
view over `apid_request_total{account_id, route, code}`. Cardinality
is bounded at the top-1000 by 24h request count (pkg/wire/topn.go),
with the remainder collapsed to `account_id="other"`.

Severity: `warn` (abuse, not outage). The `family` label is
`tenant_abuse` so the existing `family`-based inhibition /
silencing rules in `alertmanager.yml.j2:106-110` compose with this
alert. Routing follows the standard `severity=warn → faas-warn`
path; the `account_id` rides as a drill-down label on the alert
payload (acceptance #3 from issue #300).

## Symptom

The alert fires when a single customer's 5s rps on `apid`
exceeds 500 for the configured `for:` window:

| Alert | Trigger | For |
|---|---|---|
| `FaasTenantAbuse` | `topk(20, max(apid_top_tenant_rps) by (account_id)) > 500` | 10m |

The `topk(20, ...)` aggregation preserves the `account_id` label so
Alertmanager routes per-customer; the page receiver groups by
`[alertname, component]` so 20 simultaneous offenders collapse into
one page with per-account drill-down.

The `account_id="other"` row is excluded via the `!=` matcher —
the overflow bucket represents demoted customers (not abusive
ones), and a non-zero `other` reading means the top-1000 cap is
saturated, which is a separate fleet-level signal (covered by
the §12 dashboards, not by this alert).

A real customer id surfaces under its own label, never under
`account_id="other"` — even after the id leaves the top-N it
transitions to a gauge value of 0, not to the overflow bucket.

## Verify

The dashboard `faas-top-tenants` (deploy/grafana/top-tenants.json,
uid `faas-top-tenants`) has four panels:

> **First-scrape-after-restart is approximate.** The sampler
> goroutine emits the gauge once per 5s tick and computes rps as
> (currentCount - prevCount) / interval. On the very first tick
> after a daemon restart, `prevCount` is empty so the gauge
> surfaces the cumulative request count divided by the 5s sample
> interval. The value converges to a true 5s delta on the second
> tick (5s later). If you're investigating "why is this customer's
> rps so high right after a deploy?", wait 10s and re-check.

- Panel 1: "Top-10 noisy customers (5m, apid)" — `topk(10, apid_top_tenant_rps{account_id!="other"})`.
- Panel 2: "Top-10 noisy apps (5m, gateway)" — mirror for `gateway_top_tenant_rps`.
- Panel 3: "Customer share of fleet traffic (5m, apid)" — top-10 by traffic share.
- Panel 4: "Other bucket growth (apid, 5m)" — flags overflow saturation.

For ad-hoc Prometheus API queries:

```bash
# Top-20 by current 5s rps (the rule's source expression)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(20,apid_top_tenant_rps)' | jq .

# Single-customer drill-down — replace $ACCOUNT_ID with the offending uuid
ACCOUNT_ID=<uuid>
curl -fsS --data-urlencode "query=sum by (route) (rate(apid_request_total{account_id=\"${ACCOUNT_ID}\"}[5m]))" \
  'http://127.0.0.1:9095/api/v1/query' | jq .

# Cross-check gateway-side rps for the same customer
curl -fsS --data-urlencode "query=sum by (account_id) (gateway_top_tenant_rps{account_id=\"${ACCOUNT_ID}\"})" \
  'http://127.0.0.1:9095/api/v1/query' | jq .

# Is the top-1000 cap saturated?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=apid_top_tenant_rps{account_id="other"}' | jq .
```

## Check

```bash
# Recent daemon logs for the offending account_id (cross-reference
# against the apid log stream for tenant attribution)
journalctl -u apid --since '-15m' --no-pager | grep -i "<ACCOUNT_ID>"

# Cross-check the per-customer failure counter — a spike paired with
# rising failures = a customer in trouble, not abusive (different
# runbook: see FaasApidAuditWriteFailures.md)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=apid_request_failures_total' | jq .

# Cross-check the daemon-side per-customer rate via the underlying
# counter (the gauge is a presentation view; the counter is truth)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(5,sum by (account_id)(rate(apid_request_total{account_id!="__other__"}[5m])))' | jq .
```

A spike paired with low `apid_request_failures_total` is a
successful burst (legitimate traffic surge from the customer
side, e.g. a marketing campaign). A spike paired with rising
`apid_request_failures_total` is a customer in trouble — see
the per-account failure runbook for triage.

A spike that does NOT show up on the gateway gauge but only on
apid is a control-plane load (cron sweep, build orchestrator,
deploy engine) rather than customer traffic — coordinate with
the platform team before rate-limiting.

## Silence

```bash
# Silence a specific customer (e.g. expected traffic spike during
# a marketing campaign). Replace $ACCOUNT_ID with the offending uuid.
ACCOUNT_ID=<uuid>
amtool silence add \
  --matchers='alertname="FaasTenantAbuse",account_id="'"${ACCOUNT_ID}"'"' \
  --duration=15m \
  --comment='planned customer spike; rate-limit ahead'

# Silence every FaasTenantAbuse row during an active incident
# bridge (use sparingly; this hides ALL noisy customers)
amtool silence add \
  --matchers='alertname="FaasTenantAbuse"' \
  --duration=30m \
  --comment='incident bridge open; investigating'
```

## Recover

Three-step cascade, ordered from least to most disruptive:

1. **Rate-limit.** `POST /v1/accounts/<account_id>/ratelimit` with a
   per-account token-bucket ceiling (default 100 rps; tweak per
   customer expectation). This is the §11 egress-deny precedent
   applied at the API edge — the customer keeps their session and
   can still authenticate, but the per-second budget is throttled.
   Verify the rate-limit took effect by reading the per-customer
   failure counter and `apid_request_total{code="err"}`; a healthy
   rate-limit shows `429` rows climbing on the offending
   `account_id` while the overall rps drops below 500 within a
   minute. (Note: the per-account rate-limit endpoint exists
   alongside the per-app limiter — the per-account one is a
   spec §11 surface, not the gateway's per-app bucket.)

2. **Notify.** Send a courtesy email to the account's primary
   contact: "Your account is exceeding 500 rps on the FAAS API. We've
   applied a temporary 100 rps rate-limit to keep the platform
   healthy for other customers; please reach out to coordinate a
   higher plan tier or investigate your client's retry policy."
   The mail template lives in `pkg/grace/dunning_templates.go` (or
   a future `pkg/abuse/` package — not in scope for this PR). The
   notification is best-effort: a transient mail failure does NOT
   block the rate-limit, but should be retried within an hour.

3. **Escalate: suspend deployment.** If the rate-limit does NOT
   bring the customer back below 500 rps within 10 minutes (i.e.
   they're flooding through the limiter, suggesting an
   intentionally-bad client), suspend their deployments via the
   apid admin endpoint: `POST /v1/admin/accounts/<account_id>/suspend`.
   This flips every app owned by the account to a parked state;
   new requests return 503 until the operator unsuspends via the
   corresponding `unsuspend` endpoint. **This is a load-bearing
   step** — verify with the customer success team before
   invoking, and log every suspend action in the apid audit
   table (the same seam used by `pkg/grace` for GDPR deletion).

Recovery verification:

```bash
# After rate-limit: customer's rps should drop below 500 within 1m
curl -fsS --data-urlencode "query=apid_top_tenant_rps{account_id=\"${ACCOUNT_ID}\"}" \
  'http://127.0.0.1:9095/api/v1/query' | jq .

# After notify: customer should self-correct within 30m; if not,
# proceed to step 3.
# After suspend: apid_request_total should drop to ~0 for that
# account within 1m (their apps are parked).
curl -fsS --data-urlencode "query=sum(rate(apid_request_total{account_id=\"${ACCOUNT_ID}\"}[5m]))" \
  'http://127.0.0.1:9095/api/v1/query' | jq .
```

A sustained recovery (no further `FaasTenantAbuse` fires for
24h) closes the incident; the silence expires on its own and
the gauge surfaces the customer at their normal rps position.
