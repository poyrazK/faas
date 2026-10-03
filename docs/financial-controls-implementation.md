# Financial controls implementation

Objective: implement predictable financial visibility and configurable spending controls end to end, as specified in ADR-431. This checklist records outstanding work; completed infrastructure alone does not establish customer availability.

- [ ] Durable usage evidence with retained account/app/job/project/environment/deployment attribution, historical price contracts, replay protection and correction lineage.
- [ ] Exact period costing, one shared allowance allocation, fixed charges/credits/tax and explicit invoice reconciliation.
- [ ] Read-only period, cost breakdown, coverage and forecast APIs, OpenAPI and generated SDKs.
- [ ] Scoped budget intent, previews, overlapping-policy precedence, authorization, audit and period counters.
- [ ] Durable threshold events, signed notification delivery and target enforcement state.
- [ ] Warm/cold traffic, workers/jobs/workflows/trigger/floor/recovery enforcement and independent holds.
- [ ] Atomic strict compute/allowance reservations, attempt fencing, settlement and local execution deadlines.
- [ ] CLI commands/help/completion/reference and journeys.
- [ ] Customer console: cost breakdown, forecast, policy preview/editor, action history and recovery.
- [ ] Customer docs, registry entries, staged rollout and rollback procedures.
- [ ] Unit/property, Postgres, contract, CLI/SDK, console and native KVM/leakcheck acceptance.

## Worktrees

Platform: `understand-gregale-latest-20261002`, branch `codex/financial-controls-20261002`.

Customer console: `faas-web-latest-20261001`, branch `codex/financial-controls-20261002`.

Existing dirty primary checkouts are not part of this implementation.

## Verified implementation checkpoint — 2026-10-03

Completed portions:

- [x] Retained compute/interface-egress deltas committed with canonical usage, stable read heads, historical price activations and deleted-workload identity.
- [x] Append-only negative corrections with original-source lineage, immutable terms/identity, bounded concurrent credits, audit metadata and idempotent replay.
- [x] Exact integer allocation across price versions, one maximum recorded period allowance, run-rate quantity forecasts and explicit incomplete coverage.
- [x] Read-only costs/forecast APIs, Go client, OpenAPI, Node/Python generated SDKs and runtime contract tests.
- [x] Financial policy semantics and account-scoped persisted revisions, ownership/action validation, optimistic concurrency, policy-cap serialization and atomic audit.
- [x] Read-only budget preview API and CLI command with affected/continuing targets and enforcement readiness.
- [x] Customer cost panel with retained attribution, UTC month selection, explicit forecast gaps and exact money formatting.
- [x] Public budget draft CRUD and immutable paged audit; admin writes and usage reads; bounded retry keys, stable creation identity and optimistic revisions.
- [x] Activation refusal before owner acceptance; financial reads and policy recovery remain reachable during payment suspension without clearing account status.
- [x] CLI budget list/get/create/update/delete/history, generated SDK contracts, console live scope pickers, exact EUR editor, consequences and revision history.

Evidence: the separate `pkg/state/financialtest` suite runs against isolated,
fully migrated PostgreSQL as well as memory. It covers transaction rollback,
commit-order paging, replay, price activation, aggregation, sampling coverage,
correction lineage/concurrency, immutable history, account deletion, policy
ownership/revisions/capacity, and audit failure rollback. Financial service,
OpenAPI AST/lint, Node/Python SDK and customer console checks also run locally.
The final customer console suite passed all 151 files and 1,173 tests, and its
production build prerendered 30 routes. API/CLI tests, OpenAPI parity, generated
Node/Python SDK runtime tests and race checks for the financial domain, both
stores, billing services and metering also passed.

Local verification uses a private Go cache and bounded builds. Repeated disk
exhaustion from concurrent workspace builds led to compact debug output and a
separately runnable financial store suite; these are test execution choices,
not weaker financial assertions. Native KVM/leakcheck has not been run for future
stopping paths.

Still required: remaining bill components and invoice reconciliation; explicit
period/history APIs and activity/floor/run attribution; overlapping-policy precedence and runtime
activation; monitored decisions, outbox delivery and independent holds; all
scheduler/gateway/worker/workflow paths; atomic strict compute and allowance
leases with local deadlines; console action history and workload recovery; rollout,
rollback and native lifecycle acceptance. Enforcement currently reports
unavailable, and the full objective remains incomplete.
