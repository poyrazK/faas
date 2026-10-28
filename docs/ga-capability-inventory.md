# GA capability inventory — 2026-10-23

Every customer-reachable capability on `main` at `2f64ff1117`, built from the
code rather than from `pkg/productcap/catalog.json`.

**Sources cross-checked:**
- the apid route table: ~2,200 `/v1` route patterns in 238 route groups, every
  group mapped to a row;
- non-`/v1` HTTP routes on every daemon (OAuth, `.well-known`, Slack/Linear
  hooks, status, live queries, registry);
- the CLI command table (`cmd/gregale/cli_meta.go`, 127 commands);
- the dashboard templates (`pkg/dashboard/templates`, ~110 pages);
- the `gregale.yaml` schema (`pkg/gregalemanifest`);
- the plan limits table (`pkg/api/limits.go`, 198 fields);
- every CLI subcommand, every doc page in `docs/`, the OpenAPI tags (63), the
  database tables (1,091), the SDK packages, the environment and endpoints
  guest-init gives apps, gateway headers and reserved paths, and customer
  notification email kinds;
- the daemons in `cmd/`, the runner images, the SDKs in `sdk/`, platform flags
  (`pkg/platformflags/catalog.go`), operator env flags in `deploy/ansible`, and
  `docs/customer-pages.json`.

**Result:** 167 capabilities. All 53 catalog entries are covered below, and
**110 capabilities have no catalog entry**, including core ones (functions,
crons, secrets, logs, health probes, account, billing). Section J is
operator-only or not on `main`. Unmerged local branches are out of scope.

**Completeness check (ci-light gate, `make capability-coverage-check`):** `python3 -I scripts/ga/capability_coverage.py`
enumerates every entry point above straight from the code (about 5,500
identifiers across 16 sources) and maps each one to a row here, or to an
explicit non-capability class (INFRA, TOOLING, TEST). It exits non-zero if
anything is unmatched or if a row has no entry point. Current result: 0
unmatched, 0 rows without an entry point. The non-capability bucket was
audited by hand and hides no feature.

Columns:
- **Catalog**: the catalog id and maturity, or **MISSING**.
- **Gate today**: a platform flag (`pf:`), an operator env flag (`env:`) or a
  CLI audience, when one exists. `—` means no gate was found in the catalog,
  platform flags or deploy defaults.
- **Tier**: pre-filled recommendation (`GA`, `beta`, `preview` or `internal`); see below.

## Tier recommendation (pre-filled, for owner review)

| Tier | Rows | Meaning |
|---|---|---|
| `GA` | 46 | Ships in v1.0, billed, held to the "ready to charge" bar. |
| `beta` | 32 | On for every account, labelled beta, may change, no service credits. |
| `preview` | 47 | Off by default; released per account with a platform flag (design partners). |
| `internal` | 42 | Hidden from customers; development frozen until after v1.0. |

**How the tiers were chosen**
- **GA** is the compute product the financial model sells: deploy (source,
  GitHub, image), runtimes, scale-to-zero, domains, env and secrets, health
  probes, logs and metrics, rollback, crons, account, keys, billing, GDPR, CLI,
  dashboard, docs and status page. PR preview environments joined GA on
  2026-10-26 (ADR-20261026090000) with live-path acceptance evidence.
  Multi-service projects (projects, internal services, service bindings and
  service wake-ahead) joined GA on 2026-10-27 (ADR-20261027120000) with
  end-to-end acceptance evidence on every pull request.
- **beta** is what customers expect around that core and what already runs on
  production, but should not hold up the charge bar: canaries,
  jobs and workflows, edge rules, caching, alerts, orgs.
- **preview** is every large standalone product (object storage, Postgres,
  Data API, realtime, live queries, application Auth, AI gateway, durable
  entities, events and queues, Operations, MCP hosting) and every feature that
  is off or flagged today. Resource-holding products need proven backups and
  plan limits before anyone depends on them.
- **internal** is operator tooling, frozen experiments, and features that
  duplicate or decorate others: the route-analysis family (8 rows, except
  route monitoring), automations, scenario tests, platform tenants, API
  consumers and monetization, and the internal CI, hosting and mail systems.

**Calls worth a second look**
1. Function runtimes (`GA`): Node 22/24 and Python 3.12/3.13 only; Go and the
   static binary runtime stay beta until each passes runtime qualification.
2. Streaming (`GA`): streaming, SSE and WebSockets; gRPC stays beta.
3. Autoscaling (`GA`): min instances and max concurrency; target RPS/CPU and
   scheduled scaling stay beta.
4. Billing (`GA`): plans, usage, invoices, overage cap and per-app costs; the
   FOCUS export stays beta.
5. Env vars (`GA`): environment clone belongs with project environments
   (`preview`); projects themselves are GA.
6. Snapshot lifecycle hooks (`beta`): promote to GA if apps that hold database
   connections need `after_restore` to reconnect reliably after a wake.
7. Release commands (`beta`): many teams expect a migration step before
   traffic; promote if beta customers depend on it.
8. Synthetic checks (`preview`): an always-on synthetic blocked a node drain
   during the rc.290 rollout (2026-10-22).

**Gating work implied:** 48 rows recommended `preview` or `internal` have no
gate today (`—` in the Gate column) and need one before launch. Rows for the
internal hosting service and Gregale Agents are already unreachable.

## A. Deploy and build

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 1 | Apps: create, update, rename, delete, restore deleted | `/v1/apps`, `deleted-apps`, `rename`, `restore` | **MISSING** | — | GA |
| 2 | Source deploys: local tree, tarball, Dockerfile; resumable uploads | `deployments`, `/v1/uploads`, `gregale deploy` | `source-deploy` ga | `pf:source_deploy` (upload routes and multipart deploys; deployment status is core `apps`, ADR-20261026152731) | GA |
| 3 | Builds in builder microVMs: Railpack, build logs, build cache | builderd, `/v1/builds` | **MISSING** | — | GA |
| 4 | Container / OCI image deploys, incl. image-published deployments; image preflight | `deploy --image`, `image-published`, `gregale doctor --image` | `container-deployments` beta | — | GA |
| 5 | GitHub: App install, push-to-deploy, Checks status | githubd, `/v1/install`, `github`, `connect` | `github-deploys` beta | — | GA |
| 6 | GitLab connect and deploys | `/v1/gitlab/*` | **MISSING** | — | preview |
| 7 | Service migration: public "would my app run here" preflight, import from GitHub, verify, cut over | `/v1/preflight` (unauthenticated), `project-migrations`, `migration-verifications`, `migration-cutovers` | **MISSING** | — | preview |
| 8 | Repo decomposition: split a repo into apps | `gregale decompose` | **MISSING** | — | internal |
| 9 | Starter templates (17) | `/v1/templates` | **MISSING** | — | GA |
| 10 | Framework profiles: detect start command, port, health path | | `framework-profiles` preview | — | beta |
| 11 | `gregale.yaml` deploy overrides, app config | `/v1/apps/{slug}/config` | **MISSING** | — | GA |
| 12 | Source-ref deploys: one-shot pinned CI deploy | `docs/source-ref.md` | **MISSING** | — | beta |
| 13 | Per-app deploy tokens for CI | `deploy-tokens` | **MISSING** | — | GA |
| 14 | Signed deploys: trusted signers, `require_signed` | `trusted_signers`, `security` | **MISSING** | admin-only toggle | internal |
| 15 | Private registry credentials | `registry-credentials`, `gregale registry` | **MISSING** | — | GA |
| 16 | Stateless-contract checks and source review | `source-review`, dashboard `stateless` | **MISSING** | — | GA |
| 17 | Affected-workload preview: what a deploy will touch | `gregale diff`, `gregale scan`, `deploydiff` | **MISSING** | — | beta |
| 18 | Deploy-time vulnerability scan (Grype) and secret scan | `gregale deployment --show-scan --show-secret-scan` | **MISSING** | — | beta |
| 19 | Projects: multi-app apply, submissions, project previews | `/v1/projects` (93 routes), `gregale projects` | `projects` ga | — | GA |
| 20 | Environments and Environment GitOps (field ownership) | `gregale environment`, `environment-field-ownership` | **MISSING** | — | preview |
| 21 | App tasks: one-off commands (`app exec`, ADR-230) | `/v1/apps/{slug}/tasks` | **MISSING** | `env: FAAS_APP_TASK_API_ENABLED=0` | preview |
| 22 | Image runs: run an image interactively | `/v1/image-runs` | **MISSING** | `faas_image_runs_enabled: false` | internal |
| 23 | Deploy queue controls: reorder, deploy immediately, cancel, clear obsolete | `gregale deploy` queue routes | **MISSING** | plan: `QueueControlsAllowed` | beta |

## B. Releases

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 24 | Rollback to a named revision | `rollback`, `gregale rollback` | `progressive-rollouts` beta | — | GA |
| 25 | Progressive rollouts: canary ladder, traffic split, automatic rollback on 5xx | `rollouts`, `gregale canary` | `progressive-rollouts` beta | — | beta |
| 26 | Deployment aliases | `deployment-aliases`, `gregale alias` | **MISSING** | — | beta |
| 27 | Pull-request previews | `previews` | `pr-previews` ga | — | GA |
| 28 | Release gates on customer signals | `release-gates` | **MISSING** | — | preview |
| 29 | Release approvals and approval policy | `release-approvals`, `release-approval-policy`, `gregale approvals` | **MISSING** | — | preview |
| 30 | Deploy freezes and release windows | `/v1/freezes`, `gregale freeze` | **MISSING** | — | preview |
| 31 | Scheduled releases | `/v1/scheduled-releases` | **MISSING** | — | preview |
| 32 | Release review | `release-review` | **MISSING** | — | internal |
| 33 | Deployment staging, incl. cron staging | `deployment-staging`, `deployment-cron-staging` | **MISSING** | — | internal |
| 34 | Release commands: a command (e.g. migrations) that must succeed before a deployment takes traffic | `gregale.yaml` `release` | **MISSING** | — | beta |
| 35 | Traffic mirroring | `mirrors`, `gregale mirror`, `gregale traffic` | **MISSING** | — | preview |
| 36 | Critical route health during canaries | `route-health` | `route-health-canary-guard` preview | — | preview |
| 37 | Alert-triggered rollbacks | `alert-rollbacks` | **MISSING** | — | preview |

## C. Runtime and compute

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 38 | Scale to zero: park idle, wake on request | | `scale-to-zero` beta | — | GA |
| 39 | Browser wake page: a waiting page for browser visits during a cold wake | `pkg/gateway/wake_page.go` | **MISSING** | — | GA |
| 40 | Firecracker isolation | | `firecracker-isolation` beta | — | GA |
| 41 | Function runtimes: Node 22/24, Python 3.12/3.13, Go 1.24 (+alpine), static binary | `guest/runners`, `images/runner-*`, `docs/runtimes` | **MISSING** | — | GA |
| 42 | Manual park / wake, prewarm | `park`, `wake`, `prewarm(s)` | **MISSING** | — | GA |
| 43 | Autoscaling and min instances (target RPS / CPU), scheduled scaling | `autoscale_target_*`, `min_instances` on apps | **MISSING** | — | GA |
| 44 | Service wake-ahead | `service-wake-ahead`, `gregale wake-ahead` | `service-wake-ahead` ga | — | GA |
| 45 | Frozen tier (fast thaw) | operator env `FAAS_FROZEN_TIER`, `FAAS_FROZEN_FAST_PATH` | **MISSING** | operator env | internal |
| 46 | Instances: list, restart, runtime-config restarts | `instances`, `restart`, `gregale ps` | **MISSING** | — | GA |
| 47 | Health probes and auto-restart: startup, readiness, liveness; restart policy, stop signal, grace period, drain timeout | `gregale.yaml` probes, `restart_policy` | **MISSING** | — | GA |
| 48 | Snapshot lifecycle hooks: run app code before checkpoint and after restore | `gregale.yaml` `before_checkpoint`, `after_restore` | **MISSING** | — | beta |
| 49 | Resource sizing: RAM, CPU millicores, scratch disk, disk I/O profile, workload classes (http / worker / job), replicas | `gregale.yaml` | **MISSING** | — | GA |
| 50 | Warm pool and warm snapshots | app `warm_pool_size` | **MISSING** | plan: `WarmPoolAllowed` (Hobby+) | beta |
| 51 | Reserved concurrency and eviction priority | app `eviction_priority` | **MISSING** | plan: `ReservedConcurrencyPerAccount` | preview |
| 52 | Application companions (sidecars) | `sidecars` | `companions` preview | — | preview |
| 53 | Disposable isolated runs, execution workflows, artifact grants | `/v1/executions` | `disposable-runs` preview | `env: FAAS_EXECUTION_API_ENABLED=0` | preview |
| 54 | MCP hosting: readiness, catalog monitor, OAuth, tasks | `mcp-*` | `mcp-hosting` preview | — | preview |
| 55 | Production forks | `forks` | `production-forks` preview | `env: FAAS_APP_FORKS` | preview |
| 56 | Crash snapshots | `crash-snapshots` | `crash-snapshots` preview | `env: FAAS_CRASH_SNAPSHOTS` | preview |
| 57 | Dev Bridge: run one service on a laptop | `/v1/dev`, `gregale dev` | `dev-bridge` internal | `faas_dev_bridge_enabled: false` | internal |
| 58 | Developer environments: expiring `gregale dev` environments, developer session leases, file watch and sync, live source patch without rebuild | `/v1/dev/sessions`, dashboard `developer_environments` | **MISSING** | plan: `DeveloperApps` | preview |
| 59 | Scenario tests and release rehearsal: run scenarios against expiring developer environments, record and replay, network fault injection | `/v1/dev/test-runs`, `gregale test`, `gregale chaos` | **MISSING** | — | internal |
| 60 | Env vars: diff, history and rollback, push / pull, environment clone, secret scanning | `env`, `env-diff`, `gregale env` | **MISSING** | — | GA |
| 61 | Secrets: sealed values, references, revocations, hot reload without restart, delivery status | `secrets`, `secret-references`, `secret-revocations`, guest `FAAS_SECRETS_RELOAD_*` | **MISSING** | — | GA |
| 62 | Workload identity: short-lived OIDC tokens for apps | guest-init + vmmd, `/v1/auth/oidc/exchange` | **MISSING** | — | beta |

## D. Networking and edge

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 63 | Custom domains with managed TLS, wildcard domains (Pro+), domain doctor | `/v1/domains` | `custom-domains` beta | doctor: `env: FAAS_DOMAIN_DOCTOR_ENABLED=false` | GA |
| 64 | Streaming, SSE, WebSockets, gRPC, protocol selector, streaming cap | `streaming-cap` | `streaming-and-grpc` beta | — | GA |
| 65 | Internal services (call apps by name) | | `internal-services` ga | — | GA |
| 66 | Service bindings, verification and probes | `bindings`, `gregale bindings` | `service-bindings` ga | — | GA |
| 67 | Private apps | | `private-apps` preview | — | beta |
| 68 | Private networks | `/v1/networks` | **MISSING** | — | preview |
| 69 | TCP listeners | `tcp-listeners`, tcpd | **MISSING** | `faas_tcpd_enabled: false` | preview |
| 70 | UDP listeners | `udp-listeners`, udpd | **MISSING** | `faas_udpd_enabled: false` | preview |
| 71 | Static egress IP | `static-egress-ip` | **MISSING** | `pf: static_egress_ip` | preview |
| 72 | Egress allowlist and extra egress ports | app egress settings | **MISSING** | plan: `EgressAllowlistAllowed` | beta |
| 73 | Data upstream discovery and placement hints | `upstreams`, `DataPlacementHintsPerApp` | **MISSING** | plan limit | internal |
| 74 | Edge rules (incl. host / path routing declared in `gregale.yaml`): route, rewrite, redirect, headers, CORS, JWT, IP, request validation, geo, async, throttle, cache, retry, circuit breaker, WAF, challenge, maintenance; rule lists; rule trace | `edge-rules`, `edge-rule-lists` | **MISSING** | — | beta |
| 75 | CORS presets | `cors-presets`, `gregale cors` | **MISSING** | — | beta |
| 76 | Edge protection: pre-auth guard, challenge under attack, WAF | `edge-protection`, `pre-auth-observations` | **MISSING** | — | beta |
| 77 | App access control: per-app authentication (`require_authn`), public bearer / basic auth, IP allowlist | `appaccess` | **MISSING** | — | GA |
| 78 | Throttle suggestions, route priorities | `throttle-suggestions`, `route-priorities` | **MISSING** | — | internal |
| 79 | Session affinity: sticky routing to one instance by signed cookie | `pkg/gateway/session_affinity.go` | **MISSING** | — | beta |
| 80 | Declarative response caching | `cache` | `declarative-response-caching` preview | — | beta |
| 81 | Async routes at the edge: accept and run a route asynchronously | `gregale.yaml` `async_routes`, `execution_mode` | **MISSING** | — | preview |
| 82 | Tenant surfaces: per-customer hostnames (ADR-100) | `tenant-surfaces` | **MISSING** | CLI compatibility audience | preview |
| 83 | OpenAPI: captured discovery, import and merged spec, contract preview and diff | `openapi`, `openapi_discovery`, `openapi_import` | `openapi-contract-preview` preview | — | preview |
| 84 | Route requirements and policy plans, route budgets, route removal | `route-requirements`, `route-policy`, `route-budgets`, `route-removal` | `route-requirements` preview | — | internal |
| 85 | Route advisor, incl. learned cacheability (ADR-1188) | | `route-advisor` preview | — | internal |
| 86 | Route lifecycle review | `route-lifecycle` | `route-lifecycle-review` preview | — | internal |
| 87 | Route source impact | | `route-source-impact` preview | — | internal |
| 88 | Production route budgets and monitor | `route-monitor` | `production-route-monitoring` preview | — | preview |
| 89 | Discovered routes and route insights (status, investigation, customer impact; ~20 `route-*` docs) | `discovered-routes`, `routes` | **MISSING** | — | internal |
| 90 | Gateway request retry | | `request-retry` internal | `env: FAAS_GATEWAY_RETRY` | internal |
| 91 | Circuit breaking (service and egress) | | `circuit-breaking`, `egress-circuit-breaking` internal | env flags | internal |

## E. Async and events

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 92 | Crons, fire-now | `/v1/crons`, `cron-fire-now-requests` | **MISSING** | — | GA |
| 93 | Jobs, including output artifacts | `/v1/jobs` | `workflows-and-jobs` preview | `env: FAAS_JOBS_DISPATCH=1` | beta |
| 94 | Workflows: code workflows SDK, attention/outcomes/performance, recovery policy | `workflows`, `workflow-*`, `recovery-policy` | `workflows-and-jobs` preview | `env: FAAS_WORKFLOWS_ENABLED=1` | beta |
| 95 | Delayed tasks | `delayed-tasks` | `delayed-execution` preview | — | beta |
| 96 | Async invocations and results | `invoke`, `invocations` | **MISSING** | — | beta |
| 97 | Triggers and event sources: Kafka, NATS JetStream, Redis Streams, AMQP / RabbitMQ, SQS-compatible, bucket events, function webhooks; batch delivery | `/v1/triggers` | **MISSING** | batch: `pf: trigger_batch_delivery` | preview |
| 98 | Events: publish, schemas, subscriptions, deliveries, replays, recoveries, DLQ, work policies | `events`, `event-*`, `dlq`, `work-policies` | **MISSING** | — | preview |
| 99 | Queues: in-platform SQS-compatible HTTP queue, pull leases, queue bindings, queue workloads | `queues`, `queue-bindings`, `queue-workload` | **MISSING** | — | preview |
| 100 | Worker pools | `workers` | `worker-pools` preview | — | preview |
| 101 | Application inbox and outbox | `inbox`, `outbox` | `application-messaging` preview | — | preview |
| 102 | Durable inbound webhooks | `inbound-webhooks` | `durable-inbound-webhooks` preview | — | beta |
| 103 | Outbound webhooks (account and app) | `/v1/webhooks`, app `webhooks` | **MISSING** | — | beta |
| 104 | Operations: typed customer work, approvals, schedules, children, signals, milestones, effects | `operations`, `operation-*` | **MISSING** | `pf: operation_schedules`, `pf: operation_children` | preview |
| 105 | Automations: manual / scheduled / event steps, test suites, baselines | `automations` | **MISSING** | — | internal |
| 106 | Managed exclusive operations | | `managed-exclusive-operations` internal | — | internal |
| 107 | Gregale Commit | `commit-sources` | `gregale-commit` internal | `env: FAAS_COMMIT_RELAY_ENABLED` | internal |

## F. Data

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 108 | Object storage: buckets, signed URLs, upload routes, S3 gateway, versioning, lifecycle rules, object lock, write protection, encryption keys, change notifications, multipart uploads | `buckets`, `upload-routes`, object-storaged, s3-gatewayd | `object-storage` preview | `s3_enabled` | preview |
| 109 | Managed Postgres (account and org databases): point-in-time restore, scale-to-zero, availability classes, resize, connection pool | `/v1/postgres`, org `postgres` | `managed-postgres` internal | CLI operator audience | preview |
| 110 | Data API | `data-api` | **MISSING** | — | preview |
| 111 | Durable entities and sessions, incl. browser sessions, backup, export and restore | `entities` | **MISSING** | — | preview |
| 112 | Realtime channels: resume, tickets, access, occupancy | realtimed, app `realtime` (110 routes) | **MISSING** | `pf: realtime_*` | preview |
| 113 | Live queries | livequeryd | **MISSING** | `pf: live_queries` | preview |
| 114 | AI gateway | outboundd | `ai-gateway` preview | `pf: ai_gateway` | preview |
| 115 | Outbound integrations and bindings: key pools, probes | outboundd, `/v1/outbound`, `outbound-bindings` | **MISSING** | — | preview |
| 116 | Data pipelines | | `data-pipelines` internal | — | internal |
| 117 | Selective data repair | `data-repair` | `selective-data-repair` internal | CLI operator audience | internal |

## G. Observability

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 118 | Logs, tail, log archive with retention | `logs`, `gregale logs`, `gregale tail` | **MISSING** | — | GA |
| 119 | Log drains, log analytics | `log-drains` | **MISSING** | — | beta |
| 120 | Datadog integration | | `datadog-integration` internal | env flag | internal |
| 121 | App, service and account metrics; request analytics | `metrics`, `gregale analytics` | **MISSING** | — | GA |
| 122 | Custom metrics and OTLP metric ingest | `custom-metrics`, `otlp` | **MISSING** | — | beta |
| 123 | Custom metric history | | `custom-metric-history` internal | env flag | internal |
| 124 | Tracing: in-guest request tracing, customer OTLP trace ingest, W3C trace propagation | `gregale trace`, `/v1/traces`, `/v1/otel/v1/traces` | **MISSING** | `pf: guest_tracing` | preview |
| 125 | Alerts, notification channels, alert presets | `alerts`, `notification-channels`, `alert-presets` | **MISSING** | — | beta |
| 126 | Anomaly (baseline) alerts | | `anomaly-alerts` internal | `pf: anomaly_alerts` | internal |
| 127 | Issues: error grouping, Linear and Slack integrations, errors view | `issues`, `errors` | `issues` preview | — | preview |
| 128 | SLOs and error budgets | `slos` | `custom-slos` preview | — | preview |
| 129 | Synthetic checks | `synthetics` | `synthetic-checks` preview | — | preview |
| 130 | Traffic profiles: routes, request mix, attribution | `profiles`, dashboard `profile_*` | **MISSING** | — | internal |
| 131 | Continuous profiling | | `continuous-profiling` internal | env flag | internal |
| 168 | On-demand profile captures: CPU, heap, stacks, Go locks; automatic captures; capture diff | `profiles/captures`, `gregale debug capture`, dashboard `profile_capture*` | `profile-captures` preview | `pf: profile_captures`, `env: FAAS_PROFILING_ON_DEMAND` | preview |
| 132 | Service map | | `service-map` internal | `pf: service_map` | internal |
| 133 | Change timeline | `changes` | `change-timeline` internal | `pf: change_timeline` | internal |
| 134 | Debug: debug bundle and telemetry, dependency view, post-deploy regressions | `debug`, `gregale debug` | **MISSING** | — | beta |
| 135 | Production debugger: request evidence capture and safe replay (ADR-127) | `debugger` API, OTel spans writer | **MISSING** | — | preview |
| 136 | Wake timeline | `wake-timeline` | **MISSING** | — | GA |
| 137 | App health assessment, read without waking the app | `/v1/apps/{slug}/health` | **MISSING** | — | beta |
| 138 | Audit log | `audit-events` | **MISSING** | — | GA |
| 139 | Operational summary | `operational-summary` | **MISSING** | — | internal |
| 140 | Per-app costs and cost trend | dashboard `app_costs_*` | **MISSING** | — | GA |
| 141 | Public status page and incidents | `/v1/status`, `/v1/status/incidents` | **MISSING** | — | GA |

## H. Account, teams and billing

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 142 | Signup, login (password and magic link), MFA, password reset, email verification, CLI login | `/v1/account`, `cli-auth` | **MISSING** | — | GA |
| 143 | Customer notification emails: verification, magic link, password reset, MFA alerts, deploy failed, quota warning, certificate expiry, payment failed, subscription ended, suspension and restore | `pkg/mail` (Resend / Postmark) | **MISSING** | — | GA |
| 144 | API keys with scopes | `keys` | **MISSING** | — | GA |
| 145 | Orgs: members, invitations, roles, ownership transfer, seats | `/v1/orgs` | **MISSING** | — | beta |
| 146 | Org application standards (ADR-595) | `application-standard-*` | **MISSING** | — | internal |
| 147 | Application Auth: end-user auth on GoTrue | `/v1/auth`, `gregale auth` | **MISSING** | `pf: application_auth` | preview |
| 148 | Platform tenants | `platform-tenant-self` (80 routes) | **MISSING** | — | internal |
| 149 | API consumers: end-customer keys (ADR-120) | `consumers` | **MISSING** | — | internal |
| 150 | API monetization: rate cards, consumer plans, billing, signup | `consumer-*`, `rate-cards` | `api-monetization` internal | `pf: api_monetization` | internal |
| 151 | Plans, usage, invoices, overage cap, cost breakdown and forecast, FOCUS 1.4 invoice export | `billing`, `invoices`, `usage`, `gregale billing costs/forecast` | **MISSING** | `faas_billing_mode: disabled` | GA |
| 152 | Budgets: policies, projections, alerts and channels, automatic stops (strict budgets) | `gregale billing budgets`, `financial_budget_*` tables | **MISSING** | `pf: strict_budgets` | beta |
| 153 | GDPR export, account deletion and restore during grace, DPA | `/v1/account/export`, `DELETE /v1/account`, `gregale account restore` | **MISSING** | — | GA |
| 154 | Customer feature flags | | `feature-flags` internal | `pf: customer_flags` | internal |
| 155 | Product control | `product-control` | `product-control` internal | env flag | internal |

## I. Developer tools

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 156 | `gregale` CLI: guided `start` and `menu`, doctor, context, completion, man, open | `cmd/gregale` | **MISSING** | — | GA |
| 157 | Web dashboard | `pkg/dashboard` | **MISSING** | — | GA |
| 158 | SDKs: Node, Python, Go, Kotlin, Swift; auth and data SDKs; webhook verifier, idempotency helpers | `sdk/` | **MISSING** | — | beta |
| 159 | Public OpenAPI spec and capabilities endpoint | `/v1/openapi.json`, `/v1/capabilities` | **MISSING** | — | GA |
| 160 | Gregale management MCP server: AI agents manage Gregale over MCP, with an OAuth server | `gregale mcp serve`, `pkg/mcpmanagement`, `/oauth/*`, `/.well-known/oauth-*` | **MISSING** | — | preview |
| 161 | Docs site and trust pages: DPA, subprocessors, security reporting, security.txt | `docs/customer-pages.json`, `/.well-known/security.txt` | **MISSING** | — | GA |

## J. Operator and internal systems (not customer-facing today)

| # | Capability | Where it lives | Catalog | Gate today | Tier |
|---|---|---|---|---|---|
| 162 | Internal CI: runners, GitHub checks, dispatch | `ci-runner`, `pkg/apid/ciruntime` | **MISSING** | `env: FAAS_CI_*=0` | internal |
| 163 | Internal frontend hosting (sites) | `/v1/internal/hosting/*` | **MISSING** | — | internal |
| 164 | Internal mail service | maild, mailctl | **MISSING** | `env: FAAS_INTERNAL_MAIL_*=0` | internal |
| 165 | Operator admin, compute nodes, `gregalectl` | `/v1/admin`, `/v1/compute-nodes` | n/a | admin auth | internal |
| 166 | Platform OCI registry: internal snapshot and image storage backend | `pkg/oci/registry.go` (`/v2/`) | n/a | internal | internal |
| 167 | Gregale Agents | branch `feat/agent-runs` | not on `main` | — | internal |
