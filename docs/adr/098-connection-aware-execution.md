# ADR-098 — Connection-aware execution (data-aware placement)

- **Status:** proposed
- **Date:** 2026-08-12
- **Renumbered:** originally drafted as ADR-095. Renumbered 095 → 098
  on 2026-08-13 after PR #851 (PR-preview environments, MERGED
  2026-08-11) claimed 095 on main and PR #854 (wake single-flight,
  OPEN) also claimed 095. Mirrors the renumber dance ADR-097 used to
  dodge ADR-093 + ADR-096. The internal ADR doc filename + spec
  section anchor (`§9.A`) + PR-cluster-outline file all carry 098;
  the original 095 references live only in this renumber note.
- **Closes:** the §9 Connection-aware execution gap surfaced by the
  2026-08-12 customer-trust review. The platform is **stateless by
  contract** (CLAUDE.md, ADR-046, `docs/storage.md`); customers bring
  their own datastore / cache / object store / APIs. Today the
  scheduler has **no awareness** of those upstreams — `ChoosePlacement`
  (`pkg/sched/placement.go:96`) consumes only RAM / vCPU headroom +
  sticky-warm `PreferredNodeID` + static `compute_nodes.region` /
  `zone`. As a result, when a Postgres-backed Node app on Neon
  `us-east-1` is woken on an `eu-central-1` node, every request pays a
  cross-ocean DB round-trip that no customer ever asked for. The
  absence is silent — there is no telemetry for it.

- **Depends on:** ADR-090 (env-write plumbing — `cmd/apid/handlers_env.go`
  + `app_envs.scope`), ADR-091 (per-deployment scope precedent —
  `deployments.scope`), ADR-087 (sliding-window polling precedent —
  `pkg/sched/pressure_aggregator.go`), ADR-046 (stateless contract —
  the BYO-storage guard), ADR-009 (per-app snapshot reuse — the
  affinity hint this ADR biases toward).
- **Sits alongside:** ADR-091 edge-rules (kind=geo, migration 00218).
  That ADR routes an *incoming* request by client IP / region. This
  ADR routes an *outbound* dependency — different problem, similar
  shape.

## Context

Gregale's customers run their stateful services elsewhere — Neon,
Upstash, PlanetScale, RDS, S3, Cloudflare R2, their own APIs. The
platform's job is to wake their compute close to where the data is
and run the request fast. Today the wake path does not know what
"close to where the data is" means for any given app, because nothing
in the deploy-time surface records the upstreams an app talks to.

The 2026-08-12 customer-trust review surfaced this as the
**statelessness asymmetry**: customers accepted that Gregale owns no
storage, but they also expect Gregale to place their compute sensibly
relative to the storage they bring. The only placement signal we
have is the warm-affinity hint (`pkg/sched/warmaffinity.go:55`), which
captures "which node last woke this app" — a strong bias for snapshot
reuse (ADR-009) but blind to where the customer's data lives.

The minimum useful feature set, locked in v1 scope conversations on
2026-08-12:

- **Inferred-from-env baseline + explicit per-app override.** The
  customer does not have to opt in. When they set
  `DATABASE_URL=postgres://...ep-name.us-east-2.aws.neon.tech/...` on
  an `apid` PUT, the platform records the upstream host. When they
  want to override the inference (say, a static-IP Neon endpoint that
  suffix-matching can't place), `POST /v1/apps/{slug}/upstreams`
  accepts an explicit `{kind, host, declared_region, scope}` row.
- **Meterd-owned RTT measurement.** A 30s × 5 min sliding-window probe
  loop, owned by `meterd` next to its existing sample/stripe/dunning
  /residency/alerts timers (`pkg/meter/loop.go`). TCP-connect +
  TLS-handshake timing per `(host, region)` pair, with a
  `UpstreamProbeMaxConcurrent` worker pool cap.
- **Schedd wake-time bias.** A new `Request.PreferredRegion` field,
  fed by `pkg/sched/upstream_affinity.go` (a TTL'd in-memory map
  mirroring `WarmAffinity`). `ChoosePlacement` adds one branch to
  `betterCandidate`. Fail-open to legacy if no data — the
  single-node install is not regressed.
- **Customer-visible surface.** `GET /v1/apps/{slug}/upstreams`
  returns the inferred + explicit rows + last observed RTT. This is
  both a trust surface (a customer can see what we inferred from their
  env) and a debug surface for §12 dashboards.

## Decisions

### D1. Capture: inferred-from-env + explicit-override

`apid` classifies env values into one of fourteen kinds —
`postgres | redis | mongo | cassandra | clickhouse | elasticsearch |
opensearch | rabbitmq | kafka | nats | minio | memcached | etcd | s3 |
https_api` — by matching key names against a fixed regex set:

```
^(DATABASE|POSTGRES|REDIS|MONGO|CLICKHOUSE|CASSANDRA|ELASTIC|OPENSEARCH|RABBITMQ|KAFKA|NATS|MEMCACHED|ETCD)_(URL|ENDPOINT|URI|DSN)$
^S3_(BUCKET|ENDPOINT|REGION)$
^.*_API_URL$
```

The classifier is a pure function in `pkg/data/extract.go`:
`func ClassifyEnvValue(key, value string) []DataUpstreamDraft`.
Each entry is `(kind, host, port, raw_redacted_host_hash)`. Host
extraction uses `net/url.Parse` (stdlib only — no third-party
inference). Values that fail to parse are dropped silently (§11
forbids logging the raw value; the key and value-hash are still
counted).

Two parallel §11-compliance rules:

- **Plaintext values never leave the handler.** The capture side
  accepts the PUT body in TLS, parses it in-process, and writes only
  the host + port + region to PG. The raw DSN never reaches a log
  line, an audit emit, or a Prometheus label.
- **Hosts are stored plaintext for inspection** (the customer needs
  to see what we inferred), with a `host_redacted_hash` column
  (`sha256(salt + host)`) used in Prom labels so the metric surface
  does not become a host-leak.

The classifier is parallel to `pkg/reposcan/denylist.go::datastoreDenylist`
(image→hint table). Extending the static provider→region map lives in
`pkg/data/infer.go`; an unknown host returns `"", 0.0` (no region).
PR-B writes the resulting rows via `Store.InsertDataUpstream` with
`source='inferred'` and `declared_region` from the inference table
(empty when unknown). The capture path is gated on
`FAAS_DATA_PLACEMENT=1` AND `Limits.DataPlacementHintsPerApp > 0` for
the customer plan — Free apps are never inferred (D5).

Explicit overrides arrive through `POST /v1/apps/{slug}/upstreams`
with body `{kind, host, declared_region, scope}`. The override wins
when `source='explicit'`. The GET endpoint returns the merged set.

### D2. Probe ownership: meterd background loop

The probe loop lives in `pkg/meter/upstream_probe.go`, wired by
`cmd/meterd/main.go` next to the existing six timers in `Loop.runTicks`
(`pkg/meter/loop.go:166` family). A new `runTicks("upstream_probe", ...)`
selects on the existing `Ctx.Done()` and ticks every
`UpstreamProbeSampleIntervalSeconds` (default 30s, env-overridable via
`FAAS_UPSTREAM_PROBE_INTERVAL`).

Per tick:

1. Scan `data_upstreams` rows + `compute_nodes` rows to produce the
   distinct `(host, region)` pairs to probe. **Single-node deploys
   produce exactly one region.** Multi-node produces one pair per
   distinct (host, region) cross-product — the cross-product is
   intentional, because the point of the feature is to see the
   cross-region delta.
2. Hand the pairs to a bounded worker pool (capacity
   `api.UpstreamProbeMaxConcurrent = 64`). Each worker performs a
   `crypto/tls.Dial` to `host:port` with `context.WithTimeout` of 3s,
   records the wall time from `DialStarted` to `TLSHandshakeComplete`.
3. Insert one row into `data_upstream_probes` per probe outcome:
   `(host, region, kind, sampled_at, rtt_ms, ok)`. The table is
   monthly-partitioned per the migration's `pg_partman` style.
4. Update the `pkg/wire/metrics.go` Gauge
   `data_upstream_rtt_ms{kind, host, region}` to the median of the
   last 10 samples (5 min sliding window).

Two SLOs land in `pkg/promqlrules/data_placement.yaml`:
`data_upstream_rtt_ms:kube_quantile{quantile="0.95"} < 50` (info),
alert at `>200`. Documented in `docs/faas_implementation_spec.md` §12.

**Deploy wiring.** The file is staged onto the box by
`deploy/ansible/roles/prometheus/tasks/main.yml` (a sibling `copy:`
task next to the existing faas.rules.yml + pg_backup.rules.yml drops)
and listed in the Prometheus scrape config's `rule_files:` block at
`deploy/ansible/roles/prometheus/templates/prometheus.yml.j2:17-22`
alongside `/etc/prometheus/faas.rules.yml` and
`/etc/prometheus/pg_backup.rules.yml`. CI lints it via
`promtool check rules` at `.github/workflows/ci.yml` (extended to
cover the sibling in issue #951) and the Go-side
`TestFaasRulesSyntax` (extended to a list of rule files in
issue #951).

### D3. Placement signal: `Request.PreferredRegion`, wake-time bias only

`pkg/sched/placement.go::Request` gains one field:

```go
type Request struct {
    // ... existing fields ...
    PreferredRegion string // ADR-098: populated from upstream_affinity.Score()
}
```

`pkg/sched/upstream_affinity.go::UpstreamAffinity` mirrors
`WarmAffinity` (TTL'd in-process `appID → regionScore` map, populated
via `pg_notify` from the `data_upstream_probed` emit). Pure helper:

```go
func upstreamFit(candidateRegion string, scores map[string]float64) float64 {
    // 0.0–1.0; fail-open to 0.5 when scores is empty.
    // 0.0 when candidateRegion == "" (no candidate region known).
}
```

`ChoosePlacement` adds one branch to `betterCandidate`
(`pkg/sched/placement.go:235`), **between** the vCPU-headroom tie-break
(line 245) and the static region tie-break (line 250):

```go
// ADR-098: tie-break #3 = upstream-region fit, weighted against
// api.UpstreamFitMinDeltaMs (default 5 ms). Behaviour:
nFit := upstreamFit(n.Region, e.appScores[appID])
bestFit := upstreamFit(best.Region, e.appScores[appID])
// Skip when both candidates have zero information (fail-open).
if nFit != bestFit { return nFit > bestFit }
```

Ordering rule (pinned in `pkg/sched/placement_test.go`):
**`headroom > vcpu_headroom > upstream_fit > static_region > static_zone > name`**.
The exact position of upstream-fit (between vCPU-headroom and
static-region) is load-bearing — putting it before the static-region
tie-break means a node in an "unknown" region can still beat a
distant-but-perfectly-named node. **Upstream-fit beats warm-affinity
when the upstream-RTT delta exceeds `UpstreamFitMinDeltaMs`**
(default 5 ms, in `pkg/api/limits.go`). When the delta is below the
threshold, warm-affinity wins — keeps the snapshot+page-cache benefit
for the typical case where the data and the last-warm node are
nearby already.

A9 pressure rebalance (`pkg/sched/pressure_rebalancer.go`, ADR-087)
is **unchanged** — capacity-driven. Only wakes are data-biased;
migrations stay capacity-driven. Future-cluster work (out of scope
below) would extend A9.

### D4. Customer surface: `GET / POST / DELETE /v1/apps/{slug}/upstreams`

```
GET    /v1/apps/{slug}/upstreams
POST   /v1/apps/{slug}/upstreams      {kind, host, port, declared_region, scope}
DELETE /v1/apps/{slug}/upstreams/{id}
```

GET joins `data_upstreams` to the latest `data_upstream_probes` row
(LEFT JOIN, last 5 min) and returns:

```jsonc
{
  "items": [
    {
      "id": "…uuid…",
      "kind": "postgres",
      "host": "ep-cool-name.us-east-2.aws.neon.tech",
      "port": 5432,
      "scope": "default",
      "source": "inferred",          // or "explicit"
      "declared_region": "aws-us-east-2",
      "last_rtt_ms": 12.3,           // latest observation; null when no probe yet
      "last_sample_at": "2026-08-12T18:42:01Z"
    }
  ],
  "quota_max": 10,
  "count": 1,
  "probe_enabled": true             // FAAS_UPSTREAM_PROBE echo (operator UI hint)
}
```

The three routes register in `cmd/apid/server.go` alongside the
existing env routes, owned by `cmd/apid/handlers_upstreams.go` (new
file — does not extend `handlers_env.go`). Auth is identical to env
routes (`env:read` for GET, `env:write` for POST/DELETE).

### D5. Quota + free-tier gating

`pkg/api/limits.go` adds three new fields:

| Field | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| `DataPlacementHintsPerApp` | 0 | 3 | 10 | 50 |
| `UpstreamProbeMaxConcurrent` | 64 (global) | 64 (global) | 64 (global) | 64 (global) |
| `UpstreamFitMinDeltaMs` | 5 (global) | 5 (global) | 5 (global) | 5 (global) |

**Free = 0** — Free apps never get inferred rows. The capture path
short-circuits to "do nothing" before the regex match. This is the
**customer-trust story for the free tier**: the platform doesn't peek
at env values on a free app that hasn't opted into anything. A Free
customer who wants explicit overrides must upgrade; the
`Plan.IsDataPlacementAllowed(plan)` helper makes the upgrade prompt
unmistakable. Hobby+ capture is the default behavior.

The two global fields (`UpstreamProbeMaxConcurrent`,
`UpstreamFitMinDeltaMs`) are `Limits` constants (not per-plan
deltas) — they're operational knobs and do not segment by customer
class.

Quota enforcement in PR-B's handler: count existing
`data_upstreams` rows per `(app_id)`; reject POST / PUT with
`http 402` + `code=data_upstream_quota_exceeded` when
`count >= limit`. Limit = `0` for Free ⇒ POST is unconditionally
rejected with the same code (the upgrade UX is correct).

### D6. No secrets in scope; §11 hardening

Three rules:

1. **DSN values never logged.** Capture-side handler reads the PUT
   body, parses, writes — there is no `slog.*` call that includes
   the value. The `cmd/apid/handlers_env.go::recover` chain that
   logs handler errors is reviewed to redact `value` from the map
   before logging.
2. **Audit emit uses redacted hosts.** The `data_upstream.set` /
   `data_upstream.deleted` audit events carry the host-hash only;
   the plaintext host stays in the row.
3. **Probe-side: no body / payload is sent.** The probe is
   `crypto/tls.Dial`, not `http.Get`. There is no API path to leak
   — the only network bytes are the TCP+TLS handshake. Documented
   in `pkg/meter/upstream_probe.go` header.

### D7. Rollout: every step feature-flagged, defaults OFF

| Flag | Owner | Default | Purpose |
|---|---|---|---|
| `FAAS_DATA_PLACEMENT` | apid | OFF | Gates the capture path (PR-B) |
| `FAAS_UPSTREAM_PROBE` | meterd | OFF | Gates the probe loop (PR-C) |
| `FAAS_UPSTREAM_AFFINITY` | schedd | OFF | Gates the chooser branch (PR-D) |

Defaults are OFF for the v1.10 ship. Manual flip per node for v1.11
once CI is clean for one full month on `main`. This is the
**read-only-first posture**: PR-C ships observable telemetry without
the chooser branch even firing, so we get a month of measured data
before any scheduling behavior changes.

## Files (representative — full inventory is in PR-cluster-outline)

### New

- `migrations/00226_reserve_slot.sql` (fence — this PR)
- `migrations/00226_data_upstreams.sql` (PR-A — real DDL, drops the fence file)
- `pkg/data/infer.go`, `pkg/data/extract.go` (PR-B)
- `pkg/sched/upstream_affinity.go` (PR-D)
- `pkg/meter/upstream_probe.go` (PR-C)
- `cmd/apid/handlers_upstreams.go` (PR-B)
- `cmd/e2e/upstreams_e2e_test.go`, `cmd/e2e/upstream_probe_e2e_test.go`
- `docs/adr/098-pr-cluster-outline.md`
- `pkg/promqlrules/data_placement.yaml` (PR-C)
- `cmd/gregale/commands_inspect.go`,
  `cmd/gregale/commands_inspect_upstreams.go`,
  `cmd/gregale/commands_inspect_upstreams_test.go` (issue #952)

### Modified

- `docs/faas_implementation_spec.md` (§9.A insert before existing §9)
- `pkg/api/limits.go` + `pkg/api/limits_test.go`
- `pkg/state/types.go`, `pkg/state/pgstore.go`, `pkg/state/memstore.go`
- `pkg/sched/placement.go`, `pkg/sched/placement_test.go`,
  `pkg/sched/engine.go`, `cmd/schedd/main.go`
- `pkg/meter/loop.go`, `pkg/meter/health.go`
- `pkg/wire/metrics.go`
- `cmd/apid/server.go`, `cmd/apid/handlers_env.go` (audit payload), `cmd/meterd/main.go`
- `api/openapi.yaml`, `pkg/apid/openapi.yaml`
- `sdk/{go,node,python}`
- `docs/storage.md` (new `data_upstreams` / `data_upstream_probes` schema)

## Consequences

### Positive

- **Stateless story gets a placement story.** Customers bring their
  own data, the platform brings the proximity. The asymmetry closes.
- **Trust surface is visible.** A customer running
  `gregale inspect <slug> --upstreams` (issue #952) can see the
  platform's view of their upstreams — and complain when an inference
  is wrong, instead of having no signal at all. The CLI is read-only
  and lives in `cmd/gregale/commands_inspect.go` (verb dispatcher)
  + `commands_inspect_upstreams.go` (leaf); the §11 invariant is
  enforced end-to-end (wire DTO carries only `host_redacted_hash`
  + `host_last4` (the first 8 hex chars of the redacted hash), the
    CLI renderer references only those two
  fields).
- **Multi-node payoff.** On a fleet with two regions and an app
  whose `DATABASE_URL` points to one of them, the wake picks the
  local-region node. Snapshot reuse keeps the same warm-affinity
  benefit; upstream-fit only kicks in when the cross-region gap
  exceeds `UpstreamFitMinDeltaMs`.
- **Single-node observability now, multi-node behaviour later.**
  PR-C ships the metric surface on day one. PR-D's bias is
  conditional. The feature exists on a single-box install, the
  payoff lands at M9.

### Negative

- **Static inference misses.** The provider→region table is small.
  An unknown host returns `"", 0.0`. The explicit-override POST
  unblocks every customer who hits this.
- **RTT is reachability, not query time.** A handshake + a Postgres
  query are different beasts. We'll need a follow-up "query-shape
  probe" if read-vs-write skew appears.
- **Quota + free-tier gating is a UX cost.** A Free customer who
  wants to see the inferred hints has to upgrade. The
  `data_upstream_quota_exceeded` error code surfaces the upgrade
  path explicitly.
- **OpenAPI / SDK surface widens.** Three new routes, three SDKs to
  regen. Mechanical, follows the established `make sdk-…` target.

### Out of scope (deferred)

- **A9 pressure-rebalance respecting upstream-fit.** Future PR-cluster
  once M9 lands production traffic.
- **Per-deployment scope overlay** (PK widens to
  `(app_id, deployment_scope, kind, host, port)`). Parallel work to
  ADR-091's `deployments.scope`. Co-ship-able in a future cluster.
- **Customer-facing inspect latency history**
  (`GET /v1/apps/{slug}/upstreams/history` — separate time-series).
- **Per-host probe at edge nodes (gatewayd-public).** Future ADR.
- **Inference table growth as providers ship new region prefixes.**
  Tracked in `docs/storage.md` + CHANGELOG; future-cluster follow-up.
- **Wire-level trim of host hashes from audit logs.** PR-D ships the
  redaction; full SIEM-format hardening is a future security review.

## Rejected alternatives

- **Hard-require explicit declaration (no inference).** Rejected: the
  customer's primary value from this feature is that "it just works"
  on a Neon / Upstash / RDS deploy without configuration. Inference
  is the dominant case; explicit override is the escape hatch.
- **Run the probe inside the wake loop.** Rejected: a cold wake
  cannot afford a 3s probe timeout. Probe results are amortised
  across N wakes; sampling cost is per-host, not per-wake. meterd is
  the natural owner.
- **Hard-gate wakes that don't fit (reject if no local-region node
  has headroom).** Rejected: creates surprise tail-latency and
  punishes the single-box install. Bias, never gate — same posture
  as warm-affinity.
- **Extend the existing edge-rules table (migration 00218) to hold
  "data-locality" rules.** Rejected: edge-rules are
  customer-authored (DB-IP Lite CC-BY-4.0). Data placement is
  platform-derived. Different lifecycle, different schema, different
  ownership — keep them in separate tables.
- **Per-deployment `data_upstreams.scope` PK widening now.** Rejected:
  ADR-091 already widens `deployments.scope`; co-ship them in a
  future cluster to avoid two PK-changes in one release.
- **Probe via real DB queries (e.g. `SELECT 1`).** Rejected: leaks
  query traffic into the customer's datastore, requires protocol
  knowledge per kind. Handshake timing is sufficient for v1 and
  protocol-agnostic.

## Compatibility

- `data_upstreams` + `data_upstream_probes` are new tables — no
  existing data is affected. Feature flags default OFF so existing
  single-node installs are byte-identical until ops flip the flags.
- `ChoosePlacement` widens by one field on `Request`; the
  pure-function signature remains `func ChoosePlacement(nodes []state.ComputeNode, usedMB, usedVCPU map[string]int64, r Request) (Placement, error)`.
- OpenAPI is additive — three new paths + two new error codes
  (`data_upstream_quota_exceeded`, `data_upstream_invalid`). Existing
  routes unchanged.
- `pkg/api/limits.go` widens by three fields; existing plans
  backfill from defaults via the column DEFAULT clause at table
  build (the file is not a SQL table — the defaults are inline
  struct literals).

## Acceptance

- `make test` — limits_test, handlers_upstreams_test,
  upstream_probe_test, placement_test (new row),
  upstream_affinity_test all green.
- `make lint` — clean across the four-job split (lint+build /
  unit-tests / spec-check parallel; per repo memory
  `ci-three-job-split`).
- `make spec-check` — `api/openapi.yaml` and
  `pkg/apid/openapi.yaml` in sync; new error codes
  (`data_upstream_quota_exceeded`, `data_upstream_invalid`) present.
- `make sdk-coverage` — three SDKs (go / node / python) regenerated,
  new paths present, kebab-pinned (`pr-819-sdk-coverage-method-routemap-pin`
  memory).
- `make metal-lima` — for any package that touches `pkg/fcvm`,
  `pkg/netns`, `vmmd`, `builderd`. PR-D touches `pkg/sched` only;
  metal suite is a regression guard.
- `make leakcheck` — zero leaked netns / TAPs / cgroups (cluster
  touches no VM lifecycle code path).
- `cmd/e2e/upstreams_e2e_test.go` (PR-B) — PG-backed: `PUT
  DATABASE_URL=…` produces a `data_upstreams` row; `POST
  /v1/apps/{slug}/upstreams` override wins; quota trip returns the
  new error code.
- `cmd/e2e/upstream_probe_e2e_test.go` (PR-C) — insert a
  `data_upstreams` row pointing to `127.0.0.1:NNNN`; meterd probes
  and writes `data_upstream_probes` within 1 min.
- Slot fence discipline — migration 00226 lands as
  `00226_data_upstreams.sql` at PR-A merge base, fence file
  `git rm`'d. Sibling-PR collisions handled per the four-step
  renumber playbook (`pr-849-adr-092-pr-a-slot-chase-cluster`).
  PR-0 was renumbered three times: 00219→00221 after PR #855 landed
  `00219_edge_rules_kind_limit.sql`, 00221→00223 attempted when PR
  #863 (ADR-096 PR-A) added its own `00221_reserve_slot.sql` fence
  (the 00223 jump failed `TestMigrationsContiguous` — fences cannot
  skip slots on a branch), then 00221→00226 after PR #863 MERGED
  (taking ownership of 00221 on main) and PR #866 (CORS) added 00223
  + 00224 + 00225 fences. Slot 00226 is the next free contiguous
  slot on main (see `main-duplicate-slot-pattern-2026-08-10`,
  `cross-pr-slot-fence-reservation-fence-pattern`,
  `cross-pr-slot-precheck-pr-867-collision-2026-08-13` memories).

## References

- `pkg/sched/placement.go:96` — `ChoosePlacement`, the pure-function
  chooser this ADR extends.
- `pkg/sched/warmaffinity.go:55-159` — `WarmAffinity`, the
  TTL hint cache shape `UpstreamAffinity` mirrors.
- `pkg/sched/engine.go:2068-2125` — `choosePlacementLocked`, the
  engine site that injects hints into `Request`.
- `pkg/sched/pressure_aggregator.go` — the sliding-window polling
  shape `pkg/meter/upstream_probe.go` mirrors.
- `pkg/reposcan/denylist.go:27-45` — the parallel image→hint table
  this ADR extends with a key→kind table.
- `pkg/api/limits.go` (`EdgeRulesGeoPerApp`, `EgressAllowlistMaxSize`)
  — the quota table this ADR widens by three fields.
- `cmd/apid/handlers_env.go:1` — the env handler whose `?scope=`
  plumbing the upstreams handler mirrors.
- `pkg/meter/loop.go:33-129` — `Loop`, the six-timer pattern the
  probe loop slots into as the seventh.
- `docs/adr/090-named-envs.md` — the env-write plumbing precedent
  this ADR builds on.
- `docs/adr/091-deployments-scope.md` — the per-deployment scope
  precedent this ADR aligns with.
- `docs/adr/087-tier-a9-capacity-pressure-rebalance.md` — the
  polling-loop precedent `pkg/meter/upstream_probe.go` mirrors.
- `docs/adr/046-stateless-runtime-advisory.md` — the
  stateless-contract guard the Free-tier gating enforces.
- `docs/adr/009-snapshot-inner-netns.md` — the snapshot-reuse
  invariant `upstream_fit`'s 5 ms threshold respects.
- `docs/storage.md` — the BYO-storage contract this ADR makes
  placement-aware.
