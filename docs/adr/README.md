# Architecture Decision Records

ADR-001 through ADR-010 are **accepted and locked for v1**; they live inline in
[`../faas_implementation_spec.md`](../faas_implementation_spec.md) §3, not as
separate files here. This directory holds ADRs made *after* the spec.

Any deviation from the spec requires a new ADR here first (spec §3, CLAUDE.md).

## Format

```
# ADR-NNN · <title>

- **Status:** proposed | accepted | superseded by ADR-MMM
- **Date:** YYYY-MM-DD
- **Decision:** <what we're doing>
- **Why:** <the forcing reason>
- **Consequences:** <what this makes true, including new surfaces/milestones>
- **Rejected alternatives:** <options considered and why not>
```

## Log

| ADR | Title | Status | Source |
|---|---|---|---|
| 001–010 | Locked v1 decisions | accepted | spec §3 |
| 011 | Thin dashboard at launch (was gap G3) | accepted | UX spec §11 — landed before M7.5 code |
| 012 | `githubd` / GitHub App for push-to-deploy | accepted | UX spec §11 — landed before M7.5 code |
| 013 | M1 gRPC codegen: generated protobuf (v1.0) | accepted | M1 plan |
| 014 | M1 wire shape: caller resolves `(app)` | accepted | M1 plan |
| 015 | M1 unix-socket auth (mode 0660 group `faas`) | accepted | M1 plan |
| 016 | M1 `Stats()` shape + `vmmd_*` metric names | accepted | M1 plan |
| 017 | Hand-written `pkg/state/pgstore.go` (M5 sqlc exception) | accepted | M5.1 review |
| 018 | schedd gRPC surface + ReportActivity ownership | accepted | M5 plan |
| 019 | Jailer `--exec-file` invocation + jail resource ownership | accepted | M0 metal run |
| 020 | `pkg/secretbox` host age keypair for sealed customer secrets | accepted | M7 — landed before M8 |
| 021 | Account export + staged deletion (G6 GDPR self-service) | accepted | M8 G6 — landed 2026-07-21 |
| 022 | Post-restore resume hook over AF_VSOCK (V6 ship-blocker) | accepted | M8 PR-A |
| 023 | IPv6 tenant egress policy (`ip6 daddr`, allow-and-restrict) | accepted | M8 |
| 024 | CertMagic cut-over + test closure (gatewayd TLS) | accepted | M8 |
| 025 | Decoupled control plane and compute nodes | proposed | M8 |
| 026 | schedd consumes `NotifyAccountDeletionPending` and evicts live instances | accepted | M8 — landed 2026-07-21 |
| 027 | Stripe push observability taxonomy (11-label + duration histogram) | accepted | M7 hardening |
| 031 | Per-app egress IP allowlist (`cidr[]` on `apps`, post-deny accept) | accepted | M8 tier-2 |
| 032 | MVP auth: harden /login against #165 + real sign-in methods | accepted | issue #165 / PR #1+#2 |
| 033 | Per-app egress IP allowlist — IPv6 mirror (trigger swap + renderer partition) | accepted | M8 tier-2 |
| 034 | IPv6 lateral-movement: 6to4 + Teredo deny (v6 denylist gap from ADR-023) | accepted | M8 tier-2 PR-A |
| 035 | Auth audit log surface (IAM-4: `auth.login`, `key.created`, `account.plan_changed`, …) | accepted | M8 IAM-4 / PR #217 |
| 036 | Per-instance metrics: {app,node} cardinality rollups (issue #170 / PR-A + G10) | accepted | issue #170 |
| 037 | Reactive scale-up trigger (per-app RPS / CPU targets → proactive admit up to max_concurrency) | accepted | issue #169 / #172, M7 follow-up |
| 038 | Build attestation: provenance row + (Phase 3) cosign sign/verify for ext4 layers | accepted | issue #197 B3.x, Tier 3 sprint |
| 040 | OCI layer symlink policy: store `Linkname` verbatim, clamp ancestors on traversal | accepted | fixes imaged crash-loop / cd-digitalocean |
| 041 | Migration slot reservation convention (gate carve-out for cross-PR slot collisions) | accepted | follow-up to #335 / #369 / #352 deadlock |
| 042 | Per-app request metrics + `cold_wake`→`cold_boot` rename; `route` label dropped (ADR-036 precedent) | accepted (partially superseded §1 by ADR-093 for opt-in apps) | issue #273 / #273 |
| 043 | App logs producer stream (Move 4): per-instance ring + schedd fan-out + vmmd Logs RPC | accepted | issue #254, Move 4, M7 observability |
| 044 | Per-plan CPU fairness at the cgroup level (3-level hierarchy + per-plan `cpu.weight` / `cpu.max` + `FaasCpuStarvation` alert) | accepted | issue #301 |
| 045 | Mutable app env via `POST /v1/apps/{id}/env` (replaces immutable `--env`; envelope-sealed, re-encrypted on `RotateKey`) | accepted | Move 2 |
| 046 | Per-instance egress metering (telemetry seam for future egress-billing PR) | accepted | issue #<TBD> (egress billing seam; ADR-039 precedent) |
| 050 | Repo decomposition: `projects` object + multi-workload auto-provision | proposed | `docs/repo_decomposition_implementation.md` |
| 051 | Characterization boot: observed workload classification + in-guest port normalization | accepted | ADR-050 Phase 4 |
| 052 | Adding a function runtime: 7-layer additive procedure | accepted | Tier 1 PR 1+2 worked example |
| 053 | Deploy-time overrides for OCI image deploys (entrypoint/cmd/env/port/healthcheck) | accepted | issue #460 (PR A ships contract; PR B imaged layer injection; PR C port plumbing) |
| 059 | Customer-configurable scaling policy (4-PR: persistence + inflight signal + engine cooldown + worker carve-out) | proposed | issue #462 / PR #493 / #501 / #507 / #512 |
| 060 | Per-app GB-h floor for `min_instances > 0` (meterd synthetic rows + UUID v5 lineage) | proposed | issue #515 (follow-up to #462) |
| 061 | Organizations, memberships, and unpriced seats (IAM-6: account→org split, path-scoped APIs, automatic personal org) | proposed | issue #190 (PR 1 / PR 2+ staged rollout) |
| 062 | Tier A per-node schedd + schedd-side async placement claim | proposed | Phase 2 / Gate A |
| 063 | Tier A snapshot de-localization (residual local-cache semantics) | proposed | Phase 2 / Gate A |
| 064 | Tier A4 cross-node app rebalance (post-drain owner recovery: conditional UPDATE + cooldown + per-tick cap) | proposed | Tier A4 follow-up to ADR-062 deferred item 1 |
| 064 | Per-app private-registry Basic Auth (additive `oci.AuthPuller` + sealed `(app_id, host)` store + per-plan quota) | proposed | issue #461 |
| 065 | Decimal-vs-binary GB-h consolidation (canonical `GBHours` divisor) | reserved | promised by ADR-060 §Decision 8 — separate PR |
| 066 | Tier A5 cross-node live-instance migration (four-phase handoff: Park → mint lease → MigrateInstanceOwner → ack) | proposed | Tier A5 follow-up to ADR-062 deferred item 2 (live instances on the dying node) |
| 067 | Tier A6 migrating-instance watchdog (1 s ticker that self-heals stuck `state='migrating'` rows: re-invite active owner via gRPC, hard-delete dead owner) | proposed | Tier A6 follow-up to ADR-066 §"Open follow-ups" item 1 |
| 068 | Issue #517 closure evidence — AC→PR mapping for LOGGING (correlation, server-side filters, gap semantics) | accepted | issue #517 (PR-A #520, PR-B #524, PR-C #532; docs-only PR, renumbered 067→068 post #538 collision) |
| 069 | Sidecar containers: init + metrics, hard cap 2 (JSONB on `deployments.sidecars`, stateless-only, envelope-sealed env, billing math `plan RAM + Σ(sidecar.ram_mb) + PerVMOverheadMB`) | proposed | issue #463 (PR A ships contract + storage; PR B wires runtime effect; PR C wires e2e + observability; ADR renumbered 066→067→068→069 post #542 merge) |
| 070 | Tier A7 edge split (gatewayd-public / gatewayd-internal; in-process split per box, unix-socket hop, sticky-warm routing, central rate limits, cert replication by lex-min leader) | proposed | Tier A7 — the outer-edge tier that completes the multi-box migration started in ADR-062 (ADR renumbered 068→070 post #540/#543 collisions; PR #547) |
| 071 | Warm-snapshot engine hot-path (Park captures warm + init in one appMu window; warm-only failure path destroys VM; sticky-on-downgrade) | superseded-by: 074 (+ ADR-098 C10 for the 5th capture gate) | issue #470 PR A (extends PR #525 data layer + PR #543 framework_ready signal; ADR slot 071 — slot 070 taken by Tier A7 post #547 merge) |
| 072 | PR-C sidecar billing + observability + portnorm (closes issue #463: AC #1 init_failed emit, AC #3 restart counter, AC #4 OOM gate, AC #5 billing math consumer, AC #6 customer-image cmd, NEW routing-key portnorm) | proposed | issue #463 PR-C (ADR slot 072 — slot 071 taken by issue #470 PR A; closes the sidecar issue; supersedes nothing) |
| 074 | Warm-snapshot audit + GC + ops surface (3 audit kinds with `&app.AccountID` subject; per-tier 2+2 GC floor; 4 gregale flags; `vmmd_guest_init_duration_seconds` + `gateway_wake_snapshot_tier_total` metrics; warm-snapshot Grafana dashboard) | accepted | issue #470 PR C (closes the operations loop on PR A's writable warm tier; 5th capture gate deferred to ADR-073; slot 073 reserved for future owner; renumbered 072→074 post sidecar #463 PR-C merge took 072) |
| 075 | Per-app eviction priority (best_effort vs reserved — apps.eviction_priority column + apps_eviction_priority_chk; `Plan.EvictionPriorityReservedAllowed` gate; `Plan.ReservedConcurrencyPerAccount` cap counts APPS Hobby 1 / Pro 2 / Scale 4; `SelectEvictions` tier-first sort; `schedd_evicted_priority_total{priority,reason}` counter; `app.eviction_priority_changed` audit kind; `gregale app --eviction-priority` flag; thin SDK `SetAppEvictionPriority` one-liner) | accepted | issue #475 (NOT Lambda-style provisioned concurrency; reserved tier protects against eviction, not residency; idle-still-park guarantee via ReapIdle/ReapAggressive unchanged; migration 00135 + slot-fence pattern; closed 6-tuple counter set pre-instantiated) |
| 078 | pkg/daemonunit + pkg/daemonunitspec generator (single source of truth for the 8 production daemon systemd units; emits identical units to cp-cp / cp-sys / cp-ans trees + `deploy/etc/daemons.json`; cd-controlplane reads critical[]/best_effort[] via `jq`; `daemonunit-check` CI gate) | accepted | issue #649 (DEPLOY-2; supersedes per-tree hand-written unit drift; slot 076/077 already taken) |
| 076 | Outbound webhook delivery reliability (`app_webhooks` + `app_webhook_deliveries` tables; schedd `pkg/webhook.Dispatcher` goroutine; 5s tick + 32/tick cap; per-account fairness via `ORDER BY account_id, next_attempt_at`; DLQ at attempt 7; default / aggressive / none retry policies; sealed HMAC secret with namespace `APP_WEBHOOK`; `X-Faas-Webhook-*` headers distinct from `X-Faas-Alert-*`; 5 audit kinds) | accepted | issue #476 (parallel outbound surface — does NOT extend `alert_deliveries`; clock injection via dispatcher struct fields makes the 7.5h DLQ path testable in ≤1s wall; migrations 00140 + 00141 with fence at 00139; ADR-041 slot fence pattern) |
| 080 | Raw-bytes bridge for Upgrade traffic over the gatewayd-internal → vmmd → guest path (WebSocket / h2c / long-poll / MQTT-over-WS) — new `Vmmd.ForwardRawStream` gRPC bidi + `cmd/vmmd-raw-bridge` Go bridge binary (273 lines, `unix.Setns` → `net.Dial` → two `io.Copy` goroutines) + gatewayd-internal three-input detector (`isUpgradeRequest(r) && app.WebSocketEnabled && h.rawByNode != nil`) + 3-line public-hop preservation in `internal_proxy.go:228`; per-app `apps.websocket_enabled` flag (Free false, Hobby+ true via `Plan.WebSocketEnabled()` / `Plan.WebSocketResponseAllowed()`); 100 MiB per-request cap (`api.RawStreamMaxRequestBytes`); `x-faas-upgrade: true` observability header; `evts.Platform` `ProxyFirstByte` events surface (ADR-064); per-app PATCH rollback only (no daemon-level kill switch in merged PR-3 — follow-up issue); follow-ups: `FAAS_GATEWAY_RAW_STREAM_ENABLED` env var, `gateway_ws_*` Prometheus series, per-session byte cap meter | accepted | issue #676 (PR #694 ForwardRawStream wire + vmmd-side handler merged 2026-08-06; PR #702 gateway detector + raw forwarder + e2e merged 2026-08-07 — bundled original PR-3 + PR-4 per user decision) |
| 082 | Per-app customer-facing SLO surface (issue #696 / move-2 PR-A) | accepted | issue #696 |
| 083 | Active-passive HA topology (Tier A8 / §14 M8 / issue #297 slice 6) — lex-min leader election over `compute_nodes.name WHERE active=true`; `pkg/gateway/leader` package (`ElectLeader`, `Leader`, `LeaderStore`); `StandbyState` gauge (`<prefix>_gateway_standby_state` enum: warming=1, warm=2, draining=3) + `ActivePassiveFailoversTotal` counter (`<prefix>_gateway_active_passive_failovers_total{outcome}` for dns_flipped|dns_stale|peer_unreachable|manual_drain); standby warm-up via bounded `cmd/gatewayd-internal` HTTP HEAD scrape (timeout = `HAFailoverProbeTimeoutMS = 500` ms in `pkg/api/limits.go`); drain protocol bounded by `HADNSRecordStaleSeconds = 30`; Hetzner DNS provider (`FAAS_DNS_PROVIDER=hetzner` + `HETZNER_DNS_API_TOKEN` sealed via `pkg/secretbox.SealBytes` namespace `DNS_PROVIDER`) + operator-managed fallback (`FAAS_DNS_PROVIDER=manual` prints the required `curl` to stderr); `make ha-failover-drill` Makefile target on two-node Lima fleet (`deploy/lima/faas-metal-2node-ha.yaml`); two-host property test in `tests/property/concurrency_test.go` (issue #297 acceptance item 5); standalone runbook `docs/runbooks/active-passive-ha.md`; PR-cluster shape PR-A refactor / PR-B functional / PR-C test+deploy | proposed | Tier A8 (post-Tier-A5 multi-box HA; closes §14 M8 "Gate-A runbook (2nd box active-passive)" row) |
| 084 | Traffic splitting — picker signal + largest-remainder redistribution + wake fan-out (issue #556 PR-C) — `PGBackend.Pick` returns `PickResult{Target, OK, Picked, ColdBucket}` so the handler can wake the cold bucket outside the read-lock critical section; `UpdateDeploymentTraffic` uses largest-remainder (Hamilton's method, tie-break fraction DESC + ID ASC, integer arithmetic, Σ=100 by construction) instead of zeroing siblings; `Backend.Admit(ctx, appID, deploymentID, max)` widens the wake signature so the handler can route Phase-2 admits to the deployment the picker landed on; bounded 1 retry per request; notify emit on `updateDeploymentTraffic` with `kind:"traffic"` (defect S1) + `pg_notify` payload widens additively; bonus cleanups (implicit-100 synthesis narrowed to `len(weights)==0`, stale-set pruning in `RefreshDeploymentWeights`, Phase-1 fast path reads `instances.deployment_id`, full WakeResult in `admitAndDispatchForDeployment`, vacuous `TestPg_UpdateDeploymentTraffic_ZerosSiblings` rewritten, gate-order doc fix); `--redistribute` flag deferred to PR-D | accepted | issue #556 PR-C (slot 084 — 082/083/085 already taken; no schema change; 12 new pinned tests + 4 memstore mirrors; closes acceptance items #1 + #2 + #3) |
| 089 | Per-secret rotation (PR-B) + background re-seal walker (PR-C) — `app_secrets.kid text` column + `pkg/rekey.Replayer` skeleton (pinnedCursor + in-Run seen-set for crash-safe walk); `POST /v1/apps/{slug}/secrets/{key}/rotate` handler re-seals one row under the current identity and stamps `kid`; `FAAS_REKEY_ENABLED=true` opt-in for the background walker in `apid` (separate `audit.Auditor` instance with actor="rekey" so dashboards filter background re-seals from user-driven rotates); `GET /v1/admin/secrets/rekey-progress` operator endpoint (admin scope + email allowlist) returning the latest `rekey.RekeyProgress` snapshot; on-disk persistence at `FAAS_REKEY_PROGRESS_FILE` (default `/var/lib/faas/rekey-progress.json`, mode 0o600, atomic rename) for crash-safe resume; `pkg/api.CodeRekeyDisabled` 503 sentinel when the flag is unset; 7 unit tests + 2 e2e (single-daemon rotate + box-test full lifecycle) | accepted | ADR-089 PR-A/B/C (PR-A+B shipped via PR #800; PR-C is this PR — slot 089 — slot 088/090 reserved) |
| 093 | Opt-in per-route observability inside an app (gatewayd-internal) — `apps.route_metrics_enabled` boolean + plan gate (Free off, Hobby+ on) + operator kill-switch (`[route_metrics] enabled` in `cmd/gatewayd-internal/config.go`); bounded per-app route cap (50) with `__route_other__` non-evicting overflow; `gateway_request_duration_seconds{app,route,class}` + `gateway_requests_total{app,plan,route,code}` + `gateway_request_failures_total{app,plan,route,code}` Prometheus series; bounded in-memory reader `GET /v1/internal/apps/{slug}/routes` on the loopback control listener, reverse-proxied by apid as `GET /v1/apps/{slug}/routes`; route label = method + raw path (pre-rewrite, `WithRouteLabel` stash on `r.Context()`); new `pkg/gateway/route_label_set.go` mirroring `account_label_set.go`; new `pkg/gateway/control_routes.go`; recording rule `faas_gateway_request_rate_5m:by_route` + `GatewayWildcardRoute` info-severity alert for sustained `__route_other__` overflow; partially supersedes ADR-042 §1 (the per-app `{app, class}` histogram and `cold_boot` rename are preserved verbatim); fixes pre-existing spec §12:780-786 ADR-041→ADR-042 mis-cite. | accepted | issue #273 (customer-facing per-route intent) |
| 095 | PR-preview environments (issue #272) — bridge child deployment → ephemeral `preview_app` row (parent_app_id + preview_pr_number + preview_sha); `app_envs` shadowed by `app_preview_envs` for the preview lifetime; webhook decoder + `Service.handlePullRequest` + Checks API create-on-push + teardown-on-close; per-account preview concurrency cap (`plan_previews_table`); preview URL `https://pr-{N}--{slug}.{domain}`; `pkg/githubd/checks.go` writes `check_run` status; CLI surface deferred. PR-A spine shipped (PR #851); PR-B routing + PR-C teardown in flight. | proposed | issue #272 (PR-A spine shipped in PR #851; PR-B routing + PR-C teardown in flight; slot 095 — coexists with ADR-098 which renumbered 095→098 post-merge to avoid the slot 095 collision; both ADR files at `docs/adr/095-*.md` and `docs/adr/098-*.md`) |
| 097 | schedd wake-phase telemetry — `schedd_wake_rpc_duration_seconds{app, phase}` HistogramVec on schedd, closed-set `phase ∈ {admit_to_rpc, rpc_call, rpc_to_running}`; spec §6.3 buckets + low-end `0.01` for `admit_to_rpc`; `wake_id` attached as `prometheus.Exemplar` (no label cardinality cost) so operators join to the `events` table and to `gateway_wake_latency_seconds` on the gateway side; new instrumentation at engine.go:1744 / :1759 / :1932; three new observations per wake on the success path (error paths use `events.BootFailed - BootStarted`); spec §6.3 paragraph + §12.1 row updated; mirrors the existing `schedd_guest_init_duration_seconds{app, runner}` pattern at metrics.go:1172-1177. Renumbered 093 → 096 → 097 to avoid collision with ADR-093 (per-route-app-metrics, issue #273 / PR #861) and ADR-096 (customer-facing error grouping, PR #863 PR-A — slot reserved for PR-C's `docs/adr/096-customer-error-grouping.md`). | accepted | P1B (P1 autoscaling PR-cluster: P1C + P1A + P1B; ADR-097 ships as commit 1 of the cluster) |
| 098 | Wake single-flight coordinator on `sched.Engine` — `pkg/sched/wake_coord.go` (NEW) mirrors `pkg/gateway/gate.go` state machine (`done`/`waiters`/`completed`) but lives on the engine; leaf-lock rule `wakeCoord.mu` acquired+released **before** `e.lockApp(appID)`; new additive `schedd.EnsureWake` gRPC method (`api/proto/onebox/faas/schedd/v1/schedd.proto`, mirror WakeResponse tag layout 1–7); all five wake producers (gateway, cron `loop.go:1969`, floor `floor/trigger.go`, scaleup `scaleup/trigger.go`, targets `targets/trigger.go`) route through `Engine.EnsureWake`; `pkg/gateway/WakeGate` retains its role as in-process pre-filter (a cache in front of the authority); one `defer` closure at leader entry + `sync.Once` `finish()` covers all five completion sites (`engine.go:1435, 1818, 1823, 1830-1831, ~1892`); new `pkg/db.NotifyAppDelete` pg-notify channel + `pkg/sched/app_delete_subscriber.go` (modeled on `pkg/sched/deletion_subscriber.go`) calls `Engine.wakeCoord.Forget(appID)`; detached-ctx contract on leader's `ensure` (`context.Background()` + `WakeQueueTTLSeconds=30` at `pkg/api/limits.go:1567`); 4 invariants preserved (cold boot truth §4.6, wake never depends on snapshot ADR-005, identical inner net ADR-009, admission ceiling §6.2-2); migration 00221 adds `instances.request_count BIGINT NOT NULL DEFAULT 0` for the warm-snapshot 5th promotion gate (gate #5 at `engine.go:3876`, between MinMs `:3870-3875` and `warmKeysFor` `:3880`) | accepted | PR #854 (slot 095 collision with PR-preview #851 / issue #272 → renumbered 095 → 098 post-merge; supersedes nothing; closes the §6.2-1 single-flight correctness gap that ADR-070 introduced for the gatewayd-public/gatewayd-internal split; complements ADR-074's warm-snapshot ops close-out by adding the missing per-app request-count gate) |

ADR-011 and ADR-012 are required by the UX spec (§11) before git-deploy work
begins at M7.5; both landed on 2026-07-17 alongside the M7.5 PR open.

## PR-E (2026-08-09) legacy gatewayd narration

PR-E swept the legacy `cmd/gatewayd/` narration across the rest of `docs/adr/`
by appending a `Superseded (in part, PR-E):` banner immediately after each
ADR's existing `Status` line (or its existing superseded block). The banner
points readers at ADR-070 as the source of truth for the
`gatewayd-public` / `gatewayd-internal` split and notes that any
`cmd/gatewayd/<file>.go` citations in those bodies are stale.

Files carrying the banner:

- docs/adr/011-thin-dashboard.md
- docs/adr/012-githubd.md
- docs/adr/015-unix-socket-auth-v1.md
- docs/adr/016-vmmd-stats-and-metrics.md
- docs/adr/018-schedd-grpc-surface.md
- docs/adr/023-ipv6-egress-policy.md (banner appended after the existing PR-D block)
- docs/adr/024-certmagic-cutover.md
- docs/adr/025-decoupled-control-plane-and-compute.md
- docs/adr/028-gatewayd-remote-routing.md
- docs/adr/029-apid-compute-nodes-admin.md
- docs/adr/037-reactive-scaleup-trigger.md
- docs/adr/040-per-account-rate-limit.md
- docs/adr/041-tenant-abuse-observability.md
- docs/adr/042-per-app-metrics-and-cold-boot-rename.md
- docs/adr/042-webhook-replay-protection.md
- docs/adr/045-account-scoped-list-endpoints.md
- docs/adr/046-egress-metering-visibility.md
- docs/adr/046-pkg-auth-extraction.md
- docs/adr/047-streaming-response.md
- docs/adr/052-control-plane-mtls-and-handler-peer-binding.md
- docs/adr/055-per-host-egress-policy-templating.md
- docs/adr/056-wire-node-verifier.md
- docs/adr/062-tier-a-per-node-schedd-and-placement.md
- docs/adr/064-tier-a4-cross-node-rebalance.md
- docs/adr/064-wake-timeline-canonical-vocabulary.md
- docs/adr/066-tier-a5-cross-node-live-migration.md
- docs/adr/068-issue-517-closure-evidence.md
- docs/adr/074-warm-snapshot-audit-gc.md
- docs/adr/075-deploy-1-capdecl-mount-rpc-boundary.md
- docs/adr/076-session-binding-hash.md
- docs/adr/078-deploy-2-daemonunit-generator.md
- docs/adr/079-customer-handler-on-unix-socket-and-h2c.md
- docs/adr/079-per-app-public-auth.md
- docs/adr/080-per-app-async-task-queue.md
- docs/adr/080-raw-bytes-bridge-for-upgrade-traffic.md
- docs/adr/081-durable-execution-workflows.md
- docs/adr/083-active-passive-ha-topology.md
- docs/adr/084-traffic-splitting-pr-c.md
- docs/adr/090-named-envs.md (named envs / `app_envs.scope` + scope-aware wake-time overlay; cluster outlined in `docs/adr/090-pr-cluster-outline.md`)

ADR-070 itself is the source of truth for the split and carries an end-of-file
note instead of the banner.
