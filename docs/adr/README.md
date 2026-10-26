# Architecture Decision Records

- [ADR-20261026181418: Each runtime base generation has its own shared keys, so releases N and N-1 never overwrite each other's](20261026181418-generation-addressed-runtime-base-keys.md) — proposed; the node cache routes a gated base's shared reads and writes to `base/generations/<sha256(ref)>/…`, vmmd adopts its generation's keys first and the legacy logical key only while it names that generation, a `legacy` verdict is republished by the pre-stage; amends ADR-20261024085817 and ADR-20261024191249 (production-us 2026-10-26, rc.296 fsn-2/fsn-3)
- [ADR-20261026120843: The spend cap stops workloads on every live meter, not just compute](20261026120843-spend-cap-holds-include-live-meters.md) — accepted; meterd records an account whose compute plus live egress overage reached its cap in `account_spend_cap_holds`, schedd's overage gate honours the hold while its month and cap match, and the quota ladder no longer skips capped accounts; amends ADR-1162
- [ADR-20261026151530: Organization-owned projects get Compose postgres as an organization database](20261026151530-compose-postgres-org-projects.md) — accepted; the project's organization ownership with `org.manage_postgres` + `org.bind_postgres` and `postgres:manage`, checked unaudited at plan (reason `permission`) and audited at apply; amends ADR-20261025111824
- [ADR-20261026152009: Management MCP toolsets and a coding-agent eval](20261026152009-mcp-toolsets-and-agent-eval.md) — accepted; `gregale mcp serve --tools core|release|diagnose|all` (core = 18 tools, 11.5 KB vs 54.9 KB; every tool in exactly one toolset, core held to 16 KB), plugin and docs use core; `scripts/agent-eval` runs Claude Code/Codex headless on deploy/fix/rollback/diagnose tasks with a dedicated evaluation account
- [ADR-20261026141841: Local management MCP deploys a project directory and changes environment variables through the CLI](20261026141841-management-mcp-local-deploy-and-env.md) — accepted; local stdio connections only: `gregale_plan/apply_local_deploy` (new apps go live, serving apps stage at 0% for atomic promotion) and `gregale_plan/apply_env_change` (strict secret scan, config-revision guard), run through the serving CLI binary with the connection's credential; amends ADR-1238
- [ADR-20261026113920: Resource journal: volatile commits on tmpfs](20261026113920-resource-journal-volatile-tmpfs.md) — proposed; with vmmd `resource_journal_volatile_writes` on, volatile journal writes replace a tmpfs copy in `/run/faas/vmmd-resources` and skip the disk fsync, durable writes keep the ADR-473 protocol, a same-boot restart reads the copies over the disk records; amends ADR-20261024135625
- [ADR-20261026110240: Coding agents get a docs page, an AGENTS.md in every starter, and a Claude Code plugin](20261026110240-agent-docs-agents-md-and-claude-plugin.md) — accepted; `/docs/agents`, `templates.Materialize` renders one AGENTS.md per starter, `packaging/claude-plugin` (read-only `gregale mcp serve` + deploy/diagnose skills, marketplace in faas-web), and a drift test against the CLI and MCP tool registry
- [ADR-20261026100000: Platform domain and app domains are separate, and app domains are a list](20261026100000-platform-and-app-domains.md) — `platform_domain` (apex/api./operations., emails, webhooks), primary `apps_domain`, `deploy_domain`, and `apps_domain_aliases` that route as their canonical host (`serve` or `redirect`); custom domains accept any owned apex; unchanged until the manifest sets them; amends ADR-480, ADR-570
- [ADR-20261026120412: Developer environments can build one command of a Go module](20261026120412-dev-go-commands.md) — accepted; `go_command` on the developer session API (stored as `build_go_command`, echoed so an older API stops the CLI), `gregale dev --go-command`, and `dev --all` runs each command; amends ADR-20261025134512
- [ADR-20261026105459: `gregale dev --all` members use their service names and get service URLs](20261026105459-dev-all-service-names-and-urls.md) — accepted; a member registers under its production service name (`ui` too: a contract migration widens the run-namespace name check to DNS labels) and gets `GREGALE_SERVICE_<NAME>_*` for its in-group `depends_on`; amends ADR-20261025121530
- [ADR-20261025232118: Workflow dispatch slots give back their database token while a step runs](20261025232118-workflow-dispatch-db-phase-tokens.md) — accepted; up to 32 dispatch slots per scheduler (4 offered every tick, one more while all are busy) wait on step handlers, while 4 tokens bound their database work; amends ADR-649
- [ADR-20261024225612: Firecracker CPU templates and CPU-compatible snapshot restore](20261024225612-firecracker-cpu-templates.md) — proposed; vmmd `cpu_template` (T2CL for Cascade Lake + Ice Lake), nodes and snapshots record their CPU identity, a snapshot restores only on a compatible node (amends ADR-005); amendment 1: one live snapshot per tier per CPU platform, a new platform captures its own
- [Customer bucket CORS on the branded S3 endpoint](20261025202805-object-storage-bucket-cors.md) — per-bucket S3-shaped CORS rules set through apid (preview `bucket-cors`); the gateway applies them to preflights that carry a presigned URL or upload grant and to the actual requests
- [Per-key object deletion fences](20261025115958-object-storage-per-key-deletion.md) — one active deletion per key, not per bucket; writes are fenced only by a deletion of their key; deletions yield to inventory; bulk deletes run eight entries at once (amends ADR-547)
- [ADR-20261025131406: One tenant's deployment failures do not degrade public status](20261025131406-status-deployment-breach-needs-two-accounts.md) — proposed; deployment success degrades the status page only when platform-attributed failures span two or more accounts in the 24 h window; the percentage is unchanged (Hunt 14 H14-22; amends ADR-130)
- [ADR-20261025121530: `gregale dev --all` runs its apps in one developer namespace](20261025121530-dev-all-namespace.md) — accepted; one group workspace, each child registers in the `gregale test` run namespace, so `<name>.internal` and `<name>.svc.gregale` reach the group and never production; source-built Compose services join `dev --all`
- [ADR-20261025094915: Managed PostgreSQL compute caps hold on running databases](20261025094915-managed-postgres-compute-caps-on-running-databases.md) — proposed; Free suspends at its allowance, paid plans suspend at it unless the customer turns overage on (then at 2×); Neon project quotas, never a deletion (amends ADR-1430, ADR-155)
- [Each Compose include interpolates with its own .env](20261025090000-compose-include-env-files.md)
- [Image runs read registry credentials from a Docker config](20261025110000-image-run-docker-config.md)
- [ADR-20261025092617: The curl installer resolves its version from a release-published pointer, not the GitHub API](20261025092617-cli-install-channel-pointer.md) — accepted; `release.yml` keeps forward-only `CLI-STABLE`/`CLI-LATEST` pointers on a `cli-channel` release, read through `releases/download` (no anonymous API limit), with the API path as fallback; amends ADR-172
- [Manifest-managed S3 gateway and restore reconciliation](20261025111015-object-storage-managed-gateway-and-restore-reconcile.md) — `object_storage.gateway: managed` in the deploy manifest, strict verify fails on an unmanaged gateway, read-only `gregalectl object-storage reconcile` after a PostgreSQL restore
- [ADR-20261025090100: The host kernel never mounts a drive a guest has written](20261025090100-host-never-mounts-guest-drives.md) — restore staging, fork scrub and build export read and edit VM drives with e2fsprogs in the vmmd-guestdisk sandbox (jail uid, private namespaces, Landlock, rlimits) after an `e2fsck` journal replay; no ext4 or loop mount remains in pkg/fcvm; amends ADR-192 and ADR-568
- [Every capability behind a platform flag](20261023115741-every-capability-behind-a-platform-flag.md)
- [Per-account realtime preview rollout and realtime apps declared in gregale.yaml](20261019102657-realtime-preview-rollout-and-declared-apps.md)
- [Gateway-metered object storage billing](20261024132844-gateway-metered-object-storage-billing.md) — gateway safety meters stored byte-hours from inventory; storage-only rate card bills ADR-1430's $0.03/GiB-month
- [ADR-20261024191249: A rolled node publishes every runtime base its release configures, and is not activated until its vmmd can adopt them](20261024191249-rollout-publishes-release-bases-and-gates-activation.md) — proposed; the pre-stage publishes each configured base this node could not adopt (whoever owns its apps), a rebuild's scan refusal names no generation, `vmmd base-adoption-check` keeps a node drained until it can boot every base; amends ADR-20261024085817 (production-us 2026-10-24, rc.293 fsn-2)
- [ADR-20261024140100: Every deployment reaches a terminal state](20261024140100-deployment-watchdog.md) — apid's watchdog fails a build queued over 6 h (`deployment_queue_timeout`) or an in-flight deployment with no build and no progress for 3 h (`deployment_stalled`)
- [Build secrets resolve like the runtime environment](20261024140000-build-secrets-resolve-like-the-runtime.md)
- [ADR-20261024135625: Resource journal: durable commits only for reboot-surviving assets](20261024135625-resource-journal-volatile-commits.md)
- [Dockerfile HEALTHCHECK gates readiness on source builds](20261024100000-dockerfile-healthcheck-enforced.md)
- [ADR-20261024132405: Language runtimes are supported until upstream end of life; their CVEs are triaged, not migrated away](20261024132405-runtime-language-version-support-and-cves.md) — accepted; deprecation dates, weekly base-digest refresh, version-pinned scanner exceptions; amends ADR 20261023220532 decision 4 for Python 3.12
- [ADR-20261024090000: Idle parks re-check activity, and a park holds new forwards](20261024090000-idle-park-recheck-and-forward-hold.md) — the reaper's park re-checks fresh activity before SNAPSHOTTING; vmmd holds new forwards for the park and drains in-flight ones up to 5 s (amends spec §6)
- [ADR-20261022083601: AI crawler controls](20261022083601-edge-ai-crawler-controls.md)
- [ADR-20261024085817: A compute rollout never strands a node on a runtime base it cannot boot, and its drain finishes](20261024085817-rollout-base-generation-and-drain-completion.md) — proposed; a changed base generation pre-stages behind the drain barrier, the rescue never re-activates a node whose cache moved, a drain withdraws, drains and stops what it cannot hand off (2026-10-22 fsn-3)
- [ADR-20261021220456: Attack mode](20261021220456-edge-attack-mode.md)
- [Bucket-scoped object storage availability](20261023223000-object-storage-bucket-scoped-availability.md) — per-bucket inventory freshness, maintenance listings off the customer meter, no fence on a never-versioned baseline, busy deletions wait, public-read share and revalidation
- [ADR-20261024082000: Release gate for edge security features](20261024082000-edge-security-ga-gate.md)
- [ADR-20261023171905: Unbilled quiet pause — bill the seconds a VM runs, not the seconds it waits](20261023171905-unbilled-quiet-pause.md) — schedd `quiet_pause_ledger` + meterd `quiet_pause_unbilled` (amends §4.7, ADR-1151 decision 1, ADR-20261021215353)
- [ADR-20261023214253: Operators need MFA, and a session to read customer data](20261023214253-operators-need-mfa-and-sessions-for-customer-data.md) — proposed; operator sign-ins start mfa_pending; operator keys read fleet telemetry only (FAAS_OPERATOR_BEARER_READS grace)
- [Compose profiles from COMPOSE_PROFILES in .env](20261022190000-compose-profiles-from-dotenv.md)
- [Compose deploy resources and replicas set app settings](20261023120000-compose-deploy-resources.md)
- [Compose service secrets as files in /run/secrets](20261023090000-compose-runtime-secret-files.md)
- [ADR-20261023113721: Platform flags for edge rule features](20261023113721-edge-feature-platform-flags.md)
- [ADR-20261022203523: Per-instance request concurrency](20261022203523-per-instance-request-concurrency.md) — proposed; `scaling.per_instance_concurrency` caps the gateway's per-VM gate and drives scale-out (H12-4)
- [ADR-20261022204036: An Idempotency-Key is bound to its request](20261022204036-idempotency-key-bound-to-request.md) — another request with a used key gets `422 idempotency_key_reused`, not a replay (Proposed)
- [ADR-20261022203653: One dead-letter entry per queue message](20261022203653-one-dead-letter-entry-per-queue-message.md) — Proposed; amends ADR-179: a push receipt is captured but not listed
- [ADR-20261022105846: Penalty box for edge rules](20261022105846-edge-rule-penalty-box.md)
- [Compose build secrets for Dockerfile builds](20261022185329-compose-build-secrets.md)
- [ADR-20261023173624: Runtime secrets are bound to their account](20261023173624-runtime-secrets-bound-to-their-account.md) — vmmd opens a bound envelope only for its account; writers opt in with FAAS_RUNTIME_SECRET_BINDING
- [ADR-20261023104734: Daemons log in as runtime roles, not the schema owner](20261023104734-database-runtime-roles.md) — faas_runtime group, per-daemon roles, opt-in FAAS_DB_ROLE
- [Product Control trial-to-paid conversion](20261022180921-product-control-trial-conversion.md)
- [Native runtime snapshot capture for park and warm](20261022184951-native-runtime-snapshot-capture.md)
- [ADR-20261022184531: Wake failover away from a node that is up but not ready](20261022184531-node-wake-failover.md)
- [ADR-20261022155043: Rehearsal evidence follows the run](20261022155043-rehearsal-evidence-follows-the-run.md)
- [Compose process overrides on source-built services](20261022160000-compose-process-on-built-services.md)
- [ADR-20261024014500: Honeytokens and unseal telemetry](20261024014500-honeytokens-and-unseal-telemetry.md)
- [ADR-20261022140000: Break-glass controls for a compromised secret store](20261022140000-break-glass-controls.md)
- [ADR-20261022111812: Seal domains: a sealed value opens only where it is read](20261022111812-seal-domains-per-reader.md)
- [Product Control self-serve signup and card on file](20261022100735-product-control-self-serve-signup.md)
- [ADR-20261022082721: Rehearsal reads of captured work, inbox and async invoke](20261022082721-rehearsal-captured-reads-inbox-async-invoke.md)
- [gatewayd-internal private control listener for apid](20261022004500-gatewayd-private-control-listener.md)
- [ADR-20261021223225: Path routes in gregale.yaml](20261021223225-manifest-routes.md) — manifest-owned kind=route (+ rewrite for strip_prefix) edge rules on the target app; rewrite to the root strips cleanly
- [ADR-20261021215353: Early freeze — stop billing shortly after the last request](20261021215353-early-freeze.md) — schedd `frozen_tier_early_freeze_after_s` (amends ADR-1151)
- [ADR-20261021204307: Code-first workflows](20261021204307-code-first-workflows.md) — one entry handler replays the run's journal; Node, Python and Go SDKs
- [Platform flags: company targeting, full coverage, scheduled and automatic rollouts, self-serve previews](20261021080640-platform-flag-extensions.md)
- [Product Control free trials](20261021195901-product-control-free-trials.md)
- [ADR-20261022193512: Unified incidents](20261022193512-unified-incidents.md)
- [ADR-20261022173833: Custom dashboards](20261022173833-custom-dashboards.md)
- [ADR-20261022172052: Alerts carry what the logs show](20261022172052-alert-log-context.md)
- [ADR-20261021210000: Enrolled apps receive no account events](20261021210000-rehearsal-event-fanout-exclusion.md)
- [Data API engine pool size and server-key limits](20261021180534-data-api-engine-pool-and-server-key-limits.md)
- [ADR-20261021180000: Rehearsal capture of realtime publishes and signals](20261021180000-rehearsal-realtime-capture.md)
- [ADR-20261021150000: Rehearsal capture of workflow starts and events](20261021150000-rehearsal-workflow-capture.md)
- [ADR-20261021120000: Rehearsal capture of public-API event publishes](20261021120000-rehearsal-public-api-event-capture.md)
- [ADR-20261021090000: Rehearsal queue capture](20261021090000-rehearsal-queue-capture.md)
- [ADR-20261020200000: Rehearsal crons fire from the recipe, not the clock](20261020200000-rehearsal-crons-fire-from-the-recipe.md)
- [ADR-20261020180000: Rehearsal PostgreSQL schema inventory](20261020180000-rehearsal-postgres-schema-inventory.md)
- [ADR-20261020150000: Contained rehearsals run the candidate's release command](20261020150000-rehearsal-contained-release-commands.md)
- [ADR-20261020090000: Rehearsal event capture](20261020090000-rehearsal-event-capture.md)
- [ADR-20261019180000: Rehearsal side effects stay inside the run](20261019180000-rehearsal-side-effects-stay-inside-the-run.md)
- [ADR-20261019120000: Rehearsal runtime hooks stay off ordinary hot paths](20261019120000-rehearsal-runtime-hot-path-isolation.md)
- [Source archives are capped by their expanded size](20261020160500-source-archive-expanded-size-cap.md)
- [Guests reach only the bridge services and the public edge on the host](20261020123000-tenant-bridge-host-input-narrowed.md)
- [DNS proof takes over another account's pending hostname claim](20261020090500-dns-proof-takes-over-pending-claims.md)
- [Compose healthchecks on source-built services](20261019195000-compose-healthcheck-on-built-services.md)
- [Product Control plan fees, included starts and overage](20261021154511-product-control-plan-fees-and-overage.md)
- [Data API file storage](20261021101510-data-api-file-storage.md)
- [Keep the edge lane off the store type: edge store](20261021092138-edge-store-blast-radius.md)
- [Product Control billing records survive app purge](20261020090607-product-control-billing-records-survive-app-purge.md)
- [Product Control customers keep their billing details in the owner's Paddle](20261020120000-product-control-customer-billing-details.md)
- [Safety rails for automatic per-unit deploys](20261020214632-automatic-unit-deploy-safety-rails.md)
- [Push-bound workers may listen for their deliveries](20261021153953-push-bound-worker-delivery-listener.md)
- [What a fix restarts: daemon blast radius](20261020160744-daemon-blast-radius.md)
- [Object storage in budgets](20261020160000-object-storage-in-budgets.md)
- [Budget alerts on notification channels, and projected overruns](20261019193500-budget-alert-channels.md)
- [Finished invocations without a horizon get the plan default retention](20261019104819-invocation-default-retention.md)
- [Resolve Compose extends and include](20261019190527-compose-extends-and-include.md)

- [Compose build arguments and target stage for Dockerfile builds](20261019142544-compose-build-args-and-target.md)

- [Data API usage reporting and owner monthly caps](20261019102108-data-api-usage-and-owner-caps.md)

- [Data API public reads, server keys, aggregates, single-app projects and Python/Go clients](20261018170742-data-api-access-modes-and-clients.md)

- [Tenant egress denies the NAT64 prefixes](20261018194214-tenant-egress-deny-nat64.md)
- [ADR-1745: Shared-window compound Issues alerts](1745-shared-window-issue-compound-alerts.md)
- [ADR-1744: Offline grouping previews and reviewed regression baselines](1744-issue-grouping-previews-and-baselines.md)
- [ADR-1743: Publish final Node build source maps with build completion](1743-builder-issue-source-map-publication.md)
- [ADR-1742: Publish prebuilt Issues source maps during CLI deployment](1742-deploy-issue-source-map-publication.md)
- [Customer log search and analytics over the unified ledger](20261018083536-customer-log-search.md)

- [Preserve request identity in response caching](20261017214848-response-cache-request-identity.md)

- [Restart-safe Go acknowledgment recovery alerts](20261017182028-go-recovery-alert-checkpoints.md)

- [Bounded Go acknowledgment intent recovery passes](20261017181200-bounded-go-intent-recovery.md)

- [Bounded Node acknowledgment intent recovery passes](20261017171806-bounded-node-intent-recovery.md)

- [Bounded Python acknowledgment intent recovery passes](20261017170311-bounded-python-intent-recovery.md)

- [Shared Node and Python preparation monitoring](20261017165449-shared-preparation-monitoring.md)

- [Prometheus exposition for Python preparation metrics](20261017164707-python-preparation-prometheus.md)

- [Bounded Python preparation metrics](20261017164105-python-preparation-metrics.md)

- [Python preparation lifecycle telemetry](20261017161548-python-preparation-lifecycle-telemetry.md)

- [Python processing recovery inspection](20261017155904-python-processing-recovery-inspection.md)

- [Coordinated Python event preparation](20261017154707-coordinated-python-event-preparation.md)

- [Python consumer processing leases](20261017152929-python-consumer-processing-leases.md)

- [Python recovery alert checkpoints](20261017145402-python-recovery-alert-checkpoints.md)

- [Python sustained acknowledgment recovery alerts](20261017144446-python-sustained-acknowledgment-recovery-alerts.md)

- [Python acknowledgment recovery health](20261017143147-python-acknowledgment-recovery-health.md)

- [Python durable retry and dead-letter intents](20261017142209-python-durable-negative-acknowledgments.md)

- [Python durable acknowledgment intents](20261017140016-python-durable-acknowledgment-intents.md)

- [Python async event acknowledgment worker](20261017131506-python-async-event-acknowledgment-worker.md)

- [Opt-in Node preparation monitoring](20261017125950-node-preparation-monitoring.md)

- [Node preparation Prometheus exposition](20261017124511-node-preparation-prometheus.md)

- [Bounded Node preparation metrics](20261017122416-node-preparation-metrics.md)

- [Node preparation lifecycle telemetry](20261017121310-node-preparation-lifecycle-telemetry.md)

- [Node processing recovery inspection](20261017115623-node-processing-recovery-inspection.md)

- [Coordinated Node event preparation](20261017114800-coordinated-node-event-preparation.md)

- [Node consumer processing leases](20261017114009-node-consumer-processing-leases.md)

- [Restart-safe Node recovery alert checkpoints](20261017112428-node-recovery-alert-checkpoints.md)

- [Node sustained acknowledgment recovery alerts](20261017111119-node-acknowledgment-recovery-watch.md)

- [Node acknowledgment intent recovery health](20261017110351-node-acknowledgment-intent-health.md)

- [Durable Node acknowledgment intents](20261017105545-node-durable-acknowledgment-intents.md)

- [Node deferred event acknowledgment worker](20261017-node-event-acknowledgment-worker.md)
- [ADR-20261019120046: Preserve configuration when deployment acceptance is uncertain](20261019120046-deploy-submission-outcomes.md) — explicit submission outcomes and commit uncertainty across processes

- [ADR-20261019120045: Deploy upload recovery across CLI processes](20261019120045-deploy-upload-recovery.md) — content and intent checkpoints, interruption recovery, and expired-session replacement keys

ADR-001 through ADR-010 are **accepted and locked for v1**; they live inline in
[`../faas_implementation_spec.md`](../faas_implementation_spec.md) §3, not as
separate files here. This directory holds ADRs made *after* the spec.

Any deviation from the spec requires a new ADR here first (spec §3, CLAUDE.md).

The default-off internal static-hosting lifecycle, quotas, and Cloudflare/R2
delivery boundary are recorded in
[ADR-20261015](20261015-internal-static-hosting.md).

Durable internal static routing, conditional publication, and acknowledgement-based
retirement are specified by [ADR-202610151515](202610151515-durable-static-hosting.md).

## Picking a number

ADR numbers are hand-picked, so two concurrent PRs routinely claim the same one
and whichever merges second keeps it. The renumber trail through the table
below ("renumbered 066→067→068→069", "through 6 hops") is what that costs.

`make adr-number-uniqueness-check` (also part of `make lint` and CI) fails on
any **newly** duplicated number. The 72 numbers already duplicated on `main` are
frozen in [`DUPLICATE_NUMBERS_BASELINE.txt`](DUPLICATE_NUMBERS_BASELINE.txt);
the gate holds that set and stops it growing. Do not add a line for a collision
introduced by this PR; refresh the baseline from `main` when `main` independently
gains duplicate ADR numbers.

Before claiming a number, check both the directory **and** open PRs — a PR can
claim a number between your check and your merge:

```bash
ls docs/adr/
gh pr list --state open --limit 80 --json title \
  --jq '.[] | select(.title|test("ADR-")) | .title'
```

Re-check after any rebase. If you do have to renumber, scope the rename to the
references *your branch* introduced: shared files (`api/openapi.yaml`,
`pkg/api/dto.go`, `pkg/api/limits.go`) document many ADRs at once, so a blanket
`sed` silently rewrites other people's.

Retro-fixing the existing duplicates is deliberately out of scope for the gate.
The number is embedded in `// adr: NNN` citation lines that
`scripts/ci/check_spec_cited_tests.sh` reads, plus metric help strings and
runbooks — ADR-190 alone had 67 references. Renumber one ADR per PR, and delete
its baseline line in the same change (the gate fails on a stale entry).

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
| 20261022105723 | [Worker plan decisions: queue backlog and retention, CPU pricing, worker sizes](20261022105723-worker-queue-limits-cpu-pricing-sizes.md) | accepted | Queue backlogs grow to 10k / 100k / 1M messages per app with a 100 MiB / 1 GiB / 10 GiB account byte cap and 1 / 4 / 14-day expiry; CPU stays bundled into GB-RAM-hours; workers keep plan sizes |
| 20261019103000 | [Frozen worker pools](20261019103000-frozen-worker-pools.md) | accepted; behind `frozen_tier_workers` | A pool scaling in freezes a surplus worker that holds no lease and thaws it before booting new ones; a frozen worker stops instead of parking and gives up its account slot when another pool needs it |
| 20261018101500 | [Worker pools scale out off the scheduler loop, with brief characterization for proven workers](20261018101500-worker-pool-scale-out-off-loop.md) | accepted | The targets trigger reconciles worker apps in background runs, one per account at a time; a declared worker whose deployment passed the full window on the node boots with `faas.worker=proven` and is characterized in 1 s |
| 1728 | [Owner-authorized deployment response checks](1728-owner-authorized-deployment-response-checks.md) | accepted; native deployment validation pending | Exact-deployment GET checks through the trusted gateway, with bounded HTTP evidence and no forwarded account credentials |
| 2026101709 | [Receipt-bound repairable operation transactions](2026101709-repairable-operation-transactions.md) | accepted for internal qualification | Go, Node and Python capture, business writes and receipt bindings commit together; replay survives payload compaction |
| 2026101708 | [Application writer capture readiness](2026101708-repair-capture-readiness.md) | accepted for internal qualification | Read-only checks on the actual application writer connection |
| 2026101707 | [Read-only selective repair readiness](2026101707-repair-readiness.md) | accepted for internal qualification | Bounded executor catalog, enrollment and policy diagnostics |
| 2026101706 | [Source-backed repair history](2026101706-repair-history-discovery.md) | accepted for internal qualification | Scoped discovery and recovery after payload compaction |
| 2026101705 | [Read-only verification of committed repairs](2026101705-repair-post-apply-verification.md) | accepted for internal qualification | Compare present rows with the committed repair while preserving its receipt |
| 2026101704 | [Compaction of superseded repair plans](2026101704-repair-superseded-retention.md) | accepted for internal qualification | Preserve replacement lineage and pin active child evidence |
| 2026101703 | [Authoritative repair apply resolution](2026101703-repair-apply-resolution.md) | accepted for internal qualification | Resolve uncertain outcomes through receipts or durable source closure |
| 2026101702 | [Linked replacement repair plans](2026101702-repair-replacement.md) | accepted for internal qualification | Narrow original candidates with parent fencing and fresh approval |
| 2026101701 | [Reviewed retention of repair payloads](2026101701-repair-retention.md) | accepted for internal qualification | Digest-reviewed compaction retains identities, receipts and closure |
| 2026101710 | [Daemon-bound repair producers](2026101710-repair-producer-retirement.md) | accepted; native execution pending | Physical producer retirement fences cleanup and capacity release |
| 1580 | [Zero-downtime daemon restarts](1580-zero-downtime-daemon-restarts.md) | accepted | Opt-in durable listeners, bounded draining and restart-aware routing |
| 2026101501 | [Internal mail service](2026101501-internal-mail-service.md) | accepted for internal experiment; customer delivery disabled | Independent activation and delivery gates |
| 2026101601 | [Durable mail producer outbox](2026101601-mail-producer-outbox.md) | accepted for internal experiment; activation disabled | Durable account-deletion producer handoff |
| 20261016 | [Selective data repair](20261016-selective-data-repair.md) | accepted for internal qualification | Explicit PostgreSQL field-update capture, isolated rehearsal and atomic approved repair; limits in `pkg/api/limits.go` |
| 1576 | [Internal CI control plane with compute admission closed](1576-internal-ci-control-plane.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1571 | [Durable CI execution engine qualification without compute dispatch](1571-ci-execution-engine-qualification.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1572 | [Account-owned GitHub sources for blocked CI admission](1572-internal-ci-github-sources.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1573 | [Dormant CI adapter to Gregale Jobs](1573-dormant-ci-jobs-adapter.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1574 | [CI guest runner and task-scoped transfers](1574-ci-guest-runner-and-scoped-transfers.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1651 | [Disabled CI scheduler boundary and incomplete-output recovery](1651-disabled-ci-scheduler-boundary.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1652 | [Disabled CI HTTPS transport, private storage and retention](1652-ci-private-storage-and-retention.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1653 | [Opt-in CI native acceptance harness](1653-ci-native-acceptance-harness.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1654 | [Disabled internal CI execution service](1654-internal-ci-execution-service.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1655 | [Internal CI execution profiles](1655-internal-ci-execution-profiles.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1656 | [Default-off GitHub push automation for internal CI](1656-internal-ci-github-automation.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1657 | [Default-off GitHub Checks for internal CI executions](1657-internal-ci-execution-checks.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1577 | [Internal CI Check delivery diagnostics and bounded recovery](1577-internal-ci-check-delivery-recovery.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1578 | [Internal CI execution archival and reusable history slots](1578-internal-ci-execution-archival.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1584 | [Explicit internal CI execution reruns](1584-internal-ci-execution-reruns.md) | implemented behind default-off CI gates; native qualification pending | Fresh whole-pipeline executions with frozen configuration, parent/root lineage, idempotency and confirmed cleanup |
| 1585 | [Internal CI automation path filters](1585-internal-ci-automation-path-filters.md) | implemented behind default-off CI gates; native qualification pending | Pinned bounded comparisons, conservative run fallback, recorded skips and source-only Checks |
| 1586 | [Internal CI skipped-source Check recovery](1586-internal-ci-source-check-recovery.md) | implemented behind default-off CI gates; native qualification pending | Scoped delivery diagnostics and bounded durable feedback retries that preserve skipped sources |
| 1587 | [Bounded internal CI live progress and log following](1587-internal-ci-live-progress.md) | implemented behind default-off CI gates; native qualification pending | Attempt-scoped progress checkpoints, step timings and resumable log following with sealed native completion |
| 1588 | [Explicit internal CI dependency artifact inputs](1588-internal-ci-dependency-artifact-inputs.md) | implemented behind default-off CI gates; native qualification pending | Named dependency artifacts pinned to sealed producer attempts, verified guest downloads and execution-scoped retention |
| 1589 | [Explicit internal CI environment and working directories](1589-internal-ci-step-configuration.md) | implemented behind default-off CI gates; native qualification pending | Frozen literal job/step environments, reserved runner settings and descriptor-bound checkout directories |
| 1590 | [Bounded named matrices for internal CI](1590-internal-ci-named-matrix-jobs.md) | implemented behind default-off CI gates; native qualification pending | Deterministic concrete variants, exact image/template binding, dependency barriers and explicit producer inputs |
| 1701 | [Internal Product Control above Operations](1701-product-control.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1702 | [Product Control deployment bindings and admission evidence](1702-product-control-deployment-bindings-and-evidence.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1703 | [Product Control customer identity, consent and browser recovery](1703-product-control-customer-portal-and-consent.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1704 | [Product Control billing review, finalization and invoice handoff](1704-product-control-billing-lifecycle.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1705 | [Internal Product Control owner console](1705-product-control-owner-console.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1706 | [Product Control guided setup and observed readiness](1706-product-control-guided-setup-and-readiness.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1707 | [Product Control guided customer onboarding](1707-product-control-guided-customer-onboarding.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1708 | [Product Control customer permissions](1708-product-control-customer-permissions.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1709 | [Product Control self-service plans](1709-product-control-self-service-plans.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1710 | [Product Control customer teams](1710-product-control-customer-teams.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1711 | [Verified Application Auth release migrations](1711-verified-application-auth-release-migrations.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1712 | [Product Control customer readiness and activation overview](1712-product-control-customer-readiness-overview.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1713 | [Product Control customer activity timeline](1713-product-control-customer-activity.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1714 | [Product Control customer usage guidance](1714-product-control-customer-usage-guidance.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1715 | [Product Control Customer Help](1715-product-control-customer-help.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1716 | [Product Control internal pilot evidence](1716-product-control-internal-pilot-evidence.md) | accepted for internal implementation; deployed pilot pending | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1717 | [Product Control Customer Notifications](1717-product-control-customer-notifications.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1718 | [Product Control complete internal pilot](1718-product-control-complete-internal-pilot.md) | accepted for internal preparation; deployed pilot pending | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1719 | [Product Control customer account recovery](1719-product-control-customer-account-recovery.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1720 | [Product Control personal notification email](1720-product-control-personal-notification-email.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1721 | [Product Control owner email controls](1721-product-control-owner-email-controls.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1722 | [Product Control email delivery feedback](1722-product-control-email-provider-feedback.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1723 | [Product Control notification recovery](1723-product-control-notification-recovery.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1724 | [Product Control notification unsubscribe](1724-product-control-notification-unsubscribe.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1725 | [Product Control customer workspace activity](1725-product-control-workspace-activity.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1726 | [Product Control customer run details](1726-product-control-customer-run-details.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 1727 | [Product Control customer work history](1727-product-control-customer-work-history.md) | accepted for internal implementation | Internal Product Control; [service contract](../ops/product-control.md), default-off `FAAS_PRODUCT_CONTROL_ENABLED`, limits in `pkg/api/limits.go` |
| 20261018212918 | [Product Control payment collection through owner billing connections](20261018212918-product-control-payment-collection.md) | accepted for internal implementation; not implemented | Product Control statements export to the owner's own Stripe/Paddle/Lago, with payment status, opt-in overdue holds and month-close billing runs; Paddle embedded billing deferred |
| 1583 | [Default-off same-repository PR execution automation](1583-internal-ci-pull-request-automation.md) | implemented behind default-off CI gates; native qualification pending | Explicit PR target filters, fresh source verification, per-PR slots and existing cleanup fences |
| 1582 | [Bounded internal CI JUnit diagnostics](1582-internal-ci-junit-reports.md) | implemented behind default-off CI gates; native qualification pending | Verified attempt-bound reports, bounded failure detail and aggregate-only GitHub Checks |
| 1581 | [Internal CI branch serialization and verified-head supersession](1581-internal-ci-branch-concurrency.md) | implemented behind default-off CI gates; native qualification pending | One automatic branch slot, verified-head opt-in supersession, durable cancellation and cleanup fencing |
| 1592 | [Bounded recovery for internal CI automation admission](1592-internal-ci-automation-admission-recovery.md) | implemented behind default-off CI gates; native qualification pending | Explicit account-scoped recovery of never-admitted authenticated pushes; permanent bounded audit receipts |
| 1579 | [Internal CI health and bounded worker observations](1579-internal-ci-health-observability.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1575 | [Internal CI operational alerts and dashboard](1575-internal-ci-alerts-and-dashboard.md) | accepted for internal implementation; native acceptance pending | Default-off Gregale CI; account-scoped operator admission and separate compute/provider qualification |
| 1289 | [Reusable automation test cases](1289-reusable-automation-test-cases.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1290 | [Guided automation test case editor](1290-guided-automation-test-editor.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1291 | [Single-case live automation verification](1291-single-case-live-automation-verification.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1292 | [Guided live automation assertions](1292-guided-live-automation-assertions.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1293 | [Automatic live verification results](1293-automatic-live-verification-results.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1294 | [CLI live automation verification](1294-cli-live-automation-verification.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1295 | [Resumable CLI live automation verification suites](1295-cli-live-automation-verification-suites.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1296 | [Shared live automation suite history](1296-shared-live-automation-suite-history.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1297 | [Durable dashboard live automation suites](1297-dashboard-live-automation-suite-execution.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1298 | [Read-only live automation suite comparisons](1298-live-automation-suite-comparisons.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1299 | [Read-only CI gates for live automation suites](1299-live-automation-ci-gate.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1300 | [Required coverage for the read-only live automation gate](1300-live-automation-required-coverage-policy.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1301 | [Candidate evidence age in live automation gates](1301-live-automation-evidence-age-policy.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1302 | [Named automation live baselines and attributed promotion](1302-named-automation-live-baselines.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1303 | [Manage named automation baselines in the dashboard](1303-dashboard-automation-baselines.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1304 | [Discover automation baselines and observe retained evidence health](1304-automation-baseline-discovery-health.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1305 | [Audited retirement of named automation baselines](1305-automation-baseline-retirement.md) | accepted | Automation authoring, verification and trust lifecycle |
| 1387 | [Guided webhook automation setup](1387-guided-webhook-automation-setup.md) | accepted | Event starter fields, confirmed scoped ingress binding and safe receipt/run inspection |
| 1287 | [Guided scheduled-report configuration](1287-guided-scheduled-report-configuration.md) | accepted | Report fields, distinct edited/published schedule previews and observed setup checks |
| 1286 | [Dashboard workflow diagnosis and recovery](1286-dashboard-workflow-diagnosis-and-recovery.md) | accepted | Scoped diagnostics and attempt inspection, recovery previews, confirmed idempotent continuation and cancellation |
| 1285 | [Guided automation dashboard](1285-guided-automation-dashboard.md) | accepted | App-owner starter, validation, simulation, checked publishing, and run inspection flow |
| 1540 | [Function cancellation completion and trusted lifecycle context](1540-function-cancellation-cleanup-and-lifecycle-context.md) | accepted | Shared slots remain occupied through cleanup; Node abort signals, Python cancellation events and socket-derived function lifecycle context support bounded database work |
| 20261018005023 | [Negotiated Node post-response execution completion](20261018005023-function-background-completion-protocol.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030610 | [Acknowledged function VM execution lifetime](20261018030610-function-vm-execution-lifetime.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030611 | [Automatic PostgreSQL ownership in Node functions](20261018030611-function-postgres-resource-lifecycle.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030612 | [Recover Node resources after warm capture](20261018030612-function-warm-snapshot-resource-recovery.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030613 | [Recover a function migration source after rollback](20261018030613-function-migration-source-rollback.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030614 | [Admit function migration capture and source recovery](20261018030614-function-migration-admission.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030615 | [Restore and publish a measured function migration destination](20261018030615-function-migration-destination.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 20261018030616 | [Reconstruct measured function handoffs at scheduler startup](20261018030616-function-migration-scheduler-restart.md) | implementation for qualification | Function execution/resource lifecycle and fenced migration; native acceptance pending, production off |
| 202610171901 | [Organization application authorization](202610171901-organization-application-authorization.md) | accepted; local validation | Current membership, operation roles, credential scopes and caller attribution |
| 202610171902 | [Organization offboarding fences](202610171902-organization-offboarding-fences.md) | accepted; local validation | Transaction ordering, credential revocation and stream cancellation |
| 202610171903 | [Organization application quota ownership](202610171903-organization-application-quota-ownership.md) | accepted; local validation | Shared application admission across creator namespaces |
| 202610171904 | [Organization resource lifecycle](202610171904-organization-resource-lifecycle.md) | accepted; native acceptance pending | Stable resource namespaces, permanent billing adoption and creator erasure |
| 202610171905 | [Organization project ownership](202610171905-organization-project-ownership.md) | accepted; native acceptance pending | Projects, environments, releases and protected promotions |
| 202610171906 | [Organization managed PostgreSQL ownership](202610171906-organization-managed-postgres-ownership.md) | accepted; native acceptance pending | Retained databases and binding credentials with company quotas |
| 202610171907 | [Organization object storage ownership](202610171907-organization-object-storage-ownership.md) | accepted; native acceptance pending | Retained buckets and application credentials with revocable human access |
| 202610171908 | [Organization queue ownership](202610171908-organization-queue-ownership.md) | accepted; native acceptance pending | Retained backlogs and worker receipts with company quota reservations |
| 1190 | [Application Auth on a pinned GoTrue server](1190-application-auth.md) | proposed | Ordinary auth app on the customer database, `auth` credential mode, organizations and one SQL authorization function shared by data, storage, realtime and workflows |
| 1385 | [Refresh authorization for retained WebSocket channels](1385-retained-channel-authorization-refresh.md) | accepted for implementation; deployed qualification pending | Share SSE's bounded channel grants with WebSocket history and buffered delivery; remove denied subscriptions and reconnect after callback outages |
| 1332 | [Bind retained realtime to the verified login session](1332-session-bound-retained-realtime.md) | accepted for implementation; deployed qualification pending | Verified session/expiry handoff, periodic WebSocket JWT checks and session-aware Auth channel callbacks |
| 1333 | [Durable managed PostgreSQL activation preflight](1333-managed-postgres-activation-preflight.md) | accepted for catalog inspection; customer activation disabled | Recoverable preflight revisions check restored targets, staged SQL verification and source bindings without publishing credentials |
| 1334 | [Durable cutover source SQL connection fences](1334-managed-postgres-cutover-source-fences.md) | accepted for internal controller plumbing; customer activation disabled | Owned SQL fences share maintenance authority, survive lost replies and retain pins until confirmed cancellation cleanup |
| 1335 | [Source writer inventory and provider isolation evidence](1335-managed-postgres-source-writer-isolation.md) | accepted for internal recovery plumbing; customer activation disabled | Immutable catalog baselines, writer diagnostics and expiring provider evidence fail closed on drift or unsupported guarantees |
| 1337 | [Compose process overrides and unsupported-key reporting](1337-compose-process-overrides-and-key-reporting.md) | accepted | Prebuilt image services honour entrypoint/user/working_dir/stop_signal/stop_grace_period via a frozen `image_command` v2; `env_file` adds variable names; other keys, including unresolved `extends`/`include`, warn |
| 1170 | [Developer watch mode for apps with a build step](1170-developer-watch-mode.md) | proposed | Opt-in `gregale dev --watch`: a builder-made developer image runs the framework's dev server and ADR-740 patches reach its own hot reload |
| 1114 | [Reversible issue group links](1114-reversible-issue-group-links.md) | accepted | Merge fingerprint groups without moving evidence; split an original group back out with its lifetime history |
| 1113 | [Deployment-local issue source maps](1113-deployment-local-issue-source-maps.md) | accepted | Node reporters map bounded local artifacts to original frames while preserving generated stack evidence |
| 1112 | [Verified issue grouping explanations](1112-issue-grouping-explanations.md) | accepted | Retained occurrences expose selected sanitized evidence only when it reproduces the saved group fingerprint |
| 1111 | [Opt-in failure-site issue grouping](1111-issue-failure-site-grouping.md) | accepted | Version 2 groups the nearest application frame with a stable code or normalized message; version 1 identities remain unchanged |

| 992 | [Combined managed PostgreSQL rollout readiness](992-managed-postgres-combined-rollout-readiness.md) | accepted; native execution pending | Offline provider/durable and native guest evidence bound to one clean source revision and independently approved qualification bundle; optional staging enforcement |
| 831 | [Edge WAF signatures, bot challenge, and client certificates](831-edge-waf-bot-challenge-mtls.md) | proposed | Sequenced plan: Coraza + OWASP CRS `kind=waf` first (observe-only preview, latency gate), then a proof-of-work `kind=challenge`, then custom-domain-only mTLS on demand |
| 830 | [Platform security headers on customer app responses](830-customer-response-security-headers.md) | accepted | Customer responses keep app-set security headers; platform fills defaults only when absent, drops `includeSubDomains` on custom domains, and stops forcing `X-Frame-Options`/`Permissions-Policy` on customer apps |
| 829 | [New apps observe the pre-auth source limit by default](829-default-pre-auth-observe.md) | accepted | New apps get an observe-only, plan-clamped per-source guard unless the create request sets `pre_auth_rate_limit`; existing apps unchanged; amendment 1 adds the advisory enforce suggestion and `gregale app --pre-auth` |
| 752 | [One-command Datadog integration](752-datadog-integration.md) | proposed | Agentless logs and deployment events to Datadog with unified service tags |
| 751 | [Application change timeline](751-app-change-timeline.md) | proposed | One read-only timeline of an app's deploys, config changes, incidents and health transitions |
| 749 | [Alert notification channels](749-alert-notification-channels.md) | proposed | Account-level Slack, PagerDuty and email destinations for alert rules, sealed at rest, with test sends |
| 748 | [Synthetic HTTP checks](748-synthetic-checks.md) | proposed | Scheduled checks against an app through the public edge, with uptime, latency and alerts |
| 747 | [Customer-defined SLOs and error budgets](747-custom-slos-error-budgets.md) | proposed | Availability and latency objectives with hourly budget rollups, burn rates and SLO alerts |
| 746 | [Log-level line counts and log alerts](746-log-level-alerts.md) | proposed | vmmd counts error and warn guest log lines per app; alert rules watch them |
| 745 | [Custom metric history, OTLP ingestion, charts, and alerts](745-custom-metric-history.md) | proposed | Extends ADR-202 pushed gauges with Prometheus-backed history and read surfaces |
| 744 | [Baseline-relative ("anomaly") alerts for apps](744-anomaly-alerts.md) | proposed | Alert on a multiple of the app's usual metric value instead of a fixed threshold |
| 743 | [Allocate route compute cost by request time](743-route-cost-by-request-time.md) | proposed | Route cost estimates follow time spent serving, not request count |
| 742 | [WebSocket forwarding through Dev Bridge](742-dev-bridge-websocket-forwarding.md) | accepted for internal Dev Bridge use | Bounded, revocable WebSocket upgrades in both bridge directions; other upgrades, gRPC and raw TCP remain out of scope |
| 741 | [Remote debugger attach for developer environments](741-developer-debugger-attach.md) | proposed | `gregale dev --debug` over a leased edge WebSocket to vmmd `ForwardTCPStream`; developer sessions only |
| 740 | [Developer live source patches](740-developer-live-source-patch.md) | proposed | ~1 s interpreted-source edits via a guest long-poll on the runtime-config vsock; the build still converges |
| 731 | [Durable PostgreSQL lifecycle qualification](731-managed-postgres-durable-qualification.md) | accepted | Version-8 SQL restart, encrypted credential delivery, workload rotation and cleanup evidence |
| 687 | [Object version listing and bound historical downloads](687-object-version-cli-and-bound-downloads.md) | accepted | Public immutable version identities, bounded listings and exact-version gateway read authority |
| 688 | [Resumable CLI object uploads](688-resumable-cli-object-uploads.md) | accepted | Private fingerprint-bound multipart checkpoints and uncertain-completion recovery |
| 819 | [Continuous CPU profiling across guest lifetimes](819-continuous-cpu-profiling.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 820 | [Route-associated CPU profiling](820-route-associated-cpu-profiling.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 821 | [Advisory route CPU regression checks](821-advisory-route-cpu-regression-checks.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 822 | [Route attribution quality reporting](822-route-attribution-quality.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 823 | [Per-route request labeling consistency](823-per-route-request-label-consistency.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 824 | [Native profiling restore qualification](824-native-profile-restore-qualification.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 825 | [Advisory route profiling notifications](825-advisory-profile-route-notifications.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 826 | [Periodic route profiling for running deployments](826-periodic-route-profiling.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 827 | [Route-specific profiling code evidence](827-route-specific-profile-code-evidence.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 828 | [Opt-in profiling gates for canary deployment stages](828-profile-canary-deployment-gates.md) | accepted for internal implementation; native acceptance pending | Continuous profiling and qualified deployment evidence |
| 1155 | [On-demand CPU and heap profiling](1155-on-demand-cpu-and-heap-profiling.md) | accepted for internal implementation; native acceptance pending | Dormant guest collectors armed per instance through a Postgres queue; continuous heap profiles; automatic heap capture on memory pressure |
| 949 | [App consumer statement revisions and unbilled platform failures](949-app-consumer-statement-revisions-and-platform-failure-billing.md) | accepted | Late usage becomes additive app-local statement revisions; gateway-generated 5xx carry zero billable units |
| 950 | [Monthly included units on app consumer rate cards](950-api-consumer-monthly-allowances.md) | accepted | Free requests per consumer per UTC month, consumed in minute order; adjustments bill allowance lost to late usage |
| 951 | [Graduated tiers on app consumer rate cards](951-api-consumer-graduated-tiers.md) | accepted | Per-consumer monthly price ladders with exact re-rating; tiered statements cover whole UTC months |
| 952 | [Route weights on app consumer rate cards](952-api-consumer-route-weights.md) | accepted | Requests on weighted routes count as N units; gateway labels consumer routes and apid keeps route-level minutes |
| 953 | [Named consumer plans](953-api-consumer-plans.md) | accepted | Plans bundle per-minute and monthly limits with their own price history; minute-effective assignments; gateway admission counters |
| 954 | [Consumer usage completeness check](954-api-consumer-usage-completeness.md) | accepted | Read-only hourly comparison of billed successful requests with request telemetry; lower-bound gaps; CLI warning after draft or finalize |
| 958 | [Zero-config in-guest tracing for the debugger](958-zero-config-guest-tracing.md) | accepted for internal implementation; native acceptance pending | Opt-in OTLP bridge in guest-init, preloaded Node/Python auto-instrumentation, and a vmmd-brokered, apid-parsed path into request telemetry spans |
| 849 | [Consumer usage alerts](849-api-consumer-usage-alerts.md) | accepted | Plan alert thresholds as percentages of the monthly unit cap; recorded once per consumer, month and threshold in the gateway admission transaction; consumer.usage_threshold webhook |
| 975 | [Allowances and tiers on platform tenant rate cards](975-platform-tenant-allowances-and-tiers.md) | accepted | Tenant-wide monthly allowances and graduated tiers across apps; calendar-month statements re-priced against finalized lines; app card features only block fallback minutes |
| 990 | [AI gateway phase 2: per-app limits, fallback routes, retries, cache and cost](990-ai-gateway-budgets-fallback-cost.md) | accepted | Per-app token limits, invisible OpenAI stream usage injection, replayable POST bodies for retries/cache/fallback routes, customer price lists with daily/monthly cost budgets, Runs streaming through the relay, `gregale ai` CLI |
| 991 | [AI gateway phase 3: hard caps, guardrails, providers, translation, key pools and visibility](991-ai-gateway-hard-caps-providers-visibility.md) | accepted | Admission-time reservations make token/cost limits hard caps; per-app RPM/TPM, model allow-lists and output ceilings; Gemini, Azure OpenAI, Bedrock (SigV4) and Vertex (service account); OpenAI-format routes translating to Anthropic/Gemini; extra key pools with per-key cooldown; 80/100% budget alert webhooks, opt-in request metadata log, Grafana dashboard; the whole AI gateway ships behind the off-by-default `ai_gateway_enabled` / `FAAS_AI_GATEWAY_ENABLED` release gate |
| 994 | [AI gateway phase 4: price catalog, per-end-user limits and key affinity](994-ai-gateway-price-catalog-end-users-affinity.md) | accepted | Built-in EUR price catalog generated from LiteLLM (MIT) prices models an integration's list omits; `X-Gregale-AI-User` attributes calls and per-end-user UTC-day token/cost hard caps (10,000 tracked users per day, `~other` overflow, 31-day rollups); `X-Gregale-AI-Session` key-pool affinity by rendezvous hash; AI apid endpoints lifted into `pkg/apid/outboundai` |
| 995 | [AI gateway phase 5: structured output, Bedrock/Vertex route targets, embeddings, end-user alerts and export](995-ai-gateway-structured-output-targets-user-alerts.md) | accepted | OpenAI `response_format` maps to Anthropic `output_config.format`, Gemini `responseJsonSchema` and Bedrock `outputConfig.textFormat` (json_object refused where a schema is required); openai routes reach Bedrock Converse (binary stream translated) and Vertex Claude/Gemini under one project/location; openai routes accept embeddings (Gemini, Vertex, OpenAI, Azure; base64 float32); 80/100% per-end-user alerts to the calling app; streamed CSV/NDJSON per-user export; Node/Python relay URL and header helpers; route attempts no longer double-escape ARN paths |
| 996 | [AI gateway phase 6: Responses API routes, forced tool choice on current Claude models, hard-cap gaps and opt-in content capture](996-ai-gateway-responses-api-hard-cap-gaps-content-capture.md) | accepted | openai routes accept the stateless Responses API (passthrough to OpenAI/Azure, translated replies and stream events for other targets); forced `tool_choice` becomes `auto` plus an instruction on Claude models that reject it; billable calls with unreadable usage settle at their reservation; new end users past the day's 10,000 are refused while per-user limits are set; opt-in capture of the first 32 KiB of logged calls' bodies (1,000 per day, 3 days) |
| 997 | [AI gateway phase 7: media metering, content guardrails and replay](997-ai-gateway-media-metering-content-guardrails-replay.md) | accepted | Image, Imagen, transcription and speech calls are billable and reserved per image, audio second or character (5 new media prices, 106 catalog models); usage read from the tail of large JSON responses; per-integration content policy (email, payment card, IBAN, secret: off/redact/block, blocked terms) enforced before forwarding; captures always redacted; replay of a captured call on up to 5 targets through outboundd's worker with usage, cost, latency and output text |
| 998 | [AI gateway preview: plan entitlement, staged account rollout and qualification](998-ai-gateway-preview-rollout.md) | accepted | AI gateway moves to preview on Hobby, Pro and Scale; plan entitlement enforced in apid (402) and outboundd (403); `ai_gateway_accounts` / `FAAS_AI_GATEWAY_ACCOUNTS` limits the gate to listed accounts during the preview; settlement timeout 2 s -> 15 s after the soak dropped usage; acceptance test, failure drills, opt-in soak, alerts, dashboard panels, runbook and customer page |
| 20261020161811 | [AI gateway: metered Gemini and Vertex Live sessions](20261020161811-ai-gateway-gemini-live-sessions.md) | accepted | Amends ADR 20261019072722 after a measured Vertex session: Live usageMetadata is per turn with the turn's context as input, so each model turn is admitted at its first output and settled from its usage; the model comes from the caller's setup message, and messages are read from binary frames too |
| 20261019132139 | [AI gateway: prompt prefixes shared across outboundd replicas](20261019132139-ai-gateway-shared-prompt-prefixes.md) | accepted | Automatic prompt caching marks a prefix once any outboundd has seen it within the cache lifetime: a process that has not asks Postgres, which keeps prefix hashes only, and falls back to its own memory on failure |
| 20261019123822 | [AI gateway prices, limits and costs in US dollars](20261019123822-ai-gateway-usd.md) | accepted | Amends ADR-1430: the AI gateway catalog takes LiteLLM US-dollar prices with no exchange rate, and price lists, cost limits and costs are read in the billing currency; the catalog response gains currency and deprecates usd_per_eur and fx_source |
| 20261019072722 | [AI gateway phase 18: metered realtime sessions](20261019072722-ai-gateway-realtime-sessions.md) | accepted | OpenAI and Azure OpenAI Realtime WebSocket sessions pass through outboundd transparently; each provider response is admitted against the caps and settled from its usage, audio tokens are priced at audio prices, and Gemini and Vertex Live stay unmetered |
| 1440 | [AI gateway phase 17: streaming response cache and cache savings](1440-ai-gateway-stream-cache-savings.md) | accepted | The response cache also stores identical streamed AI responses as they are forwarded and replays them on a hit; hits settle as cached requests with the provider cost they avoided, and settlement records what provider prompt caching saved |
| 1438 | [AI gateway phase 16: tag budget alerts and tag usage export](1438-ai-gateway-tag-alerts-export.md) | accepted | A tag's daily cost limit alerts at 80% and 100% through the budget alert table and webhook, read back from the existing rollup statement, and a day range of usage by tag exports as CSV or NDJSON |
| 1437 | [AI gateway phase 15: automatic prompt caching for Claude](1437-ai-gateway-automatic-prompt-caching.md) | accepted | With auto_prompt_cache on, the gateway adds prompt-cache breakpoints to Claude requests that carry none: after a system prompt or tool list it saw repeat, and after the last message of a continuing conversation, on the Anthropic API, Vertex and Bedrock |
| 1436 | [AI gateway phase 14: usage tags and per-tag cost limits](1436-ai-gateway-usage-tags.md) | accepted | AI calls name up to five key=value tags in X-Gregale-AI-Tags; settlement rolls usage up per tag and UTC day with a bounded number of tags, and exact tags can carry daily cost limits enforced at admission |
| 1435 | [AI gateway phase 13: response guardrails](1435-ai-gateway-response-guardrails.md) | accepted | The content policy's response section redacts or blocks e-mail addresses, cards, IBANs, secrets and blocked terms in model output, and runs the moderation check on it; whole responses are checked before release and streams through a per-channel hold-back window |
| 1434 | [AI gateway phase 12: weighted, latency and cost route strategies](1434-ai-gateway-route-strategies.md) | accepted | A route's strategy picks the target each call tries first: by weight, by lowest recent time to first byte, or by lowest recent cost per call within an optional latency target, from the per-target minute rows read in the existing route query; the rest fall back in route order |
| 1433 | [AI gateway phase 11: price built-in tool fees](1433-ai-gateway-tool-fees.md) | accepted | Web search, file search, code-interpreter container sessions and Gemini search grounding are counted from responses, priced per use from price lists and the catalog, and reserved when a request declares the tool |
| 1432 | [AI gateway phase 10: refuse paid calls the gateway cannot meter, and meter video generation](1432-ai-gateway-unmetered-endpoints.md) | accepted | Writes on an AI integration go through only to metered or known-free endpoints unless the AI policy sets allow_unmetered; Veo video generation reserves and settles its seconds at the price for its resolution |
| 1431 | [AI gateway phase 9: evaluations before a canary, access tokens for callers outside Gregale, and batch APIs](1431-ai-gateway-evaluations-access-tokens-batches.md) | accepted | Captured calls replay on a candidate target and a judge model compares the answers before a canary starts; expiring app tokens let laptops and CI call the gateway on an optional public listener; OpenAI and Anthropic batches reserve at creation and settle actual usage at batch prices |
| 1340 | [AI gateway phase 8: model canary routes, request-log export and moderation guardrails](1340-ai-gateway-model-canaries-log-export-moderation.md) | accepted | A route can send a percentage of calls to a canary target first and rolls it back automatically on worse errors or latency; settled app calls can be exported to the app's log drains through a Postgres outbox; a content policy can check requests with OpenAI moderation or a classifier model on the account's own integration |
| 1120 | [AI gateway throughput: pipelined admission and settlement, keyed settlement retries](1120-ai-gateway-throughput-pipelined-admission-keyed-settlement.md) | accepted | Admission and settlement pipeline their statements, admission reports the breaker and cooldown, unchanged breaker outcomes take no lock, and settlements keyed by the lease are retried; a call drops from 30 to 11 Postgres round trips |
| 989 | [App access to managed outbound integrations through a guest relay](989-app-outbound-relay.md) | accepted | guest-init loopback `GREGALE_OUTBOUND_URL` relays bytes over vsock to vmmd, which adds a relay assertion and splices to an mTLS `outboundd` listener; only outboundd parses HTTP; Anthropic credentials use `x-api-key` |
| 988 | [AI gateway on managed outbound integrations](988-ai-gateway-outbound-integrations.md) | accepted (phase 1) | `ai_provider` integrations stream event-stream responses, extract OpenAI-compatible and Anthropic token usage, keep UTC-day usage per caller and model, and enforce a daily token limit at admission |
| 1300 | [Export usage statements to the customer's own Stripe](1502-stripe-statement-invoice-exports.md) | accepted | Sealed per-account Stripe key; finalized statements become resumable draft invoices with one item per price and a rounding item; export records the handoff |
| 1301 | [Month-end billing runs](1503-month-end-billing-runs.md) | accepted | One request or a monthly schedule drafts, completeness-checks, finalizes and optionally exports every customer's statement for an ended month; leased, resumable worker; tenant-attributed usage bills on the tenant statement |
| 1302 | [Stripe payment status for exported statements, and billing notifications](1504-statement-invoice-payment-status.md) | accepted | Worker polls each non-final exported invoice; unpaid and overdue lists; email when a run needs attention, after scheduled runs, and a daily overdue digest |
| 1303 | [Usage is durable before a monetized response completes](1505-usage-durable-before-response-completes.md) | accepted | The gateway fsyncs a consumer or tenant request's usage before the bytes that complete its response; fails closed when the outbox cannot take it |
| 1304 | [Consumer billing self-service](1506-consumer-billing-self-service.md) | accepted | Consumer keys with the new billing scope read their own month-to-date spend, daily usage and finalized statements with invoice status; billing grants no app access |
| 1305 | [Paddle and Lago statement exports](1507-paddle-and-lago-statement-exports.md) | accepted | Statement exports, billing runs and payment sync target Paddle (draft manual transactions) and Lago (issued one-off invoices) through one provider interface; routes name the provider |
| 1306 | [Consumer prepaid credit and spend caps](1306-consumer-prepaid-credit-and-spend-caps.md) | accepted | Credit grants per consumer are drawn down when statements finalize and exported as a credit line; monthly spend caps after credit are checked each minute and enforced by the gateway with a 402 |
| 1307 | [Platform tenant route weights and plans](1307-platform-tenant-route-weights-and-plans.md) | accepted | Tenant rate cards weight consumer routes and are month-priced with raw-unit coverage; tenant plans bundle card terms and a request budget that assignment applies from the next minute |
| 1308 | [Consumer compute and response-size pricing](1308-consumer-compute-and-response-size-pricing.md) | accepted | Usage minutes sum billable requests' compute time and response bytes; app cards with unit compute_second or response_megabyte price them rounded per consumer minute |
| 1309 | [Volume rate card tiers](1309-volume-rate-card-tiers.md) | accepted | App and tenant ladders take tier_mode volume, pricing every unit of a month at the step its total reaches; adjustments that would lower a finalized month return 409 |
| 1310 | [Platform tenant compute pricing and surface route weights](1310-platform-tenant-resource-pricing-and-source-routes.md) | accepted | Tenant cards and plans price compute seconds or response megabytes per subject minute; surface and JWT-rule requests carry billing routes that tenant route weights price |
| 1311 | [Provider collection for exported statement invoices](1311-statement-invoice-collection.md) | accepted | Billing connections opt into collection none, send (Stripe) or charge (Stripe, Lago): Stripe finalizes with auto_advance after the last item, Lago creates the invoice without skip_psp |
| 1312 | [Finalized volume months keep their step](1312-locked-volume-steps.md) | accepted | Late usage that would move a finalized volume month into a cheaper step is billed at the step finalized revisions billed instead of returning 409 |
| 1313 | [Stripe automatic tax on exported invoices](1313-stripe-automatic-tax-on-exports.md) | accepted | A Stripe billing connection can set automatic_tax, and exports then ask Stripe Tax to add tax; statement totals stay net of tax |
| 1314 | [Free trials for API consumers](1314-api-consumer-trials.md) | accepted | A consumer gets one trial starting at the next minute; trial minutes are priced at zero and consume no included units, and trials never move into the past |
| 1315 | [Block consumers with overdue invoices](1315-block-consumers-with-overdue-invoices.md) | accepted | An app policy blocks consumers whose exported invoice is unpaid N days after its due date; apid recomputes holds every 5 minutes and the gateway answers 402 |
| 1316 | [Monthly base fee and minimum spend](1316-monthly-base-fee-and-minimum-spend.md) | accepted | App cards charge a base fee and a minimum per consumer month; statements carry fee lines and billing runs include idle consumers |
| 1317 | [Several units priced at once](1317-parallel-unit-price-streams.md) | accepted | App cards of different units are parallel price histories; statement buckets and coverage are keyed by minute and unit |
| 1318 | [App-reported usage units](1318-app-reported-usage-units.md) | accepted | Apps report units in a Gregale-Usage header or trailer; apid keeps them per consumer, meter and minute, and app:<meter> cards price them |
| 1319 | [Customer-wide cards price several units, including app-reported ones](1319-platform-tenant-unit-streams-and-app-units.md) | accepted | Platform tenant cards of different units are parallel streams across every subject; tenant surfaces and JWT rules record app-reported units; assigning a plan stops other units |
| 1322 | [Monthly fees and free trials for platform tenants](1322-platform-tenant-fees-and-trials.md) | accepted | Tenant cards and plans charge a base fee and minimum per tenant month; tenant statements and webhooks carry fees; tenants get one free trial |
| 1324 | [Block platform tenants with overdue invoices](1324-block-tenants-with-overdue-invoices.md) | accepted | An account rule holds tenants whose exported invoice is unpaid past its due date; tenant admission answers 402 tenant_invoice_overdue |
| 1325 | [Prorated monthly fees in the first month](1325-prorated-monthly-fees.md) | accepted | App and tenant cards can prorate the base fee and minimum of the month a consumer or tenant is created, by minutes left |
| 20261018090210 | [API monetization behind a release flag](20261018090210-api-monetization-release-flag.md) | accepted | Runtime setting `api_monetization_enabled` (default off) hides its routes before auth, spec paths, CLI verbs, webhook events and starter billing, and pauses its workers and gateway enforcement |
| 20261019100330 | [Zero- and three-decimal statement currencies](20261019100330-statement-currencies.md) | accepted | Statements stay in millicents; invoices, exports and payment syncs use each ISO 4217 currency's minor unit (0–3 decimals), converted to Stripe's own units at the edge |
| 20261019100331 | [Prepaid credit and spend caps for platform tenants](20261019100331-platform-tenant-credit-and-spend-caps.md) | accepted | Tenant credit grants are drawn at finalize; monthly tenant spend caps after credit are checked each minute and enforced through tenant admission with a 402 |
| 20261019100332 | [Prorated monthly fees across card changes and revocation](20261019100332-prorated-fees-across-card-changes.md) | accepted | Prorating cards charge fees and minimums for the share of the month they are in force, and until a revocation unless the month was already finalized |
| 20261019100333 | [Paddle invoices sent by Paddle](20261019100333-paddle-send-collection.md) | accepted | Paddle connections can collect with `send`: exports bill the draft transaction with the customer's newest address so Paddle emails it |
| 20261019180000 | [One export attempt at a time, retargetable refusals, floored minimums and fresh overdue holds](20261019180000-monetization-review-fixes.md) | accepted | Exports claim their statement so concurrent attempts get a 409; a provider refusal before creation can be retried with another customer; finalized minimums fall only by new usage; holds need a payment status read within 24 h |
| 20261020170649 | [API monetization released per account](20261020170649-api-monetization-per-account-release.md) | accepted | The `api_monetization` platform flag releases monetization to chosen accounts beside the all-or-nothing switch; routes 404 after authentication for others, workers and gateways decide per account; `make monetization-sandbox` qualifies the providers |
| 20261020211500 | [Handoff reservations, usage-only credit in spend caps, reprice on change, credit on self-service statements](20261020211500-monetization-hardening.md) | accepted | Exports reserve their handoff before calling the provider; credit that paid fees no longer lifts a spend cap; spend checks reprice only on new usage or every 15 minutes; consumer statements show credit and amount due |
| 20261021143000 | [Customers sign up for an API themselves, with Stripe Checkout](20261021143000-consumer-self-signup.md) | accepted | A hosted page and public API let an app's customers pick a plan, save a card in the owner's Stripe and get a consumer key; claim tokens, per-address and daily limits |
| 20261021183000 | [Customers manage their own account, and owners hear about it by webhook](20261021183000-customer-self-service-and-webhooks.md) | accepted | A billing-scope key lists plans, switches between offered plans, opens Stripe's customer portal or closes the account, as the owner allows; consumer.signed_up, consumer.plan_changed and consumer.closed webhooks |
| 650 | [Schema-generated Data APIs](650-schema-generated-data-api.md) | accepted | Ordinary app lifecycle, schema-restricted bindings, private type export and typed application clients |
| 712 | [Object-storage durable entities](712-object-storage-durable-entities.md) | internal prototype; qualification pending | SQL-free entity state and retry receipts, opt-in alarms and checkpointed cleanup |
| 1446 | [Safe releases by default](1446-safe-releases-by-default.md) | accepted; n2 drills passed | Default balanced canary and 5xx rollback for production releases, bounded low-traffic hold, crash-loop gate, unfenced plain rollback, verified rollback outcomes |
| 1447 | [Explained rollout decisions](1447-explained-rollout-decisions.md) | proposed | The circuit breaker records why each canary stage advanced, is held, or was aborted; shown on the deployment, in `deployment wait --rollout` and `deployment summary`, and in rollout webhooks |
| 1321 | [Evidence-paced default rollouts](1321-evidence-paced-default-rollouts.md) | proposed | The default `adaptive` ladder advances a stage on clean circuit-breaker evidence after one minute instead of its 2-minute timer; the default probe sends 20 per revision per round and each minute's round starts within about 15 s |
| 1448 | [Releases that change the database](1448-releases-that-change-the-database.md) | proposed | A rollback over release commands that ran after its target last served needs acknowledgement; the refusal lists them with managed PostgreSQL restore points; automation never acknowledges; deployments report their release run |
| 20261023181931 | [Rollbacks that restore the database](20261023181931-rollbacks-that-restore-the-database.md) | accepted | `restore_databases` on an acknowledged rollback restores the migrated managed PostgreSQL database to before the undone releases, fences and drains the app, makes the target live and moves the bindings to the copy in one transaction; writes after the restore point stay in the untouched source; only this switch activates a cutover |
| 1449 | [App-chosen rollout probe routes](1449-app-chosen-rollout-probe-routes.md) | proposed | `release_probe_paths` names up to three GET paths the default rollout probe checks instead of `/`; a round splits its 20 probes across them |
| 1450 | [One rollout flag and a live rollout view](1450-one-rollout-flag-and-live-rollout.md) | proposed | `gregale deploy --rollout safe\|fast\|immediate` and `release_policy` safe/fast/immediate; deploy follows a canary to 100% at a terminal and Ctrl-C detaches; `--safe`, `--canary-preset`, `--canary-stages` deprecated |
| 1445 | [Post-release watch](1445-post-release-watch.md) | proposed | For 30 minutes after a release reaches 100%, apid compares it with its predecessor's pre-rollout traffic; a clear 5xx or p95 regression rolls back through the readiness-gated rollback (trigger `post_release`) and records an explained decision |
| 1451 | [Guarded, reversible config changes](1451-guarded-reversible-config-changes.md) | proposed | Every env/secret write records a config revision (`gregale env history`, `gregale env rollback`); a guard judges the first new-config traffic against the old and restores the previous config on a 5xx failure; secret values are kept only while a guard is open; deleted or rotated secrets are never restored automatically |
| 903 | [Object-storage entity outbox commit contract](903-object-storage-entity-outbox-contract.md) | internal engine; delivery pending | Atomically rooted outgoing intents with bounded restore, receipt replay and storage accounting |
| 1037 | [Interactive app-task attach](1037-interactive-app-task-attach.md) | accepted; operator-gated preview | `gregale app exec -it` attaches one client to a fresh task VM's PTY through the gateway and vmmd with a single-use token; schedd lifecycle unchanged |
| 971 | [Retire dead same-boot instance records after verified absence](971-same-boot-dead-instance-record-retirement.md) | accepted | vmmd startup retires instance records whose process is gone and whose kernel resources are verifiably absent, freeing quarantined slots and identity-verified temporary files (amends ADR-472, extends ADR-478) |
| 630 | [Guest-init-only release reuse](630-guest-init-only-release-reuse.md) | proposed | Patch PID 1 into staged bases instead of rebuilding them, and key the builder cache on the builder image plus a guest-init build contract version |
| 635 | [Authenticated tenant workflow continuations](635-tenant-workflow-continuations.md) | accepted | Tenant-authenticated event and callback continuation routes with atomic live-link checks |
| 636 | [Tenant-scoped scheduled workflow starts](636-tenant-scheduled-workflow-starts.md) | accepted | Per-tenant schedule cursors and atomic tenant-bound run admission |
| 637 | [Tenant-configurable workflow schedules](637-tenant-configurable-workflow-schedules.md) | accepted | Explicitly opt in to tenant-owned cadence, overlap, and enablement settings |
| 644 | [Verified MCP promotion and resource policy](644-mcp-verified-promotion-and-resource-policy.md) | accepted | Verify zero-traffic candidates, gate caller catalogs, and reuse gateway JWT resource authorization |
| 640 | [Watchdog claims WAKING rows before teardown](640-watchdog-claims-waking-rows-before-teardown.md) | proposed | Every WAKING row gets the restore + cold-boot budget, and the watchdog CASes it to COLD_BOOTING before Destroy so a peer schedd's RUNNING publication can never point at a destroyed VM |
| 641 | [Edge health follows the last instance](641-edge-health-follows-last-instance.md) | proposed | A parked app's edge health answer comes from its last instance (only FAILED is unhealthy), cached 15 s per gateway, instead of the process-local wake outcome that went unknown after every restart |
| 642 | [Restore-safe guest timers](642-restore-safe-guest-timers.md) | proposed | Guests boot with kvm-clock and the one-shot LAPIC timer because Firecracker 1.7 restores lost TSC-deadline interrupts; backing identity v2 refuses captures booted with the old profile |
| 643 | [Park-to-admit for refused wakes](643-park-to-admit-refused-wakes.md) | proposed | A gateway wake refused for fleet capacity parks one idle, floor-respecting instance of another owned app and retries once, instead of answering 503 until the idle timeout |
| 680 | [Restored processes reseed their userspace random generators before serving](680-restore-userspace-rng-reseed.md) | accepted | guest-init reseed barrier with Node (N-API RAND_poll addon) and Python preloads; fails closed to cold boot; GHSA-24j2-p895-mwc9 |
| 981 | [Node RNG caches are drained on first use after a restore](1110-node-rng-caches-drained-on-first-use.md) | accepted | The reseed reply marks randomUUID/randomInt caches stale and the preload's wrappers drain on first read, so apps that never call them skip the drain; capture-time emptying was rejected after metal showed it replays (amends ADR-680) |
| 790 | [meterd catches up closed minutes it did not roll](790-meterd-closed-minute-catch-up.md) | proposed | A sample tick re-rolls, from the billing ledger, closed minutes of the last 24 h never recorded compute-complete (≤15 per tick); idempotent through first-write-wins; H5-55 |
| 791 | [The build wall-clock limit is 15 minutes](791-build-wall-clock-limit.md) | accepted | Records `BuildTimeoutSeconds = 900` (raised from the spec's 10 min in August without an ADR); end-to-end deploy time is not separately enforced; H5-71 |
| 815 | [Guarded native runtime qualification collector](815-guarded-native-runtime-qualification-collector.md) | accepted | Collects and signs exact-runtime native evidence only after teardown, host rechecks, service restoration and retained import |
| 816 | [Fresh cold-boot readiness receipts for runtime upgrade candidates](816-runtime-upgrade-candidate-acceptance.md) | accepted | Requires exact candidate identity, unrevoked qualification and a fresh cold-boot readiness receipt before activation |
| 817 | [Trusted import of exact runtime native qualification evidence](817-trusted-runtime-qualification-import.md) | accepted | Verifies trusted signatures, native test/leak evidence, published artifact bindings and durable readback before recording receipts |
| 644 | [Verified MCP promotion and resource policy](644-mcp-verified-promotion-and-resource-policy.md) | accepted | Verify zero-traffic candidates, gate caller catalogs, and reuse gateway JWT resource authorization |
| 646 | [Unified backfill delivery inspection](646-unified-backfill-delivery-inspection.md) | accepted | Complete source/consumer inspection across webhook and workflow backfills |
| 647 | [Independent event routing by default](647-independent-event-routing-default.md) | accepted | Default independent routing for captured event recipients |
| 648 | [Independent workflow event routing](648-independent-workflow-event-routing.md) | accepted | Workflow event recipient routing with lease-bound recovery |
| 725 | [Fair workflow admission, history, and transient retries](725-workflow-admission-history-and-transient-retries.md) | accepted | Durable tenant/workflow fairness, atomic occurrence history, throttling retries, and workflow alert signals |
| 726 | [Bounded workflow schedule catch-up](726-bounded-workflow-schedule-catch-up.md) | accepted | Opt-in latest-fire recovery with a bounded window and tenant parity |
| 727 | [Workflow handler deployment pins](727-workflow-handler-deployment-pins.md) | accepted | Immutable handler code per run, private retention from event acceptance, and exact deployment routing |
| 649 | [Fair bounded workflow dispatch](649-fair-bounded-workflow-dispatch.md) | accepted | Fill bounded execution slots and persist app/tenant service order with lease-aware dispatch caps |
| 650 | [Schema-generated Data APIs](650-schema-generated-data-api.md) | accepted | Restricted customer SQL through schema-bound, generated application clients |
| 730 | [Automation queue health](730-automation-queue-health.md) | accepted | Current dispatch capacity and bounded aggregate waiting reasons, independent of the historical health window |
| 651 | [Due automation backlog alerts](651-due-automation-backlog-alerts.md) | accepted | Opt-in webhook alerts for aged due work, including overdue parked wakes and expired leases |
| 652 | [Workflow run diagnostics and resume preview](652-workflow-run-diagnostics-and-resume-preview.md) | accepted | Read-only queue and step diagnostics with precise recovery blockers and the existing continuation plan |
| 653 | [Read-only workflow schedule preview](653-workflow-schedule-preview.md) | accepted | Timezone-aware upcoming fires and a what-if catch-up decision using the durable schedule cursor |
| 654 | [Controlled workflow schedule replay](654-controlled-workflow-schedule-replay.md) | accepted | Explicit replay of selected skipped occurrences with definition pins, idempotency, and normal admission checks |
| 655 | [Selected unstarted workflow cancellation](655-selected-unstarted-workflow-cancellation.md) | accepted | Preview and atomically cancel selected pending runs that have never started |
| 678 | [Compose prebuilt image workloads](678-compose-prebuilt-image-workloads.md) | accepted | Deploy stateless image services through imaged with immutable resolution and existing project dependency policies |
| 679 | [Image-published deployment trigger](679-image-published-deployment-trigger.md) | accepted | CI publishes an immutable image, then hands it to existing deployment admission with durable workload/scope/digest deduplication |
| 686 | [Freeze Compose commands per image deployment](686-frozen-project-image-commands.md) | accepted | Capture Compose CMD in the deployment profile and preserve it through app edits, image processing, retries, and runtime port updates |
| 681 | [Image deployment promotion ordering](681-image-deployment-promotion-ordering.md) | accepted | Atomically reject older same-scope image candidates at cutover and recheck recorded GitHub branches while preserving explicit rollback |
| 682 | [Compose image healthchecks](682-compose-image-healthchecks.md) | accepted | Validate and freeze partial Compose healthcheck overrides for prebuilt image workloads, preserving artifact inheritance and existing guest execution |
| 683 | [Fresh image healthchecks gate readiness](683-image-healthcheck-readiness.md) | implemented; native qualification pending | Require fresh command proof before serving boot, restore, warm resume, or migration |
| 684 | [Image healthchecks drive runtime recovery](684-image-healthcheck-runtime-recovery.md) | implemented; native qualification pending | Recover serving images after declared command failures using existing scheduler ownership and restart limits |
| 685 | [Compose dependency readiness gates](685-compose-dependency-release-gates.md) | implemented | Hold dependent project releases against exact dependency deployments with durable waits and atomic promotion checks |
| 622 | [Event protection for new object versions](622-event-protection-for-new-object-versions.md) | accepted | Captured event defaults, enrolled creation headers and strict exact-version policy proof |
| 621 | [Durable object event holds](621-durable-object-event-holds.md) | accepted | Exact-version variable retention, immutable native policy snapshots and single-dispatch release recovery |
| 620 | [Protection-aware object lifecycle deletion](620-protection-aware-object-lifecycle.md) | accepted | Fresh native retention/hold checks, bounded scan deferrals and exact absence proof before settlement |
| 619 | [Durable object write protection](619-durable-object-write-protection.md) | accepted | Immutable bounded write policies and exact native version verification for PUT, copy and multipart |
| 596 | [Service binding dependency evidence](596-service-binding-dependency-evidence.md) | accepted | Versioned evidence for service binding dependencies. |
| 597 | [Exact caller deployment for service smoke tests](597-caller-pinned-service-smoke.md) | accepted | Probe the exact selected caller deployment. |
| 598 | [Stored binding release policy per deployment scope](598-stored-binding-release-policy.md) | accepted | Persist and enforce binding checks at release transitions. |
| 599 | [Binding-checked exact canary recovery](599-binding-checked-canary-recovery.md) | accepted | Recover an exact canary pair with fresh binding evidence. |
| 600 | [Binding-checked service routing and abort handoffs](600-binding-checked-service-handoffs.md) | accepted | Fence service handoff, application ACK and drain completion. |
| 601 | [Binding-checked historical rollback](601-binding-checked-historical-rollback.md) | accepted | Prepare and restore an exact historical deployment safely. |
| 602 | [Durable binding-checked alert rollback](602-durable-binding-checked-alert-rollback.md) | accepted | Persist alert-triggered canary recovery and resume retries. |
| 603 | [Alert-driven service rollback](603-alert-driven-service-rollback.md) | accepted | Resume alert-triggered service recovery through its handoff barriers. |
| 604 | [Alert-driven rollback after completed releases](604-alert-driven-historical-rollback.md) | accepted | Pin completed-release alert recovery to an eligible predecessor. |
| 605 | [Deployment evidence for post-release automatic rollback](605-deployment-evidence-for-post-release-rollback.md) | accepted | Require exact post-cutover error evidence before accepting automatic rollback. |
| 589 | [Versioned business-key and customer routing for Commit](589-commit-customer-routing.md) | accepted; operator qualification only | Versioned customer-scoped business keys route unrelated Commit work independently and serialize matching work |
| 588 | [In-place retry of a failed workflow step](588-in-place-workflow-step-retry.md) | accepted | Retry a failed step within its existing run while preserving attempt history and managed operation identity |
| 587 | [Transactional managed HTTP workflow steps](587-transactional-workflow-http-steps.md) | accepted | Reuse customer transaction receipts and durably deliver workflow operation effects |
| 586 | [Transactional operation handler SDK](586-transactional-operation-handler-sdk.md) | accepted for implementation | Node, Go, and Python helpers atomically save customer PostgreSQL writes, replay receipts, and webhook intent |
| 585 | [Managed operation results with durable webhook effects](585-managed-operation-webhook-effects.md) | accepted for implementation; promotion requires runtime qualification | Atomically fence managed HTTP completion with receiver-scoped durable webhook delivery |
| 584 | [Durable object version protection](584-durable-object-version-protection.md) | accepted | Exact-version retention and legal holds with durable mutation/readback recovery |
| 568 | [Git-owned environment intent and continuous reconciliation](568-environment-gitops-contract.md) | implementation in progress | Reviewed definitions, scoped ownership and binding retirement, bounded reports, durable effects, and scoped runtime convergence |
| 1000 | [GitLab.com as a reviewed environment Git source](1000-gitlab-gitops-source.md) | implementation in progress | Account OAuth, verified project identity, commit-pinned reads, manual report scope |
| 576 | [Private TCP addressing between services](576-private-tcp-service-addressing.md) | proposed | Account-scoped service addresses in 198.19.0.0/16 give non-HTTP protocols natural-port private reachability through a node-local TCP proxy |
| 520 | [Self-hosted TLS for customer custom domains](520-self-hosted-custom-domain-tls.md) | proposed | Caddy on-demand certificates gated by gatewayd-public's ask endpoint; durable per-wildcard issuance budget; no CDN in the customer-domain path |
| 580 | [Control-plane role convergence in CD](580-control-plane-convergence-in-cd.md) | proposed | CD runs the control-plane bootstrap play before release activation whenever a hash of its inputs (roles, inventory, operator vars) changes |
| 567 | [Runtime base convergence](567-runtime-base-convergence.md) | proposed | Keep each node's cached runtime base byte-identical to its shared publication so ADR-510 snapshots restore on any node |
| 510 | [Snapshot backing image identity](510-snapshot-backing-image-identity.md) | proposed | Restore only onto the kernel and read-only base a capture was taken with; refuse and cold-boot otherwise |
| 516 | [Replay recent managed PostgreSQL usage corrections](516-managed-postgres-usage-correction-replay.md) | accepted | Recover missing windows first, bound recent revision replay, and reject unsafe collection windows |
| 566 | [Financial visibility and scoped spending controls](566-financial-visibility-and-scoped-budgets.md) | monitored notifications preview; stopping pending | Retained costs, forecasts, revisioned budgets and durable spending alerts; stopping and strict limits have separate acceptance gates |
| 565 | [Managed PostgreSQL fleet usage recovery](565-managed-postgres-fleet-usage-recovery.md) | accepted | Recover one window per database per round across the fleet before correction replay, using durable observation order |
| 569 | [Managed PostgreSQL terminal usage coverage](569-managed-postgres-terminal-usage-coverage.md) | accepted | Retain known resources through shutdown, recover finite final windows, and require every final correction before completing automatic accounting |
- [ADR-581: Managed PostgreSQL uncertain accounting intent](581-managed-postgres-uncertain-accounting-intent.md) — durable provider-attempt ownership and identity recovery before deletion
- [ADR-582: Managed PostgreSQL accounting diagnostics](582-managed-postgres-accounting-diagnostics.md) — operator-only per-database evidence and admission blockers
- [ADR-583: Managed PostgreSQL retained usage import](583-managed-postgres-retained-usage-import.md) — previewed, atomic accounting repairs with immutable evidence
- [ADR-591: Managed PostgreSQL legacy accounting reconciliation](591-managed-postgres-legacy-accounting-reconciliation.md) — attested identity/shutdown repair with audited coverage reset
| 529 | [Bindings gate at traffic promotion](529-binding-gated-promotion.md) | accepted | Require evidence and atomically fence traffic promotion on observed revisions |
| 528 | [Deployment selection for bindings verification](528-deployment-binding-verification.md) | accepted | Probe the exact deployment artifact with its scoped runtime configuration |
| 527 | [Bindings preflight policy](527-binding-preflight-policy.md) | accepted | Apply configurable readiness, age, rotation and runtime gates |
| 526 | [Object-storage binding verification](526-object-storage-binding-verification.md) | accepted | Verify storage credentials and identity through task guest and rotation metadata |
| 525 | [Binding runtime configuration freshness](525-binding-runtime-freshness.md) | accepted | Expose configuration and secret revisions for resident instances without reading values |
| 524 | [Durable binding verification evidence](524-binding-verification-evidence.md) | accepted | Persist versioned verification evidence for exact binding configurations |
| 509 | [Per-target binding adoption diagnostics](509-binding-adoption-target-diagnostics.md) | accepted | Return stable, sanitized workload-secret statuses and blocker reasons in binding checks |
| 508 | [Process-generation secret acknowledgements](508-process-generation-secret-acknowledgements.md) | accepted | Register each execution before start and require matching generation evidence for strict adoption |
| 507 | [Wait for the initial secret revision](507-initial-secret-revision-wait.md) | accepted | Bound bootstrap polling for an empty initial revision, cancel on shutdown and serve only after application ACK |
| 506 | [Startup-safe runtime secret notifications](506-startup-safe-runtime-secret-notifications.md) | accepted | Gate reloads on per-process readiness, skip redundant startup signals and recover queued or failed delivery |
| 505 | [Atomic runtime secret snapshots](505-atomic-runtime-secret-snapshots.md) | accepted | Publish values, revision and legacy views as one generation; bind application reads and acknowledgements to one envelope |
| 504 | [Workload secret reload restart consistency](504-workload-secret-reload-restart-consistency.md) | accepted | Prepare projections once, preserve revisions across sidecar restarts and remove revoked credentials from restart environments |
| 503 | [Main-workload secret reload with sidecars](503-main-secret-reload-with-sidecars.md) | accepted | Independent main and sidecar reload, grants and application receipts in the same deployment |
| 502 | [Version-bound application adoption for managed bindings](502-binding-application-adoption.md) | accepted | Require an application receipt for the exact deployed binding version |
| 523 | [Queue consumer readiness at bindings promotion](523-queue-binding-readiness.md) | accepted | Include queue consumer health and poll freshness in promotion checks |
| 522 | [Deployment-pinned outbound binding verification](522-outbound-binding-verification.md) | accepted | Verify configured outbound probes without disclosing credential material |
| 497 | [Stateless automation simulation with bounded sample data](497-automation-simulation.md) | accepted | Validate definitions and sample traces without invoking app handlers or external integrations |
| 496 | [Start customer automations from verified Stripe webhooks](496-verified-webhook-automation-starts.md) | accepted | Verify inbound webhook signatures, deduplicate provider events, and route directly into published automations |
| 495 | [Customer-requested continuation of failed workflow runs](495-failed-workflow-run-resume.md) | accepted | Resume failed runs with fenced retries and an auditable continuation history |
| 494 | [Bounded workflow iteration](494-bounded-workflow-iteration.md) | accepted | Execute bounded list iterations durably and recover completed items without restarting them |
| 575 | [Stateless automation simulation with bounded sample data](575-automation-simulation.md) | accepted | Validate definitions and sample traces without invoking app handlers or external integrations |
| 574 | [Start customer automations from verified Stripe webhooks](574-verified-webhook-automation-starts.md) | accepted | Verify inbound webhook signatures, deduplicate provider events, and route directly into published automations |
| 573 | [Customer-requested continuation of failed workflow runs](573-failed-workflow-run-resume.md) | accepted | Resume failed runs with fenced retries and an auditable continuation history |
| 572 | [Bounded workflow iteration](572-bounded-workflow-iteration.md) | accepted | Execute bounded list iterations durably and recover completed items without restarting them |
| 491 | [Native workflow branch joins](491-native-workflow-branch-joins.md) | accepted | Merge selected conditional branches with deterministic outputs and durable skip propagation |
| 490 | [Declarative workflow step guards](490-declarative-workflow-step-guards.md) | accepted | Select workflow paths from prior step outputs without a customer adapter handler |
| 489 | [Managed outbound integration steps in workflows](489-workflow-outbound-integration-steps.md) | accepted (preview) | Call managed integrations with scoped identity, bounded responses, and durable retry decisions |
| 488 | [Customer automation drafts and publication](488-customer-automation-authoring.md) | accepted (preview) | Create versioned drafts and publish them independently from YAML-owned definitions |
| 487 | [Scheduled workflow starts](487-scheduled-workflow-starts.md) | accepted (preview) | Start workflow runs from durable schedules with overlap controls and missed-minute semantics |
| 432 | [Start workflows through durable event fanout](432-event-workflow-starts.md) | accepted | Capture matching event recipients and admit workflow runs through the durable fanout ledger |
| 480 | [Platform paths reserved on platform hosts only](480-platform-paths-reserved-on-platform-hosts.md) | proposed | App, preview and custom-domain hosts own /v1, /status, /docs, /login, /oauth/* and the edge well-known documents |
| 460 | [Prepared network policy retention](460-prepared-network-policy-retention.md) | proposed | Preserve fresh unused exact-policy spares within ADR-149's existing global capacity |
| 499 | [Customer-cohort production route monitoring](499-customer-cohort-production-route-monitoring.md) | accepted | Opt-in tenant/consumer budgets, redacted identity details, aggregate impact counts, and a bounded recovery inventory |
| 595 | [Versioned inherited application standards](595-inherited-application-standards.md) | implementation in progress | Organization-owned versions, automatic enrollment, reviewed rollouts, field provenance and bounded exceptions; runtime acceptance pending |

| 424 | [Managed outbound integrations for stateless Runs](424-run-scoped-managed-outbound-integrations.md) | proposed | Explicit account grants and a bounded vsock broker; the execution VM remains networkless |
| 427 | [Exclusive operation policy retirement](427-exclusive-operation-policy-retirement.md) | accepted | Idle retirement preserves ownership history and releases the active policy quota slot |
| 428 | [Native gRPC request stream admission](428-native-grpc-request-stream-admission.md) | accepted | Incremental bounded native gRPC requests and duplex response controls through the gateway handler |
| 429 | [Handled internal service request evidence](429-handled-service-request-evidence.md) | accepted | Actual guest response evidence, exact registered-scenario timestamps and truthful cleanup phases |
| 423 | [Scenario-scoped chaos testing](423-scenario-scoped-chaos-testing.md) | accepted | Bounded request faults on registered test-run service calls, with deterministic selection, expiry, and lifecycle-profile execution |
| 984 | [Scenario resilience campaigns and recovery regression gates](984-scenario-resilience-campaigns.md) | accepted | Leased DNS faults, deterministic campaigns, confirmed recovery evidence, pinned replay and offline trends |
| 392 | [Event and trace wire conformance](392-event-and-trace-wire-conformance.md) | accepted | OTLP/HTTP encodings and standard responses, CloudEvents attributes, and pinned official AsyncAPI validation |
| 391 | [Provider invoice history discovery and backfill](391-provider-invoice-history-backfill.md) | accepted | Authenticated provider history discovery with bounded, customer-scoped imports |
| 390 | [Authenticated provider invoice refresh](390-provider-invoice-refresh.md) | accepted | Authenticated provider facts, bounded reads, and atomic enrichment |
| 389 | [Invoice detail lifecycle tracking](389-invoice-detail-lifecycle.md) | accepted | Independent record timestamps, exact row fingerprints, and historical coverage |
| 388 | [Provider invoice facts for FOCUS](388-provider-invoice-facts.md) | accepted | Durable provider facts, reconciled line classifications, and source coverage |
| 387 | [FOCUS invoice projection](387-focus-invoice-projection.md) | accepted | Account-scoped invoice CSV and metadata, exact reconciliation, and declared source gaps |
| 461 | [Managed PostgreSQL credential privileges](482-managed-postgres-credential-privileges.md) | accepted for gated preview | SQL runtime and migration logins, stable schema ownership, and verified role isolation |
| 386 | [HTTP/1 upgrade socket ownership](386-http001-upgrade-socket-ownership.md) | accepted | Hijack successful raw upgrades, retain buffered duplex bytes, and cancel both directions on session closure |
| 385 | [Durable scheduled work policies](385-scheduled-work-policies.md) | accepted for recurring Jobs and deployment-command Crons | Persist versioned schedule decisions and classified retries with per-occurrence history |
| 384 | [Fetch-compatible internal service port](384-fetch-compatible-internal-service-port.md) | accepted | Canonical HTTP bindings use 10081 with the existing authorization path; legacy 10080 remains available |
| 383 | [Curated dependency profiles for stateless executions](383-curated-stateless-execution-profiles.md) | accepted | Preinstalled immutable package sets, separate snapshots, and lease-pinned image provenance |
| 382 | [Bounded inline output artifacts for stateless executions](382-stateless-execution-output-artifacts.md) | accepted | Explicit file exports within the existing receipt output budget; disposable scratch and VM teardown |
| 381 | [Compact platform-tenant statement lines](381-compact-platform-tenant-statement-lines.md) | accepted | Separate compact invoice rows from exact private minute coverage; remove the public 20,000-line ceiling |
| 380 | [Gregale Issues](380-gregale-issues.md) | accepted for preview implementation | Account/app-scoped issue reporting, grouping, triage, occurrence retention, customer impact, OTLP and webhook recovery |
| 379 | [Complete the local development bridge workflow](379-development-bridge-workflow.md) | accepted for internal HTTP use | Supervised execution, framework propagation, session activity, dashboard controls and native acceptance |
| 393 | [Managed exclusive operations](393-managed-exclusive-operations.md) | implementation in progress | Account and trusted customer scope, explicit contention modes, durable ownership generations, lease recovery, and stale-owner fencing |
| 378 | [Local processes in development environments](378-development-bridge.md) | accepted for internal HTTP use | Scoped one-hour sessions, local service routing, bounded inspection and webhook replay; operator-gated pending native acceptance |
| 590 | [Complete project-environment clones and qualified promotion](590-complete-project-environment-clones.md) | proposed | Complete effective-state snapshots, isolated data capture, exact qualification, and guarded full promotion |
| 430 | [Managed PostgreSQL Commit outbox](430-managed-postgresql-commit-outbox.md) | accepted; gated | Customer transaction outbox → managed relay → atomic durable HTTP operation receipt; at-least-once delivery with consumer-owned deduplication |
| 374 | [Application-keyed background work policies](374-application-keyed-work-policy.md) | accepted | Shared durable policy for per-key admission, claim, replacement, debounce, expiry, and fairness |
| 372 | [Tenant egress through a dedicated WireGuard gateway](372-tenant-egress-gateway.md) | accepted | Opt-in manifest gateway; bridged tenant IPv4 leaves from the gateway's address, fails closed, and gets a second deny layer there |
| 370 | [GitHub Actions immutable OIDC subject bootstrap](370-github-actions-immutable-oidc-subjects.md) | accepted | Resolve immutable GitHub repository IDs for binding lookup while pinning the complete subject in the OIDC trust policy |
| 369 | [Resumable managed realtime channels](369-resumable-managed-realtime-channels.md) | proposed | Ordered outbound channel log, retention floor, versioned client replay, acknowledgements, and channel grants |
| 348 | [Customer after-restore readiness hook](348-customer-after-restore-hook.md) | accepted | Opt-in, loopback HTTP callback after entropy and clock repair and before restore readiness; failure cold-boots |
| 354 | [Confirmed platform-tenant reconciliation apply](354-platform-tenant-confirmed-reconciliation-apply.md) | accepted | Apply only a current ownership-aware plan atomically; detach managed links, remove managed declared hostnames, preserve unmanaged resources |
| 362 | [Durable platform-tenant reconciliation receipts](362-platform-tenant-reconciliation-receipts.md) | accepted | Persist secret-free, immutable outcomes atomically and expose tenant-scoped recovery and audit reads |
| 352 | [Fence snapshot publication against runtime configuration changes](352-snapshot-publication-runtime-config-fence.md) | accepted | Reject durable publication after the app runtime configuration changes |
| 353 | [Observe snapshot publication at the durable row boundary](353-snapshot-publication-outcomes.md) | accepted | Audit warm promotion after snapshot row publication |
| 344 | [Atomic usage statement webhook production](344-usage-statement-webhook-outbox.md) | accepted | Commit the finalized statement and webhook event together, then relay to the delivery ledger with event/subscription dedupe; extends ADR-076 |
| 343 | [Durable app wake webhook completion](343-app-wake-webhook-completion.md) | accepted | Track parked-to-active wakes until a ready instance exists, recover app.woken in schedd, and supersede pending wakes when park wins |
| 342 | [Durable app park webhook completion](342-app-park-webhook-completion.md) | accepted | Track park transitions through instance drain, recover completion in schedd, then enqueue `app.parked` through ADR-344's outbox |
| 349 | [Distinguish application restore-hook failures](349-after-restore-failure-signal.md) | accepted | Typed ACK 13 classification and bounded `after_restore_failed` wake-failure signal |
| 350 | [Show application restore-hook fallback in wake timelines](350-customer-restore-fallback-reason.md) | accepted | Customer-visible reason when a restore hook causes cold-boot fallback |
| 351 | [Customer callback before terminal init capture](351-customer-before-checkpoint-hook.md) | accepted | Required loopback callback for new init snapshots; warm capture is skipped |
| 340 | [Platform-tenant managed resource provenance](340-platform-tenant-managed-resource-provenance.md) | accepted | Mark only resources created by the owner bundle apply as managed; links never imply ownership |
| 341 | [Ownership-aware platform-tenant reconciliation plan](341-platform-tenant-reconciliation-plan.md) | accepted | Add a deterministic read-only desired-state diff with managed removal candidates and unmanaged retention |
| 363 | [Platform-tenant reconciliation applied webhook](363-platform-tenant-reconciliation-applied-webhook.md) | accepted | Enqueue a safe receipt-pointer notification in the apply transaction for retryable delivery |
| 364 | [Platform-tenant offboarding plan](364-platform-tenant-offboarding-plan.md) | accepted | Preview credential, policy, and managed-resource cleanup with a stable, read-only plan hash |
| 365 | [Confirmed platform-tenant offboarding apply](365-platform-tenant-offboarding-apply.md) | accepted | Atomically enforce a fresh offboarding plan, suspend the tenant, revoke access, detach managed links, and persist a recoverable receipt |
| 339 | [Runtime revocation of delivered secrets](339-runtime-secret-revocation.md) | accepted | Remove deleted keys from opted-in workloads' runtime projections and signal the authorized workload; extends ADR-338 |
| 338 | [Sidecar runtime secret reload](338-sidecar-runtime-secret-reload.md) | accepted | Workload-scoped refresh, signal, and application acknowledgement for explicitly granted sidecar secrets; extends ADR-280 |
| 336 | [Tenant-scoped self-service multi-app onboarding](336-platform-tenant-self-service-multi-app-onboarding.md) | accepted | All-or-nothing, previewable creation or replay of one app-local customer identity per selected linked surface, under owner policy and a tenant-wide cap |
| 335 | [Tenant-scoped self-service customer offboarding](335-platform-tenant-self-service-customer-offboarding.md) | accepted | Atomic, tenant-bound customer and active-key revocation with retry-safe responses |
| 334 | [Owner-controlled downstream customer provisioning](334-platform-tenant-consumer-provisioning-policy.md) | accepted | Default-off customer creation policy and per-tenant active-customer ceiling, separate from the tenant-bound manage scope |
| 333 | [Backoff-aware realtime callback replay monitoring](333-realtime-callback-backoff-aware-replay-monitoring.md) | accepted | Ready/delayed callback heads and delivery-attempt metrics distinguish intentional backoff from stalled replay |
| 332 | [Realtime callback replay scheduling index](332-realtime-callback-replay-index.md) | accepted | Ordered per-connection queues and ready/delayed heaps for scalable durable callback replay |
| 331 | [Realtime callback retry backoff](331-realtime-callback-retry-backoff.md) | accepted | Persisted jittered exponential retry schedule and bounded `Retry-After` handling for durable callbacks |
| 330 | [Realtime callback dead-letter inspection and replay](330-realtime-callback-dead-letter-inspection-and-replay.md) | accepted | Metadata-only listing and deliberate replay through the private realtimed socket |
| 329 | [Realtime callbacks without durable delivery](329-realtime-callbacks-without-durable-delivery.md) | accepted | Close sockets when direct HTTP message callbacks fail without an outbox |
| 328 | [Realtime callback outbox admission failure handling](328-realtime-callback-outbox-admission-failures.md) | accepted | Stop socket reads and page when callback events cannot be durably admitted due to storage errors |
| 327 | [Realtime callback outbox backpressure](327-realtime-callback-outbox-backpressure.md) | accepted | Retry admission while space frees; close overloaded sockets with 1013 and count rejected callbacks |
| 326 | [Realtime callback outbox capacity warning](326-realtime-callback-outbox-capacity-warning.md) | accepted | Expose pending capacity and alert before enqueue rejection |
| 325 | [Realtime callback replay recovery observability](325-realtime-callback-replay-recovery-observability.md) | accepted | Supervisor restart counter and alert for repeated recovery cycles |
| 324 | [Managed realtime callback replay supervision](324-realtime-callback-replay-supervisor.md) | accepted | In-process replay restart with shutdown-aware capped backoff |
| 323 | [Managed realtime callback backlog observability](323-realtime-callback-backlog-observability.md) | accepted | Oldest pending age, replay progress, and a stalled-replay alert |
| 322 | [Bounded parallel managed realtime callback replay](322-parallel-realtime-callback-replay.md) | accepted | Per-connection ordered callback recovery with bounded cross-connection concurrency |
| 321 | [Live managed realtime callback credential rotation](321-live-realtime-callback-auth-rotation.md) | accepted | Rotate callback credentials on active realtime connections without reconnecting clients |
| 320 | [Bounded managed realtime callback dead letters](320-bounded-realtime-callback-dead-letters.md) | accepted | Retain callback dead letters within a byte cap and expose durable recovery state |
| 319 | [Persistent managed realtime callback outbox](319-persistent-realtime-callback-outbox.md) | accepted | Reboot-safe node-local callback spool with migration from `/run` |
| 318 | [Managed realtime revocation and delivery outcomes](318-managed-realtime-reliability.md) | accepted | Endpoint inventory repair, socket revocation, ordered callback replay, and partial fleet publish reporting |
| 317 | [Tenant-scoped self-service consumer credentials](317-platform-tenant-self-service-credentials.md) | accepted | Tenant-bound inventory and hash-only key rotation under the owner's transactional delegation policy |
| 301 | [Owner-controlled delegated platform-tenant credential policy](301-platform-tenant-credential-policy.md) | accepted | Explicit downstream key-scope allowlist and active-key ceiling per linked consumer; self-service remains off by default |
| 300 | [Tenant-scoped self-service hostname onboarding](300-platform-tenant-self-service-hostnames.md) | accepted | Narrow hostnames:manage credential; existing linked surfaces, delegated DNS suffixes, and DNS proof only |
| 316 | [GitHub branch freshness before promotion](316-source-ref-branch-freshness-before-promotion.md) | accepted | Persist source-ref and webhook branch intent, then recheck through githubd before a candidate becomes live |
| 315 | [Actions-owned mapped environment deployments](315-actions-mapped-environment-deployments.md) | accepted | Generated Actions workflows route configured branches into their registered project environments |
| 314 | [Actions-owned release-tag deployments](314-actions-release-tag-deployments.md) | accepted | Deploy only newly created SemVer release tags from the immutable event SHA |
| 313 | [GitHub push head recheck before reconciliation](313-github-push-head-recheck.md) | accepted | Recheck branch freshness after fetch and scan, immediately before reconciling a push |
| 312 | [PR preview freshness](312-pr-preview-freshness.md) | accepted | Verify current PR state and head before preview mutation and fence older preview promotions |
| 311 | [GitHub push freshness and promotion fence](311-github-push-freshness-and-promotion-fence.md) | accepted | Verify remote branch heads before webhook dispatch and fence older GitHub revisions at promotion |
| 310 | [Git-driven deployment ownership and preview quotas](310-git-driven-deployment-ownership-and-preview-quotas.md) | accepted | One production push owner, terminal Action checks, and a bounded separate PR-preview allowance |
| 299 | [Owner-controlled platform-tenant hostname delegation](299-platform-tenant-hostname-delegation.md) | accepted | Deny-by-default DNS suffix allowlist and tenant-wide hostname cap for downstream self-service |
| 298 | [Tenant-scoped surface deployment outcome webhooks](298-platform-tenant-deployment-webhooks.md) | accepted | Transactional live/failed deployment outcomes for explicitly linked surfaces; safe revision metadata without source details or raw errors |
| 337 | [Platform-tenant customer lifecycle webhooks](337-platform-tenant-customer-lifecycle-webhooks.md) | accepted | Transactional linked/offboarded events for app-local customer identities, with stable cross-app references and no credentials |
| 297 | [Shared outbound provider cooldown](297-shared-outbound-provider-cooldown.md) | proposed | Postgres-shared cooldown honors provider Retry-After across outbound gateway replicas |
| 296 | [Shared outbound retry budget](296-shared-outbound-retry-budget.md) | proposed | Per-integration Postgres token bucket caps extra provider attempts across gateway replicas |
| 295 | [Per-integration outbound circuit breaker](295-outbound-circuit-breaker.md) | accepted | Shared Postgres breaker state, bounded cool-down, and one cross-replica half-open provider probe |
| 294 | [Opt-in outbound HTTP response cache](294-outbound-response-cache.md) | accepted | Short-TTL, process-local cache for eligible GET responses with strict tenant, credential, freshness, and memory bounds |
| 293 | [Tenant-scoped self-service customer creation](293-platform-tenant-self-service-customers.md) | accepted | Atomic creation on a linked active surface under the owner's default-off tenant cap |
| 292 | [Bind GitHub OIDC to immutable repository identity](292-github-oidc-binding-identity-ids.md) | accepted | Persist GitHub owner and repository IDs with bindings and require exact immutable identity for OIDC bootstrap |
| 290 | [Tenant-scoped surface certificate lifecycle webhooks](290-platform-tenant-certificate-webhooks.md) | accepted | Transactional certificate-state events for surfaces explicitly linked to the tenant; expose status and expiry only, not secrets or raw provider errors |
| 289 | [Tenant-scoped hostname verification webhooks](289-platform-tenant-hostname-webhooks.md) | accepted | Transactional hostname-verification events only for surfaces explicitly linked to the tenant; DNS ownership does not imply certificate or route readiness |
| 288 | [Service dependency reliability controls and fleet signals](288-service-dependency-reliability.md) | accepted | Caller-bounded timeouts and retries, shared retry budgets, breakers, and trusted per-edge telemetry |
| 291 | [Tenant-bound self-service activation snapshot](291-platform-tenant-self-activation.md) | accepted | Narrow activation:read scope and a redacted current-state snapshot for only the tenant represented by a downstream bearer |
| 283 | [Environment-scoped custom domains](283-environment-scoped-custom-domains.md) | accepted | Bind verified custom hostnames to project environments and route only through their active release graph |
| 282 | [Primary workload startup dependencies](282-primary-workload-startup-dependencies.md) | accepted | Main workload may wait for a declared long-running companion lifecycle condition |
| 281 | [Continuous primary-app readiness](281-continuous-primary-app-readiness.md) | accepted | Independent recurring traffic gate for the primary workload, layered after startup readiness and separate from VM liveness |
| 280 | [Sidecar-scoped secret delivery](280-sidecar-scoped-secret-delivery.md) | accepted | Per-sidecar positive app-secret grants, deployment-scope resolution, versioned restart delivery, and no implicit inheritance |
| 279 | [Guest verification of service-caller assertions](279-service-caller-key-discovery.md) | accepted | Public-only JWKS discovery, verification helper, and rotation grace for target workloads |
| 278 | [Method and path scopes for service callers](278-method-path-scoped-service-callers.md) | accepted | Target-owned per-caller HTTP method and path-prefix grants checked before routing or wake |
| 277 | [Pinned service-binding handler smoke test](277-pinned-service-binding-smoke-test.md) | accepted | Invoke one explicit service path on an exact live target deployment from the caller over verified HTTPS |
| 276 | [HTTPS-first service-binding transport](276-https-first-service-binding-transport.md) | accepted | Opt-in canonical HTTPS binding URLs with gateway-enforced no-downgrade behavior while preserving the legacy HTTP default |
| 275 | [Caller-side HTTPS service-binding canary](275-https-service-binding-canary.md) | accepted | Verify DNS, certificate trust, authorization, and live endpoint routing from a deployment-attached caller task without waking or invoking the target |
| 274 | [Additive HTTPS service-binding URLs](274-additive-https-service-binding-urls.md) | accepted | Preserve generated HTTP URLs while adding explicit HTTPS canaries for every bound service |
| 273 | [Workload-scoped trust for private service bindings](273-workload-scoped-service-ca-trust.md) | accepted | Per-workload CA bundle, common runtime trust variables, and required `.internal` CA name constraints |
| 272 | [Opt-in private HTTPS for service bindings](272-private-https-service-bindings.md) | accepted | Dedicated private CA, bridge-only TLS listener, and guest CA bundle without changing generated URLs |
| 271 | [Binding-scoped private service aliases](271-binding-scoped-internal-aliases.md) | accepted | Declared bindings gain private `<service>.internal:10080` names without claiming unbound customer DNS or bypassing proxy authorization |
| 269 | [Standalone outbound service bindings](269-standalone-outbound-service-bindings.md) | accepted | Standalone callers declare bounded targets and opt in to enforced outbound policy while legacy apps retain account access |
| 267 | [Standalone app service-caller policy API](267-standalone-service-caller-policy-api.md) | accepted | Create/PATCH target allowlists with replace, deny-all, and reset semantics; project-owned policy remains source-managed |
| 266 | [Target-authorized internal service bindings](266-target-authorized-service-bindings.md) | accepted | New project callers default to declared bindings, targets can restrict callers, and private ingress is available on all plans |
| 245 | [Durable webhooks for platform tenant statements](245-platform-tenant-statement-webhooks.md) | proposed | One signed, retryable tenant event per immutable finalized statement revision |
| 259 | [Expiring revision pins and project release sets](259-revision-pins-and-project-release-sets.md) | accepted | Exact deployment pins and immutable project release graphs across public and durable work |
| 240 | [Cross-app platform tenant request budgets](240-platform-tenant-request-budgets.md) | accepted | Synchronously enforce optional customer-wide minute/day admission ceilings across apps and gateway replicas |
| 258 | [Customer-configurable outbound request policy](258-customer-configurable-outbound-request-policy.md) | accepted | Per-integration rate, burst, concurrency, and timeout controls with plan ceilings and live Postgres-backed enforcement |
| 256 | [Customer-created outbound integrations](256-customer-created-outbound-integrations.md) | accepted | Account-owned public HTTPS destinations, sealed provider Authorization, bounded admission defaults, live gateway resolution, and deletion cascade |
| 262 | [Durable duration waits in declarative workflows](262-durable-workflow-timers.md) | proposed | Atomic timer activation and deadline; no VM during wait; plan-capped horizon |
| 263 | [Authenticated one-time workflow callbacks](263-authenticated-workflow-callbacks.md) | proposed | Per-run callback IDs, account authorization, atomic event/timeout resolution, active-run event retention |
| 264 | [Verified webhook bindings for workflow callbacks](264-verified-webhook-workflow-callback-bindings.md) | proposed | Exact Stripe event/object correlation over existing signed ingress; direct durable callback completion |
| 265 | [Bounded durable workflow condition checks](265-bounded-durable-workflow-condition-checks.md) | proposed | Scheduled checker with persisted next-check time, attempt cap, and timeout branch |
| 239 | [Verified tenant-surface usage attribution](239-platform-tenant-surface-usage.md) | accepted | Bill anonymous requests on verified customer hostnames without conflating them with consumer-key usage |
| 270 | [Bounded API route inventory independent of request audit](270-bounded-independent-api-route-inventory.md) | accepted for initial implementation | Durable, replay-safe 500-route inventory with separate opt-in and audit-independent read API |
| 242 | [Opt-in request audit and observed API inventory](242-request-audit-and-api-discovery.md) | accepted for initial implementation | Exact, opt-in gateway request evidence and bounded discovered-route reads |
| 238 | [Cross-app platform tenant usage statements](238-cross-app-platform-tenant-statements.md) | accepted | Immutable single-currency customer snapshots, late-usage adjustment revisions, and mutually exclusive app/tenant handoffs |
| 261 | [Per-runtime secret reload observations](261-per-runtime-secret-reload-observations.md) | accepted | Fleet status is tracked per active authorized runtime without implying the app applied newly projected credentials |
| 260 | [Application acknowledgement of secret reload](260-application-secret-reload-acknowledgement.md) | accepted | Version-fenced, non-sensitive self-attestation after the app applies the guest-local secret projection |
| 237 | [Provider-neutral customer billing for direct object storage](237-provider-neutral-object-storage-billing.md) | accepted architecture; implementation pending | Keep direct signed URLs; versioned customer meter and rate card; provider cost is separate operator evidence |
| 234 | [Durable consumer-usage delivery](234-durable-consumer-usage-delivery.md) | accepted | Separate fsynced gateway usage outbox and apid receipt from optional debugger; replay by event ID |
| 233 | [Environment-owned declared route contracts](233-environment-owned-declared-routes.md) | accepted | Scoped route-policy replacement, atomic clone, effective diff, and deployment-URL enforcement; OpenAPI fallback remains shared |
| 226 | [Account-level platform tenants](226-platform-tenants.md) | accepted | One end-customer identity across app consumers, hostnames, and raw usage; reversible linked-path suspension |
| 232 | [Loopback service authorization for Safe Deploy](232-loopback-safe-deploy-service-auth.md) | accepted | Separate canary and recovery credentials on APID's loopback operator listener; customer routes remain account-scoped |
| 231 | [Queue push delivery to HTTP functions](231-http-function-queue-push.md) | accepted | Function-only HTTP push binding; serial delivery is explicit until a separate parallel-dispatch design lands |
| 224 | [Account-scoped release webhooks](224-account-scoped-release-webhooks.md) | accepted | Account-owned release receiver over the existing signed delivery ledger; bounded event filter and tenant isolation |
| 223 | [Multiple long-running application companions](223-multiple-long-running-companions.md) | proposed | Bounded helper cardinality with up to four long-running companions, existing dependency/probe gates, and additive resource accounting |
| 221 | [Replay-safe mirror rollups](221-replay-safe-mirror-rollups.md) | proposed | Atomic contribution receipts, UTC hourly buckets, retention safety, and coordinated legacy-writer cutover |
| 222 | [Opt-in in-process secret reload](222-in-process-secret-reload.md) | accepted | App-owned signal handling over an atomic guest-local secret-file projection, limited to single-workload deployments |
| 225 | [Restore working-set prefetch](225-restore-working-set-prefetch.md) | accepted | Record each restore's touched snapshot pages from Firecracker's page table and readahead them at the start of the family's next wake |
| 220 | [Provider-scoped credit receipts](220-provider-scoped-credit-receipts.md) | proposed | Match invoice identity, isolate credit replay and compensation, fail closed on unresolved legacy provider evidence |
| 219 | [Preview-scoped internal service resolution](219-preview-scoped-internal-service-resolution.md) | accepted | Same-account/project/PR workload lookup before the policy-controlled production fallback |
| 218 | [Curated global organization activity timeline](218-global-organization-activity-timeline.md) | accepted | Organization-scoped safe activity projection, stable keyset API, and explicit producer mappings |
| 217 | [Application inbox and outbox facades](217-application-inbox-outbox.md) | accepted | Explicit app-to-app queue delivery and custom signed webhook delivery over the existing invocation and webhook ledgers |
| 214 | [Distributed declarative response caching](214-distributed-declarative-response-cache.md) | accepted | Optional Redis L2, stale-while-revalidate, and shared invalidation for route-level response caching |
| 213 | [Durable unified customer log events](213-durable-unified-log-events.md) | accepted foundation | Append-only tenant log ledger, stable cross-source cursor, redacted projections, and staged producer adapters |
| 211 | [Effective project-environment state and safe diffs](211-effective-environment-state.md) | accepted | Canonical environment snapshot; unified release/config/variable/secret/binding diff; app-global resources explicit |
| 210 | [Runtime secret delivery and status](210-runtime-secret-delivery.md) | accepted | Snapshot-safe restart and version-fenced delivery outcomes for app secrets |
| 208 | [Acknowledged routing handoff for zero-downtime service rollouts](208-zero-downtime-service-rollout-handoff.md) | accepted | Two-phase route publication, serving-gateway acknowledgements, and post-ack per-instance request draining before predecessor retirement |
| 212 | [Durable inbound webhook ingress](212-durable-inbound-webhook-ingress.md) | accepted | Provider-signed Stripe callbacks persist a deduplicated invocation before `202`, then reuse the scheduler wake, retry, and DLQ path |
| 207 | [Bounded builder cache affinity](207-bounded-builder-cache-affinity.md) | accepted | Prefer the latest successful builder briefly so production rebuilds reuse node-local caches without sacrificing availability |
| 216 | [Application companions without exposing an orchestration API](216-application-companions.md) | accepted; cardinality partially superseded by ADR-223 | Preferred companion API, managed presets, task-local shared memory, and rollout-safe primary ingress |
| 200 | [First-wake 5xx auto-rollback on every plan](200-auto-rollback-on-every-plan.md) | accepted | Health-driven rollback for the first wake of a new deployment, on every plan |
| 201 | [Traffic resilience as a platform primitive](201-traffic-as-a-platform-primitive.md) | accepted | `kind=retry` + `kind=circuit_breaker` over instance health, and an nftables egress breaker driven by ADR-098 probe outcomes |
| 215 | [Durable async routes](215-durable-async-routes.md) | accepted | `kind=async` turns a matched public request into the existing durable invocation lifecycle and returns `202` without waking the app |
| 193 | [Transactional per-node RAM reservation](193-transactional-node-reservation.md) | accepted | Invariant §6.2-2 enforced at the instances INSERT; ADR-062 retired NodeLedger's single-process premise |
| 192 | [Wake hot path: single pre-boot staging session and full attribution](192-wake-hot-path-staging-and-attribution.md) | accepted | One loop-mount per wake for drive1 files; Manager.Wake phases and `stage_pre_boot_files_ms` on `wake.restore_breakdown` |
| 190 | [Production BuildKit dependency cache](190-production-buildkit-cache.md) | accepted | Reuse app-scoped Railpack and Dockerfile records across production source edits |
| 186 | [Reusable private-network firewall policy](186-private-network-firewall-policy.md) | accepted | Network-level CIDR baseline layered over provider-neutral private-network reconciliation |
| 187 | [Protocol-aware private-network firewall rules](187-private-network-firewall-rules.md) | accepted | Provider-neutral TCP/UDP/ICMP allow rules with fail-closed private-network enforcement |
| 188 | [Provider-neutral private-network peering](188-private-network-peering.md) | accepted foundation | Canonical two-way route planning for non-overlapping Gregale networks |
| 189 | [Durable private-network peering lifecycle](189-private-network-peering-lifecycle.md) | accepted | Account-scoped peering intent API, pending/error fail-closed status, and deletion guards |
| 184 | [Operator-managed reserved public IP inventory](184-reserved-ip-inventory.md) | accepted | ADR-171 follow-up; durable platform-owned address pool and tenant claim transaction |
| 179 | [Unified app-scoped dead-letter ledger and replay](179-unified-dead-letter-ledger.md) | accepted | issue #1278; additive projection over invocation and trigger dead-letter state |
| 177 | [Durable container host-port leasing](177-container-host-port-leasing.md) | accepted | Node-local, restart-safe listener allocation for declared container ports |
| 175 | [Sidecar scratch and disk-I/O policy](175-sidecar-scratch-and-disk-io-policy.md) | accepted | Customer-selectable ephemeral scratch and guest I/O isolation for sidecars |
| 174 | [Terminal compute-node retirement](174-compute-node-retirement.md) | accepted | operator safety follow-up to ADR-137 |
| 173 | [Plan CPU/RAM coupling](173-cpu-ram-coupling.md): canonical plan RAM/vCPU pairs with an additive create-time assertion | accepted | issue #563; Cloud Run gap analysis |
| 172 | [Public CLI distribution channels: curl installer and npm](172-cli-distribution-channels.md) | accepted | the CLI had no install path; proprietary license rules out homebrew-core/nixpkgs |
| 171 | [Disposable one-shot executions from sanitized runtime snapshots](171-disposable-one-shot-executions.md) | proposed | caller-supplied code execution gap; runtime-snapshot foundation |
| 168 | [Temporary cold-boot CPU allowance](168-cold-boot-startup-cpu.md) | accepted | SSD-node micro-profile cold-boot measurements; issue #1668 |
| 165 | [Remote snapshot memory compression](165-remote-snapshot-memory-compression.md) | proposed | SSD-node snapshot publication measurement; reader-first OCI wire-format rollout |
| 158 | [Provider-neutral resumable multipart object uploads](158-provider-neutral-multipart-uploads.md) | accepted | Large-object and interrupted-upload hardening for S3 preview |
| 164 | [Container workload networking v1](164-container-workload-networking.md) | accepted | Loopback endpoint contract for main and sidecar workloads |
| 167 | [Container cross-VM service endpoint registry](167-container-cross-vm-service-discovery.md) | accepted | Loopback projection of the gateway's multi-node replica target cache |
| 168 | [Container node-local service proxy](168-container-node-local-service-proxy.md) | accepted | Same-account service-name routing over the existing per-node vmmd bridge |
| 169 | [Container guest service proxy identity](169-container-guest-service-proxy.md) | accepted | Tenant-bridge listener with HostIP caller binding and netns admission |
| 166 | [Container cross-drive whiteouts](166-container-cross-drive-whiteouts.md) | accepted | Preserve OCI deletions in the optimized two-drive upper filesystem |
| 167 | [Wildcard custom domains](167-wildcard-custom-domains.md): Pro/Scale-only customer-owned `*.zone` rows, zone-level DNS-01 verification, most-specific suffix routing, and typed 409 exclusion with ADR-100 tenant-surface hostnames | accepted | issue #1397 F4 |
| 158 | [Per-action CSRF cookies on multi-form dashboard pages](158-dashboard-multi-form-csrf.md): additive named-cookie issue/verify helpers; first consumer is typed-confirmed dashboard API-key revocation | accepted | issue #248 slice A; establishes the secure pattern for plan change and deployment rollback |
| 159 | [Dockerfile developer cache parity](159-developer-dockerfile-cache.md): extend the disposable tenant/workspace-scoped BuildKit cache to `gregale dev` Dockerfile builds | accepted | `gregale dev` custom-build latency follow-up to ADR-153 |
| 157 | [Named container resource profiles](157-container-resource-profiles.md): stable micro-to-xlarge RAM/CPU shapes mapped to existing cgroup enforcement | accepted | Container predictability milestone |
| 156 | [Direct object storage accounting and safety budgets](156-object-storage-accounting.md) | accepted | S3 accounting plus default-off Polar month-close billing; live provider qualification remains a launch gate |
| 157 | [Developer config parity](157-developer-config-parity.md): explicit `gregale dev --env-file` secret sync with key-only output and archive exclusion | accepted | `gregale dev` DX follow-up to ADR-156 |
| 155 | [Provider-neutral managed PostgreSQL](155-provider-neutral-managed-postgres.md): account-owned databases, app-scoped bindings, durable placement, lifecycle reconciliation, and canonical usage meters | foundation accepted; preview pending | Managed PostgreSQL foundation; provider qualification, billing, and recovery remain launch gates |
| 492 | [Validate the Neon consumption contract](492-managed-postgres-consumption-contract.md) | accepted | Correct byte-month normalization, require complete usage coverage, and reconcile prior ledgers |
| 500 | [Managed PostgreSQL provider rate-limit cooldowns](500-managed-postgres-provider-rate-limit-cooldowns.md) | accepted | Honor retry guidance, isolate consumption throttling, and preserve canceled response reads |
| 623 | [Durable managed PostgreSQL compute resizing](623-managed-postgres-durable-compute-resizing.md) | accepted for operator preview | Persist class-only resize intent, preserve data and credentials, and recover uncertain provider updates |
| 624 | [Durable managed PostgreSQL idle policy changes](624-managed-postgres-durable-idle-policy.md) | accepted for operator preview | Change scale-to-zero through the shared fenced compute journal and qualified provider contract |
| 677 | [Independently mapped historical positions for PostgreSQL restore](677-managed-postgres-historical-restore-proof.md) | accepted; provisioning gated | Verify Neon timestamp recovery against the observed parent WAL position, retain restart inspection, and require lineage in qualification |
| 625 | [First-wake 5xx auto-rollback evaluated by apid](625-first-wake-5xx-rollback-from-request-telemetry.md) | accepted | Evaluate opt-in post-release rollback from per-deployment request telemetry and preserve readiness-gated rollback semantics |
| 627 | [Prospective gateway object-storage safety accounting](627-prospective-gateway-storage-safety-accounting.md) | accepted for operator preview | Use durable gateway meters and complete inventories for admission with billing off and explicit unknown costs |
| 628 | [GCS tracked writes and native generations](628-gcs-tracked-writes-and-native-generations.md) | accepted for local qualification | Recover native receipts, fence generation copies and deletes, and stream CLI transfers |
| 631 | [Reclaim unowned layer clones and tenant cgroups, and retry failed teardowns](631-unowned-layer-clone-reclamation.md) | accepted | Reap clones and empty tenant cgroup scopes no Manager, journal record or live row owns, in cache buckets and /srv/fc/base; retry retained teardowns |
| 632 | [Converge cached runtime bases](632-converge-cached-runtime-bases.md) | proposed | ADR-567 convergence also covers runtime bases an earlier daemon cached, not only the ones this process staged |
| 633 | [Snapshot drive layer block sharing](633-snapshot-drive-layer-block-sharing.md) | proposed | Snapshot drives share their app layer's unchanged blocks and the node cache counts shared blocks once, so a node holds ~2.4x more snapshots |
| 154 | [Disposable developer source deltas](154-developer-source-delta.md): changed-entry transfer with full-archive reconstruction and automatic full fallback | accepted | `gregale dev` DX follow-up to ADR-153 |
| 153 | [Developer BuildKit dependency cache](153-developer-buildkit-cache.md): tenant/workspace-scoped Railpack cache across ephemeral developer builder VMs | accepted | `gregale dev` rebuild latency |
| 152 | [Configurable sustained CPU per app](152-configurable-app-cpu.md): 250m, 500m, and 1000m cgroup quotas with configured/effective API visibility | accepted | Cloud Run gap analysis |
| 151 | [Provider-neutral customer object storage](151-provider-neutral-object-storage.md): opt-in managed S3 buckets with durable placement and provider-neutral APIs | accepted for preview | S3 preview milestone; public launch pending live backend and billing qualification |
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
| 046 | Per-instance egress metering with default-off Polar billing | accepted, amended 2026-09-12 | canonical net_tx_bytes + meter-qualified delivery |
| 050 | Repo decomposition: `projects` object + multi-workload auto-provision | proposed | `docs/repo_decomposition_implementation.md` |
| 051 | Characterization boot: observed workload classification + in-guest port normalization | accepted | ADR-050 Phase 4 |
| 052 | Adding a function runtime: 7-layer additive procedure | accepted | Tier 1 PR 1+2 worked example |
| 053 | Deploy-time overrides for OCI image deploys (entrypoint/cmd/env/port/healthcheck) | accepted | issue #460 (PR A ships contract; PR B imaged layer injection; PR C port plumbing) |
| 059 | Customer-configurable scaling policy (persistence, inflight signal, cooldown, worker carve-out, and bounded warm-saturation queue) | proposed; amended 2026-09-22 | issue #462 / PR #493 / #501 / #507 / #512; warm-queue amendment |
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
| 076 | Outbound webhook delivery reliability (`app_webhooks` + `app_webhook_deliveries` tables; schedd `pkg/webhook.Dispatcher` goroutine; 5s tick + 32/tick cap; rotating per-account claim fairness; DLQ at attempt 7; default / aggressive / none retry policies; sealed HMAC secret with namespace `APP_WEBHOOK`; `X-Faas-Webhook-*` headers distinct from `X-Faas-Alert-*`; 5 audit kinds) | accepted | issue #476 (parallel outbound surface — does NOT extend `alert_deliveries`; clock injection via dispatcher struct fields makes the 7.5h DLQ path testable in ≤1s wall; migrations 00140 + 00141 with fence at 00139; ADR-041 slot fence pattern) |
| 080 | Raw-bytes bridge for Upgrade traffic over the gatewayd-internal → vmmd → guest path (WebSocket / h2c / long-poll / MQTT-over-WS) — new `Vmmd.ForwardRawStream` gRPC bidi + `cmd/vmmd-raw-bridge` Go bridge binary (273 lines, `unix.Setns` → `net.Dial` → two `io.Copy` goroutines) + gatewayd-internal three-input detector (`isUpgradeRequest(r) && app.WebSocketEnabled && h.rawByNode != nil`) + 3-line public-hop preservation in `internal_proxy.go:228`; per-app `apps.websocket_enabled` flag (Free false, Hobby+ true via `Plan.WebSocketEnabled()` / `Plan.WebSocketResponseAllowed()`); 100 MiB per-request cap (`api.RawStreamMaxRequestBytes`); `x-faas-upgrade: true` observability header; `evts.Platform` `ProxyFirstByte` events surface (ADR-064); per-app PATCH rollback only (no daemon-level kill switch in merged PR-3 — follow-up issue); follow-ups: `FAAS_GATEWAY_RAW_STREAM_ENABLED` env var, `gateway_ws_*` Prometheus series, per-session byte cap meter | accepted | issue #676 (PR #694 ForwardRawStream wire + vmmd-side handler merged 2026-08-06; PR #702 gateway detector + raw forwarder + e2e merged 2026-08-07 — bundled original PR-3 + PR-4 per user decision) |
| 082 | Per-app customer-facing SLO surface (issue #696 / move-2 PR-A) | accepted | issue #696 |
| 083 | Active-passive HA topology (Tier A8 / §14 M8 / issue #297 slice 6) — lex-min leader election over `compute_nodes.name WHERE active=true`; `pkg/gateway/leader` package (`ElectLeader`, `Leader`, `LeaderStore`); `StandbyState` gauge (`<prefix>_gateway_standby_state` enum: warming=1, warm=2, draining=3) + `ActivePassiveFailoversTotal` counter (`<prefix>_gateway_active_passive_failovers_total{outcome}` for dns_flipped|dns_stale|peer_unreachable|manual_drain); standby warm-up via bounded `cmd/gatewayd-internal` HTTP HEAD scrape (timeout = `HAFailoverProbeTimeoutMS = 500` ms in `pkg/api/limits.go`); drain protocol bounded by `HADNSRecordStaleSeconds = 30`; Hetzner DNS provider (`FAAS_DNS_PROVIDER=hetzner` + `HETZNER_DNS_API_TOKEN` sealed via `pkg/secretbox.SealBytes` namespace `DNS_PROVIDER`) + operator-managed fallback (`FAAS_DNS_PROVIDER=manual` prints the required `curl` to stderr); `make ha-failover-drill` Makefile target on two-node Lima fleet (`deploy/lima/faas-metal-2node-ha.yaml`); two-host property test in `tests/property/concurrency_test.go` (issue #297 acceptance item 5); standalone runbook `docs/runbooks/active-passive-ha.md`; PR-cluster shape PR-A refactor / PR-B functional / PR-C test+deploy | proposed | Tier A8 (post-Tier-A5 multi-box HA; closes §14 M8 "Gate-A runbook (2nd box active-passive)" row) |
| 084 | Traffic splitting — picker signal + largest-remainder redistribution + wake fan-out (issue #556 PR-C) — `PGBackend.Pick` returns `PickResult{Target, OK, Picked, ColdBucket}` so the handler can wake the cold bucket outside the read-lock critical section; `UpdateDeploymentTraffic` uses largest-remainder (Hamilton's method, tie-break fraction DESC + ID ASC, integer arithmetic, Σ=100 by construction) instead of zeroing siblings; `Backend.Admit(ctx, appID, deploymentID, max)` widens the wake signature so the handler can route Phase-2 admits to the deployment the picker landed on; bounded 1 retry per request; notify emit on `updateDeploymentTraffic` with `kind:"traffic"` (defect S1) + `pg_notify` payload widens additively; bonus cleanups (implicit-100 synthesis narrowed to `len(weights)==0`, stale-set pruning in `RefreshDeploymentWeights`, Phase-1 fast path reads `instances.deployment_id`, full WakeResult in `admitAndDispatchForDeployment`, vacuous `TestPg_UpdateDeploymentTraffic_ZerosSiblings` rewritten, gate-order doc fix); `--redistribute` flag deferred to PR-D | accepted | issue #556 PR-C (slot 084 — 082/083/085 already taken; no schema change; 12 new pinned tests + 4 memstore mirrors; closes acceptance items #1 + #2 + #3) |
| 089 | Per-secret rotation (PR-B) + background re-seal walker (PR-C) — `app_secrets.kid text` column + `pkg/rekey.Replayer` skeleton (pinnedCursor + in-Run seen-set for crash-safe walk); `POST /v1/apps/{slug}/secrets/{key}/rotate` handler re-seals one row under the current identity and stamps `kid`; `FAAS_REKEY_ENABLED=true` opt-in for the background walker in `apid` (separate `audit.Auditor` instance with actor="rekey" so dashboards filter background re-seals from user-driven rotates); `GET /v1/admin/secrets/rekey-progress` operator endpoint (admin scope + email allowlist) returning the latest `rekey.RekeyProgress` snapshot; on-disk persistence at `FAAS_REKEY_PROGRESS_FILE` (default `/var/lib/faas/rekey-progress.json`, mode 0o600, atomic rename) for crash-safe resume; `pkg/api.CodeRekeyDisabled` 503 sentinel when the flag is unset; 7 unit tests + 2 e2e (single-daemon rotate + box-test full lifecycle) | accepted | ADR-089 PR-A/B/C (PR-A+B shipped via PR #800; PR-C is this PR — slot 089 — slot 088/090 reserved) |
| 093 | Opt-in per-route observability inside an app (gatewayd-internal) — `apps.route_metrics_enabled` boolean + plan gate (Free off, Hobby+ on) + operator kill-switch (`[route_metrics] enabled` in `cmd/gatewayd-internal/config.go`); bounded per-app route cap (50) with `__route_other__` non-evicting overflow; `gateway_request_duration_seconds{app,route,class}` + `gateway_requests_total{app,plan,route,code}` + `gateway_request_failures_total{app,plan,route,code}` Prometheus series; bounded in-memory reader `GET /v1/internal/apps/{slug}/routes` on the loopback control listener, reverse-proxied by apid as `GET /v1/apps/{slug}/routes`; route label = method + raw path (pre-rewrite, `WithRouteLabel` stash on `r.Context()`); new `pkg/gateway/route_label_set.go` mirroring `account_label_set.go`; new `pkg/gateway/control_routes.go`; recording rule `faas_gateway_request_rate_5m:by_route` + `GatewayWildcardRoute` info-severity alert for sustained `__route_other__` overflow; partially supersedes ADR-042 §1 (the per-app `{app, class}` histogram and `cold_boot` rename are preserved verbatim); fixes pre-existing spec §12:780-786 ADR-041→ADR-042 mis-cite. | accepted | issue #273 (customer-facing per-route intent) |
| 095 | PR-preview environments (issue #272) — bridge child deployment → ephemeral `preview_app` row (parent_app_id + preview_pr_number + preview_sha); `app_envs` shadowed by `app_preview_envs` for the preview lifetime; webhook decoder + `Service.handlePullRequest` + Checks API create-on-push + teardown-on-close; per-account preview concurrency cap (`plan_previews_table`); preview URL `https://pr-{N}--{slug}.{domain}`; `pkg/githubd/checks.go` writes `check_run` status; CLI surface deferred. PR-A spine shipped (PR #851); PR-B routing + PR-C teardown in flight. | proposed | issue #272 (PR-A spine shipped in PR #851; PR-B routing + PR-C teardown in flight; slot 095 — coexists with ADR-098 which renumbered 095→098 post-merge to avoid the slot 095 collision; both ADR files at `docs/adr/095-*.md` and `docs/adr/098-*.md`) |
| 097 | schedd wake-phase telemetry — `schedd_wake_rpc_duration_seconds{app, phase}` HistogramVec on schedd, closed-set `phase ∈ {admit_to_rpc, rpc_call, rpc_to_running, resume}`; spec §6.3 buckets + low-end `0.01` for `admit_to_rpc`; `wake_id` attached as `prometheus.Exemplar` (no label cardinality cost) so operators join to the `events` table and to `gateway_wake_latency_seconds` on the gateway side; the `resume` phase covers in-place warm-pool promotion; spec §6.3 paragraph + §12.1 row updated; mirrors the existing `schedd_guest_init_duration_seconds{app, runner}` pattern at metrics.go:1172-1177. Renumbered 093 → 096 → 097 to avoid collision with ADR-093 (per-route-app-metrics, issue #273 / PR #861) and ADR-096 (customer-facing error grouping, PR #863 PR-A — slot reserved for PR-C's `docs/adr/096-customer-error-grouping.md`). | accepted | P1B (P1 autoscaling PR-cluster: P1C + P1A + P1B; ADR-097 ships as commit 1 of the cluster) |
| 098 | Wake single-flight coordinator on `sched.Engine` — `pkg/sched/wake_coord.go` (NEW) mirrors `pkg/gateway/gate.go` state machine (`done`/`waiters`/`completed`) but lives on the engine; leaf-lock rule `wakeCoord.mu` acquired+released **before** `e.lockApp(appID)`; new additive `schedd.EnsureWake` gRPC method (`api/proto/onebox/faas/schedd/v1/schedd.proto`, mirror WakeResponse tag layout 1–7); all five wake producers (gateway, cron `loop.go:1969`, floor `floor/trigger.go`, scaleup `scaleup/trigger.go`, targets `targets/trigger.go`) route through `Engine.EnsureWake`; `pkg/gateway/WakeGate` retains its role as in-process pre-filter (a cache in front of the authority); one `defer` closure at leader entry + `sync.Once` `finish()` covers all five completion sites (`engine.go:1435, 1818, 1823, 1830-1831, ~1892`); new `pkg/db.NotifyAppDelete` pg-notify channel + `pkg/sched/app_delete_subscriber.go` (modeled on `pkg/sched/deletion_subscriber.go`) calls `Engine.wakeCoord.Forget(appID)`; detached-ctx contract on leader's `ensure` (`context.Background()` + `WakeQueueTTLSeconds=30` at `pkg/api/limits.go:1567`); 4 invariants preserved (cold boot truth §4.6, wake never depends on snapshot ADR-005, identical inner net ADR-009, admission ceiling §6.2-2); migration 00221 adds `instances.request_count BIGINT NOT NULL DEFAULT 0` for the warm-snapshot 5th promotion gate (gate #5 at `engine.go:3876`, between MinMs `:3870-3875` and `warmKeysFor` `:3880`) | accepted | PR #854 (slot 095 collision with PR-preview #851 / issue #272 → renumbered 095 → 098 post-merge; supersedes nothing; closes the §6.2-1 single-flight correctness gap that ADR-070 introduced for the gatewayd-public/gatewayd-internal split; complements ADR-074's warm-snapshot ops close-out by adding the missing per-app request-count gate) |
| 100 | Tenant surfaces: multi-tenant hostname routing under one Gregale-managed cert (issue #879) — `tenant_surfaces` + `tenant_hostnames` tables; one surface binds to one app (D1) and groups N verified hostnames under one cert; `cert_kind` ∈ {'per_host_san' (v1 default), 'per_host' (fallback), 'shared_wildcard' (deferred follow-up ADR)}; `CertIssuer.RequestCertForSurface` re-mints on every surface mutation (D3); routing branch in `pgRouter.ResolveHost` ordered `slugFor` → `SurfaceByHostname` → `DomainByName` (D4); quota in `pkg/api/limits.go` only — `TenantSurfacesPerAccount` (Free 0 / Hobby 1 / Pro 5 / Scale 25), `TenantHostnamesPerSurface` (10/50/250/1000), `TenantSurfacesAllowed` bool (D5); reverses ADR-028 line 182-184 "Multi-tenant gatewayd routing policies" deferral for the SaaS surface model; PR-cluster outlined in `docs/adr/100-pr-cluster-outline.md` (PR-0 docs+fence+limits+CLI typo / PR-A schema+state+cert engine / PR-B surface parser+routing / PR-C HTTP API+CLI+E2E); feature-flagged `FAAS_TENANT_SURFACES_ENABLED` (default OFF through v1.10) | proposed | issue #879 (slot 100 free per precheck after ADR-099 was claimed by the jobs ADR; PR-0 fence at `migrations/00238_reserve_slot.sql`, PR-A target at `migrations/00243_tenant_surfaces.sql`; supersedes nothing; closes the SaaS-segment gap that the Cloudflare / Vercel / Netlify benchmarks treat as a primitive) |
| 101 | Imaged-layer secret scan: post-build OCI secrets gate (PR-A of the secret-scan cluster, follow-up to PR #873 / secret-scan v2) — `pkg/imaged/secretscan.go` + `pkg/imaged/handler.go::runDeployLayerSecretScan` + `WithSecretScanRun` setter mirrors `WithGrypeRun`; loud-fail posture (D1) mirrors `errStatefulViolation` (G13 closure) — `errImageSecretDetected` sentinel + `markDeployFailed` + error_code free-text `'image_secret_detected'` on the unconstrained `deployments.error_code` column (D7); placement post-`SetDeploymentRootfs` pre-`pending→snapshotting` (D2) reuses `stageScanExt4`; reuses v2 columns (D3) — `secret_findings jsonb` + `secret_scanned_at timestamptz` already on the row from migration 00264; sidecar coverage scans each sidecar ext4 (D4) with `layer="sidecar-<slug>"` label; functions NOT scanned (D5) — already scanned at apid source-tree time; v2 apid-side audit-row gap NOT closed (D6) — structural (422 fires BEFORE CreateDeployment) and deferred to a follow-up with a new schema; new wire surfaces — `GET /v1/deployments/{id}/secret-scan` (drill-down, 404 on IDOR + scan-pending), `DeploymentResponse.SecretScan *SecretScanResult`, `SecretFinding.Layer`, `SecretScanResult.ImageDigest`, `CodeImageSecretDetected`, `pkg/api.GetDeploymentSecretScan` SDK method, `gregale deployment <id> --show-secret-scan` flag (D8 mirrors `--show-scan`); ZERO migration (free-text error_code + v2 columns); zero-caller `UpsertDeploymentSecretFindings` seam becomes the load-bearing audit-row writer for the imaged side | proposed | issue #873 follow-up (the v2 next-direction handoff); no new migration; supersedes nothing; closes the build-step adversary pivot that v2 source-tree scanning couldn't reach |
| 104 | Dimensional throttle keying (issue #881 Phase 3 + amendment 6) — per-rule opt-in `key_by ∈ {"none","api_key","consumer_id","jwt_subject","jwt_claim","country","ip"}` extends `EdgeRuleThrottleAction` with bounded request-concept buckets; `missing_key_policy ∈ {"shared","reject"}` prevents credential omission bypass; verified scalar JWT claims are available independently of `required_claims`; central mode maps dimensions into bounded deterministic UUID shards without storing raw claims or widening `pg_ratelimit_counters`; local mode retains the non-evicting `__other__` collapse; no DDL because the action is jsonb and central subjects remain UUIDs | accepted | issue #881 Phase 3; amendment 6 (2026-09-22) enables country, arbitrary scalar JWT claims, strict missing identities, and cross-replica dimensional enforcement; amendment 9 (2026-10-08) adds the trusted client-address dimension with IPv6 /64 bucketing |
| 110 | Declarative split-box deployment manifest (issue #911) — versioned YAML at `deploy/manifest/splitbox.yaml` + typed schema at `pkg/manifest/`; SemVer schema_version (1.0.0); canonical validation path consumed by the validator (`gregalectl manifest validate`), the renderer (PR-2), the release bundle installer (PR-3), the doctor (PR-4), and the metal harness (PR-6); TOML table-placement catalog at `pkg/manifest/toml_check.go` is the source of truth for which key belongs to which table (closes the duplicated `tls_*_path` inside `[compute_node]` bug at `deploy/ansible/roles/vmmd_service/files/vmmd.toml.example` lines 33-40 — the canonical top-level `tls_cert_path` / `tls_key_path` / `tls_ca_path` cluster); PR-0 reserves migration slots 00266 + 00267 (no-op bodies) for PR-3a; `cgroups.controllers` must include `memory` (issue #911 load-bearing invariant); `deploy/controlplane/bootstrap.sh` retired in PR-1 (Phase 1 tombstone 2026-08-15; Phase 2 deletion after PR-X `gregalectl secrets init` lands); PR-cluster outlined in plan file `/Users/poyrazk/.claude/plans/crispy-hopping-sphinx.md` (PR-0/3a/5/4/2/1/3/X/6); scale-out tier-1 residual (PR #937) extended the catalog with `compute_node.{host_bridge_cidr, overlay_cidr, overlay_interface}` (Gaps #3 + #5) and added the `egress.danger_accept_rfc1918_lateral_movement` + `egress.overlay_exceptions` manifest-level knob (Gap #4) | accepted (revised 2026-08-16) | issue #911 (PR-0 fence at `migrations/00266_reserve_slot.sql` + `migrations/00267_reserve_slot.sql`; PR-3a target at `00266_compute_nodes_release.sql` + `00267_release_bundles.sql`; supersedes nothing; closes the GCP split-box drift that the 10-hour live debugging session exposed) |
| 111 | Gregale Compute Image (issue #911 post-cutover) — versioned, immutable, per-cloud Packer-built host image named `gregale-compute-{role}-{fc_release}-{kernel_version}-{git_sha}` (`{role}` ∈ `control-plane\|compute-only` per ADR-092); content-addressed by tag; first-boot user-data runs the cutover runbook's 6-step init chain (`gregalectl {pki,host-age,sign-keys,node-key,backup} init` + `release install --git-sha $MANIFEST_GIT_SHA`) idempotently; rollout (`make upgrade-node IMAGE_TAG=…`) is gated per-daemon by the existing `Lifecycle.Probe`/`ProbeTarget` health gate consumed by `gregalectl doctor --deep` (PR #921 / ADR-110 PR-4); `make bootstrap*` stays as the installer path for dev boxes, CI, and the image-seed build; image content is immutable from the operator's perspective (no sealed.env / host.age / TLS leaves / cosign keys baked); PR-cluster outlined in plan file `/Users/poyrazk/.claude/plans/crispy-hopping-sphinx.md` (PR-0 #927 ADR + PR-1 #928 Packer scaffolding + PR-2 #929 hcloud/amazon-ebs builders + PR-3 #930 first-boot user-data + PR-4 #931 upgrade-node rolling + health-gate) | proposed | issue #911 post-cutover (closes 3 of 10 tier-1 ship-blockers from the M8 launch-readiness audit; zero migration; zero new schema; supersedes nothing; closes the `apt install firecracker` + `make bzImage` drift class that Mega-PR-C closed 10 instances of) |
| 115 | Transactional email provider — choose Resend (spec G4 closure) | proposed | spec §17 G4 (`docs/faas_implementation_spec.md:1231`); selection + fail-closed boot contract + `FAAS_MAIL_*` rows in `sealed.env.example`; default flip to `resend` held back to ADR-116; rotation / webhooks / HTML headers / DNS automation deferred to ADR-117/118/119 |
| 119 | Static outbound IP per app (Scale-only, BYOIP, single-node v1) — `apps.static_egress_ip inet NULL` + `apps.static_egress_ip_set_at timestamptz` (additive, family=4 CHECK, partial unique index `apps_static_egress_ip_key` to defend against alias-IP collision on `br-tenants`); per-VM `pkg/netns.Config.AccountStaticIP` + per-netns `postrouting oifname <VethPeer> ip saddr 10.0.0.2 snat to <CustomerIP>` sibling emitted AFTER the existing MASQUERADE; host `pkg/netns.HostPolicy.AccountStaticEgressIPs map[string][]netip.Addr` + per-account `ip saddr <per-vm-host-ip> oifname <PublicIface> snat to <customer-ip>` MASQUERADE siblings in `chain postrouting` AFTER the existing `10.100.0.0/16` MASQUERADE; new `pkg/fcvm/alloc.go::AcquireStaticEgressIP` reserves a per-VM host IP from the bridge /16 + persists to `/etc/faas/egress/static_egress_ips.toml`; new `cmd/vmmd/egress_static_ip_bundle.go` mirrors `cmd/vmmd/egress_bundle.go` SIGHUP-driven reload pattern; new wire field on `AppSpec` (proto field 8) + `UpdateStaticEgressIP` gRPC for live-instance drift; new `FAAS_STATIC_EGRESS_IP_ENABLED` env flag (default OFF, mirrors `FAAS_TENANT_SURFACES_ENABLED`); new per-plan limits `StaticEgressIPAllowed` (Scale true, others false) + `StaticEgressIPsPerApp` (Scale 1, others 0); new RFC 7807 codes `plan_static_egress_ip_not_allowed` (402) + `plan_static_egress_ip_quota` (403) + `app_static_egress_ip_invalid` (400, includes RFC1918/link-local/multicast/CGN/loopback rejection via `pkg/netns.ValidateCIDRsAgainstDenySet` reuse); new `gregale app security static-egress-ip {show,set,clear}` CLI surface; new apid endpoints `GET/PUT/DELETE /v1/apps/{slug}/static-egress-ip` + dashboard card; spec §11 gains a paragraph on the customer-supplied static IP path (metadata-range deny unchanged); multi-host placement pin / IPv6 / platform-owned pool / Paddle-Stripe add-on billing are explicit follow-up ADRs (NOT in v1) | proposed | worktree-feat-static-outbound-ip (slot 00325 at `migrations/00325_apps_static_egress_ip.sql` + 00326 cross-PR follow-up fence at `migrations/00326_reserve_slot.sql`; renumbered 00303→00307→00308→00325 after main consumed 00303-00304 + open PRs #984/#990/#991/#999/#1000 claimed 00305-00324; supersedes nothing; closes the B2B allowlist gap that Vercel/Cloudflare/Fly/Railway treat as a primitive) |
| 120 | Domain doctor (issue #961 follow-on) — new `domain_doctor_observations` table persisted by the existing `dns_poller`; new `GET /v1/domains/{domain}/doctor` endpoint + `gregale domains doctor <domain>` CLI subcommand rendering 5 Render-style checks (DNS found / points to Gregale / TLS / CAA / IPv6 conflict) with the exact record to change; `api.DomainDoctorEnabled()` env flag gates the poller branch; cert engine remains the SOLE writer of `tenant_surfaces.cert_state` (doctor reads only); migration `00313_domain_doctor_observations.sql` (renumbered 00296 → 00309 → 00305 → 00308 → 00309 → 00313 after main consumed 00296-00304 + open PRs #984/#992 claim 00305 + PR #997 fences 00306-00307 + PR #999 owns 00308 + PR #990 owns 00309 (app_secret_value_hash) + fences 00310-00312 + PR #1000 owns 00309 (consumer_keys), so 00313 is the first free slot above all open PR fences; cross-PR slot precheck CI (memory cross-pr-slot-precheck-pr-867-collision) enforces this discipline — every renumber hop is one CI round-trip); `FAAS_DOMAIN_DOCTOR_TTL_SECONDS` env (default 300) bounds the synchronous re-probe path; mega-PR per `~/.claude/plans/zesty-sparking-quasar.md` | proposed | issue #961 (PR-3 surface exposed the gap: 1.5 of 5 Render-style checks present, zero `CAA` / `LookupAAAA` hits repo-wide, cert status reconstructed on every request rather than persisted; supersedes nothing; closes the activation drop-off the verify-only 422 path left open) |
| 140 | Proof of presence for `POST /dashboard/account/set-password` — handler-chosen proof (fresh step-up · `current_password` · session for OAuth-only/no-MFA) replaces the blanket ADR-077 `requireStepUpHandler` mount that locked out OAuth-only customers without MFA and never consulted the existing password; `SetPasswordRequest.current_password` (optional) + required `csrf_token` (same-site form POST; `set_password` joins the `/v1/auth/csrf` allowlist); audit kinds `account.password_set` / `account.password_set_denied` | proposed | console set-password wizard (faas-web) exposed the 403; supersedes the ADR-077 routes-table row for this path |
| 122 | Safe releases (issue #976) — orchestrator in **meterd** (5s tick goroutine at `pkg/meterd/release_orchestrator.go`), `deployments` is the unit of revision (no separate `revisions` table — `traffic_split_percent` + `target_deployment_id` + `status='superseded'` already encode every revision semantic), canary presets gated on Pro+ via `Plan.CanaryPresetAllowed()` (inherits `TrafficSplitAllowed` gate from ADR-084), audit + diff retention = 90 days (matches spec §4.7 GB-h floor), preview URL shape `deploy-{N}-{slug}.gregale.dev` (target cert-wildcard shape, NOT legacy `apps.gregale.dev`); ships as two mega PRs (Mega PR #1 = E.2 + C + D foundation: deployment_audit table + preview URL + openapidiff gate; Mega PR #2 = A + B + F headline: canary presets + auto-rollback on first-N-5xx + meterd orchestrator); Mega PR #1 starts at migration `00332_deployment_audit.sql` (renumbered through 6 hops `00320 → 00322 → 00324 → 00326 → 00327 → 00330 → 00332` to clear open-PR slots #999 #990 #1004 #1000 + post-merge of PR #999's `00326_apps_public_auth_ip_allowlist.sql` on origin/main + the round-4 fence-collision on main's `00327_reserve_slot.sql` + `00328_reserve_slot.sql` fences + main's `00329_consumer_keys.sql` real migration + the round-5 collision with open PR #1005's `00330_api_contract_diff_pr_a.sql` real migration); spec §6.4 amendment 2 (post-deploy failure actions owned by meterd, not apid handlers) lands in Mega PR #2 | proposed | issue #976 (closes the 5-gap SAFE-RELEASES audit: per-emit-not-per-deployment audit trail, `app --pr N` only preview, silent schema drift, manual-only rollback, sampled-not-orchestrated health signals; supersedes nothing; Mega PR #1 plan = `~/.claude/plans/parallel-snuggling-wall.md`) |
| 140 | Public-release backend hardening foundation — admission-only warm affinity, stale residency fail-closed, and cancellation-safe stream teardown | proposed | public release path; see ADR-140 |
| 141 | Durable imaged→apid audit-event delivery (`audit_event_outbox`; deployment-scoped dedupe; transactional `events` insert; 2s replay worker; capped retries + dead-letter; 90d queue metadata retention; legacy `pg_notify` fallback) | accepted | public-release backend hardening; migration 00590; issue #472 / ADR-058 |
| 142 | Timestamp migration IDs + post-cutover out-of-order apply; freezes legacy 1–590 and retires slot reservations | accepted | migration concurrency; supersedes ADR-041 for new migrations |
| 146 | [OCI platform selection](146-oci-platform-selection.md): shared Linux/amd64 resolution and immutable source/child references | accepted | container compatibility; index-aware image preflight and deployment |
| 147 | Full-rootfs fallback for arbitrary OCI images: paid-plan all-layer ext4 assembly, marker-based direct pivot, bounded named-user lookup, and direct-root sidecars | accepted | container compatibility; migration `20260905110000000_deployment_full_rootfs_columns.sql` |
| 157 | [Provider-neutral object-storage access grants](157-object-storage-access-control.md): separate storage manage/read/write scopes plus explicit per-bucket API-key grants; rotation inheritance; no provider-native credentials | accepted | object-storage application access; migration `20260905200000000_object_storage_access_control.sql`; extends ADR-151 and ADR-156 |
| 158 | [Explicit ephemeral disk boundary](158-ephemeral-disk-boundary.md): expose the existing plan-capped writable `drive1` capacity as `ephemeral_disk_max_mb` while retaining `app_layer_max_mb` compatibility; no persistent volumes or second quota source | accepted | container runtime storage contract; no migration |
| 144 | Zero-config workspace build context — explicit `--path` workspace members upload repository context and persist `source_root`; builderd/guest-init build from the selected nested directory | accepted | zero-config deploy follow-up; ADR-086/088/090 |
| 422 | [Bare-metal service recovery capacity](422-bare-metal-service-recovery-capacity.md) | accepted | Durable, atomic one-host recovery headroom and operator certificate |
| 426 | [MCP hosting contract](426-mcp-hosting-contract.md) | proposed | Stateless MCP starter, deployment verification, client OAuth and diagnostics on the app lifecycle |
| 1140 | [Pull leases for worker queue consumers](1140-worker-queue-pull-leases.md) | accepted | `POST /v1/apps/{slug}/queues/{queue}/leases[:ack\|:nack\|:extend]` with fenced receipts and the `queues:consume` scope or a `gregale:queues` workload token, long-poll `wait_seconds` and up to 100 messages per lease, for workers without an HTTP listener; a worker holding leases ranks busy at scale-in |
| 1141 | [Workload-identity trust provisioning](1141-workload-identity-trust-provisioning.md) | accepted | `gregalectl workload-identity init\|rotate\|status` writes the cluster RSA key, key id and JWKS; `faas_workload_identity_source_dir` installs the key on compute nodes (vmmd drop-in) and the JWKS for apid (`workload_jwks_path`, the fallback for Flags and Operations); `vmmd_workload_identity_tokens_total` and `FaasWorkloadIdentityNotConfigured` |
| 1150 | [Per-plan request timeout ceilings](1150-per-plan-request-timeouts.md) | accepted | `Limits.RequestBudgetMaxMs` per plan (Free 30 s, Hobby 1 min, Pro 5 min, Scale 15 min); gateway passes `X-Faas-Deadline-Ms` to the guest |
| 1151 | [Existing consumer execution coverage reconciliation](1151-existing-consumer-execution-reconciliation.md) | accepted | Durable forward-only collection requests; conservative lineage blockers and future-window SLO eligibility |
| 1152 | [Authoritative legacy event execution lineage repair](1152-authoritative-event-execution-lineage-repair.md) | accepted | Bounded restart-safe receipt scans associate provable roots without reconstructing historical outcomes |
| 1153 | [Inspectable lineage cases and guarded retries](1153-inspectable-lineage-cases-and-guarded-retries.md) | accepted | Retained reason-coded cases, scoped pagination and revision-fenced idempotent reevaluation |
| 1154 | [Bounded bulk lineage retries](1154-bounded-bulk-lineage-retries.md) | accepted | Reviewed fixed selections, durable per-case checkpoints and guarded retry results |
| 1155 | [Cancellable bulk lineage retries](1155-cancellable-bulk-lineage-retries.md) | accepted | Job-lock cancellation, original-creation guards and preserved per-case results |
| 1156 | [Retained lineage diagnostic backfill](1156-retained-lineage-diagnostic-backfill.md) | accepted | Bounded insert-only diagnostic scans, independent durable cursors and guarded rescan requests |
| 1157 | [Lineage diagnostic scan controls](1157-lineage-diagnostic-scan-controls.md) | accepted | Coverage-lock pause, resume and cancellation with original-incarnation guards and preserved progress |
| 1158 | [Retained lineage retry job discovery](1158-retained-lineage-retry-job-discovery.md) | accepted | Scoped live pagination, status-filtered summaries and retained job identity discovery |
| 1159 | [Scope-bound lineage retry files](1159-scope-bound-lineage-retry-files.md) | accepted | Versioned target UUIDs, source provenance and guarded CLI submission with raw-file compatibility |
| 1160 | [Pausable bulk lineage retries](1160-pausable-bulk-lineage-retries.md) | accepted | Original-incarnation pause/resume, preserved progress and unfinished-work diagnostic gates |
| 1161 | [Idempotent bulk lineage control receipts](1161-idempotent-bulk-lineage-control-receipts.md) | accepted | Atomic UUID-keyed acknowledgments, bounded metadata history and no repeated state transition |
| 1162 | [Idempotent bulk lineage cancellation receipts](1162-idempotent-bulk-lineage-cancellation-receipts.md) | accepted | Cancellation shares atomic command receipts, terminal acknowledgments and bounded history |
| 1163 | [Trusted lineage control actor attribution](1163-trusted-lineage-control-actor-attribution.md) | accepted | Immutable server-derived account or credential identity in retained command receipts |
| 1164 | [Filtered lineage control history](1164-filtered-lineage-control-history.md) | accepted | Exact action and original actor filters before sequence pagination |
| 1165 | [Guarded lineage control-history export](1165-guarded-lineage-control-history-export.md) | accepted | Bounded observed receipt snapshots in private artifacts with atomic no-overwrite publication |
| 1166 | [Offline lineage control-history inspection](1166-offline-lineage-control-history-inspection.md) | accepted | Strict local artifact validation, actor summaries and guarded receipt comparisons |
| 1167 | [Retained lineage retry-job health](1167-retained-lineage-retry-job-health.md) | accepted | Scoped active-job inactivity and retention diagnostics with explicit progress evidence |
| 1168 | [Lineage retry-job watch alerts](1168-lineage-retry-job-watch-alerts.md) | accepted | Opt-in durable stalled, retention-risk and paused-backlog lifecycle conditions |
| 1169 | [Paginated lineage retry-job attention](1169-paginated-lineage-retry-job-attention.md) | accepted | Live warning-filtered pages beyond health samples with creation guards and progress metadata |
| 1170 | [Reviewed lineage batch-control plans](1170-reviewed-lineage-batch-control-plans.md) | accepted | Fixed scoped commands with independent receipt-verified results and safe repeats |
| 1171 | [Lineage batch-control plan preflight](1171-lineage-batch-control-plan-preflight.md) | accepted | Read-only guarded readiness and existing command acknowledgments before fixed-plan apply |
| 1172 | [Durable lineage control execution reports](1172-durable-lineage-control-execution-reports.md) | accepted | Synced command intents and receipt-checked resume into a fresh private report |
| 1173 | [Read-only lineage control report reconciliation](1173-read-only-lineage-control-report-reconciliation.md) | accepted | Resolve fixed commands from retained server receipts without submitting controls |
| 1174 | [Configurable subscription delivery capacity](1174-configurable-subscription-delivery-capacity.md) | accepted | Lower per-consumer live-delivery ceilings shared by routing and replay without exceeding plan caps |
| 1175 | [Subscription capacity-pressure diagnostics](1175-subscription-capacity-pressure-diagnostics.md) | accepted | Read-only scope usage, effective ceilings and waiting acceptance ages from a complete pressure snapshot |
| 1176 | [Guarded subscription capacity preview](1176-guarded-subscription-capacity-preview.md) | accepted | Read-only proposal validation and projected consumer/shared capacity from fresh scoped evidence |
| 1177 | [Guarded subscription capacity apply](1177-guarded-subscription-capacity-apply.md) | accepted | Atomically compare reviewed saved capacity and locked plan allowance before applying changes |
| 1178 | [Idempotent subscription capacity commands](1178-idempotent-subscription-capacity-commands.md) | accepted | Retained successful capacity acknowledgments and safe repeats with fixed command UUIDs |
| 1179 | [Read-only capacity-command evidence](1179-read-only-capacity-command-evidence.md) | accepted | Inspect original capacity acknowledgments using read authorization |
| 1180 | [Paginated capacity-command history](1180-paginated-capacity-command-history.md) | accepted | Discover retained original command evidence with scoped read-only pagination |
| 1181 | [Capacity-command receipt budget diagnostics](1181-capacity-command-receipt-budget-diagnostics.md) | accepted | Advisory retained receipt usage and earliest expiry in status and capacity preview |
| 1182 | [Capacity-command receipt-budget watch alerts](1182-capacity-command-receipt-budget-watch-alerts.md) | accepted | Opt-in sustained full-budget alerts using existing lifecycle and checkpoint machinery |
| 1183 | [Capacity-command receipt-budget early warnings](1183-capacity-command-receipt-budget-early-warnings.md) | accepted | Opt-in sustained low remaining-slot warnings before receipt quota exhaustion |
| 1184 | [Receipt-budget warning recovery hysteresis](1184-receipt-budget-warning-recovery-hysteresis.md) | accepted | Separate recovery boundaries and sustained fresh recovery with versioned checkpoints |
| 1185 | [Receipt-budget watcher confirmation progress](1185-receipt-budget-watcher-confirmation-progress.md) | accepted | Local fresh-sample and duration diagnostics separate from checkpoint evidence |
| 1186 | [Application event delivery-attempt context](1186-application-event-delivery-attempt-context.md) | accepted | Authoritative transient attempt counters and replay lineage with validated SDK context |
| 1195 | [Unified operation timeline](1195-unified-operation-timeline.md) | accepted | `GET /v1/apps/{slug}/operations/{id}/timeline` merges events, HTTP and workflow step attempts and completion deliveries with per-source cursors; `customer_operation_attempt_instances` points each HTTP attempt at an instance log window; account-only |
| 1161 | [Image runs (`gregale run --image`)](1161-image-runs.md) | accepted for an operator-gated preview | One-task Job runs against a cached per-image `image_run` runner job; Docker ENTRYPOINT/CMD rules; `api.ImageRunCachedImages`; gate `FAAS_IMAGE_RUNS` |
| 1162 | [Billing delivery finality, a hard spend cap, and plan-history allowances](1162-billing-delivery-finality-spend-cap-and-plan-history.md) | accepted | `MeterUsageFinalAfter` gates provider delivery on final minutes; the spend cap bills up to the cap and parks capped accounts (`SpendCapJobDeferInterval` defers queued job tasks); app purge keeps usage; the month's allowance follows recorded plan activations |
| 1163 | [Live job task output](1163-live-job-task-output.md) | accepted | Compute-gateway SSE `GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs/stream` follows the task VM ring; `gregale run --image` streams live, `gregale jobs logs --follow` |
| 1330 | [Interactive image runs (`gregale run -it --image`)](1330-interactive-image-runs.md) | accepted for an operator-gated preview | The job guest holds its command until one client attaches over ADR-1037's token, WebSocket and `AttachAppTask` transport; the job supervisor keeps timeout, cancellation and exit receipt; gate `FAAS_INTERACTIVE_IMAGE_RUNS` |
| 1790 | [Image runs: Docker process semantics, `-u/-w`, env files and private registries](1790-image-run-docker-process-and-registry-auth.md) | accepted | Image runs take the image's ENV, USER, WORKDIR and PATH lookup; `gregale run` adds `-u`, `-w`, `--env-file`, `-e KEY`, a positional image and `--registry-username` (credential sealed on the runner job, refreshed per run) |
| 1235 | [Spec §12 operator alerts ship at warn](1235-spec12-operator-alerts-at-warn.md) | accepted | 26 §12 alerts that had no rule are implemented with fixtures, all warn and `public_status: internal` until paging is triaged; write-relay and safe-deploy names corrected to the exported `gatewayd_` / `meterd_` series |
| 1236 | [Paging set: page only on symptoms that need a human now](1236-paging-set.md) | accepted | 96 page alerts become 27: 24 kept, 72 demoted to warn, near-expiry certificate pages and access-gating edge-rule compile errors added; every page alert has a firing fixture |
| 1320 | [Object-storage plan allowances, plan ceilings, and the overage cap](1320-object-storage-plan-allowances-ceilings-and-overage-cap.md) | accepted; every plan at zero until pricing | `Limits.ObjectStorage` holds per-plan ceilings and allowances; allowances are removed before month-close pricing and follow plan history; ceilings tighten the operator policy at URL admission and gateway checks; the month-close charge is held under the overage cap after the month's compute and live egress |
| 1239 | [Docker-compatible image process identity](1239-docker-compatible-image-process-identity.md) | accepted | Root-owned layer entries are published as root when imaged holds CAP_CHOWN; customer images without USER run as root; a numeric UID absent from passwd runs with GID 0 (amends ADR-411 and ADR-136 §5) |
| 1430 | [USD pricing and plan entitlements from the October 2026 COGS report](1430-plan-entitlements-from-october-2026-cogs-report.md) | accepted | Billing in USD only at $0/$10/$30/$99 with $0.01/GB-h overage; RAM per app 256/512/1024/2048 MB and 20/200/1,000/3,000 included GB-h; 1/10/50/200 GiB included object storage with a Free hard stop (write/read split deferred); managed PostgreSQL 1/1/3/10 databases, pooled 0.5/1/5/20 GiB and 5/25/75/200 CU-h ceilings, Free gains one development database |
| 20261019193600 | [Ledger dual-run reconciliation for compute delivery](20261019193600-ledger-dual-run-reconciliation.md) | accepted | meterd compares the ledger's net compute quantity with the pusher's provider receipts per paid account for the current and previous UTC month (match/pending/capped/incomplete/drift/failed); delivery stays on the pusher until a later ADR has a closed month with no drift |
| 1180 | [Long async, queue and cron invocations](1180-long-async-invocations.md) | accepted | Background invocations run for the app's request timeout (≥30 s); leases renewed while the run lasts; long runs leave the drain tick and cron loop |
| 1288 | [Compose-declared build context](1288-compose-declared-build-context.md) | accepted | Project apply stages a Compose-declared Dockerfile service's directory as its archive root (Docker Compose's context); every other workload keeps the repository archive (ADR-144). Keyed on the workload's detector, not the project scan source (H10-25) |
| 1343 | [App edge on compute nodes behind Cloudflare Tunnel](1343-app-edge-on-compute-nodes.md) | proposed | Every compute node runs a `cloudflared` connector and a gatewayd-public that routes to its own gatewayd-internal socket; `*.<apps domain>` moves to the tunnel, platform hosts and custom domains stay on the control plane; credential-bound app paths are forwarded to the control plane; CPU/RAM hold-back, per-node monitoring and connector withdrawal before drain; off by default, on per manifest |
| 1344 | [Availability architecture: regional cells, three-zone Postgres HA, fenced compute](1344-availability-architecture-cells-zones-fencing.md) | proposed | Makes the regional branch's cells and directory highly available: 3 zones per cell, self-run Patroni + etcd with a synchronous standby, every-edge global ingress, node leases with self-fencing (quota still released only by signed stop proof), static stability and a zero-downtime contract; phases P0–P6 plus landing the regional branch |
| 1342 | [Per-function settings in gregale.yaml and concurrent requests per function worker](1342-function-settings-and-concurrent-workers.md) | accepted | A functions manifest entry sets ram_mb, request_timeout_s, min_instances, concurrency, env (prune_env), secrets_from_env, routes, queue_bindings and event_triggers; `function_concurrency` lets one Node or Python worker serve several requests at once, multiplexed by `__faas_id` with acknowledged cancels |
| 20261016151947 | [Live queries: an incremental SQL engine for native managed Postgres](20261016151947-live-queries-incremental-engine.md) | proposed | Subscribe to a read-only SQL query and receive the initial result, then atomic deltas per committed batch; capture and lineage-keyed maintenance run inside the database VM from logical decoding, PostgreSQL evaluates every expression, realtimed fans out; sealed-query and Data API row-level-security modes; native Postgres first |

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
- docs/adr/023-ipv006-egress-policy.md (banner appended after the existing PR-D block)
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
- docs/adr/064-tier-a004-cross-node-rebalance.md
- docs/adr/064-wake-timeline-canonical-vocabulary.md
- docs/adr/066-tier-a005-cross-node-live-migration.md
- docs/adr/068-issue-517-closure-evidence.md
- docs/adr/074-warm-snapshot-audit-gc.md
- docs/adr/075-deploy-001-capdecl-mount-rpc-boundary.md
- docs/adr/076-session-binding-hash.md
- docs/adr/078-deploy-002-daemonunit-generator.md
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

## Fleet security decisions

- [ADR-178: dedicated fleet sealed-secret domain](178-fleet-sealed-secret-domain.md)

## Daemon durability decisions

- [ADR-190: daemon durability primitives](190-daemon-durability-primitives.md) — default gRPC deadlines, liveness-gated systemd watchdog, last-known-good route tier, one LISTEN connection per daemon
- [ADR-419: authoritative VM inventory recovery](419-authoritative-vm-inventory-recovery.md) — signed process inventory, physical placement, and autonomous missing-service recovery
- [ADR-191: scheduler divergence reconciliation and bounded loop dispatch](191-scheduler-divergence-and-bounded-dispatch.md) — repair rows the owning vmmd is not reporting (report-only first), and move every long-running notification handler onto one bounded pool
- [ADR-345: durable event fanout and workflow leases](345-durable-event-fanout-and-workflow-leases.md) — persistent event claims, per-step workflow recovery, and versioned event schemas
- [ADR-346: event fanout recipient snapshots](346-event-fanout-recipient-snapshots.md) — capture eligible subscription candidates when an event is accepted
- [ADR-347: recipient-scoped event fanout recovery](347-recipient-scoped-event-fanout-recovery.md) — persist candidate outcomes, retry transient routing failures independently, and cap poison recipients

## Job execution decisions

- [ADR-366: job run inputs, results, and flexible scheduling](366-job-run-input-results-and-flexible-scheduling.md)
- [ADR-367: job attempt history, linked replay, and managed object evidence](367-job-attempt-replay-managed-artifacts.md)

Note: two ADRs carry the number 190 (`190-production-buildkit-cache.md` merged
first; `190-daemon-durability-primitives.md` picked the same number
concurrently). The log above also has duplicate pairs 157, 158, 167, and 168.
A renumber plus a CI uniqueness gate is worth its own PR.

## Object-storage binding decisions

- [ADR-284: safe object-storage binding rotation](284-safe-object-storage-binding-rotation.md) — retain the previous key through the durable rolling refresh, with atomic key and secret mutation
- [ADR-285: atomic object-storage binding creation](285-atomic-object-storage-binding-creation.md) — commit the S3 credential and six managed secrets together
- [ADR-286: runtime freshness for object-storage binding creation](286-object-storage-binding-create-runtime-freshness.md) — stamp runtime configuration and stale snapshots in the binding creation transaction
- [ADR-287: atomic object-storage binding revocation](287-atomic-object-storage-binding-revocation.md) — revoke both keys, remove managed secrets, and invalidate runtime snapshots in one transaction

## S3 service decisions

- [ADR-1542: First Gregale-owned object storage node](1542-owned-object-storage-first-node.md) — private single-node disk prototype behind the existing S3 driver; no production enrollment.
- [ADR-1543: Durable owned-node write receipts](1543-owned-object-storage-write-receipts.md) — exact committed/rejected outcomes survive overwrite, deletion and restart; signed private recovery and replay fencing.
- [ADR-1544: Durable owned-node multipart uploads](1544-owned-object-storage-multipart.md) — restartable parts, frozen completion manifests, atomic assembly, safe abort/expiry and exact completion recovery.
- [ADR-1545: Owned-node inventory and reconciliation](1545-owned-object-storage-inventory.md) — coherent private capacity pages, read-only disk diagnostics, preserved admission holds and cached operational metrics.
- [ADR-1546: Resumable owned-node checksum scrubbing](1546-owned-object-storage-scrubbing.md) — bounded SHA256 verification, durable checkpoints and findings bound to immutable blobs, private status and operational metrics.
- [ADR-1547: Verified offline owned-node backups](1547-owned-object-storage-backups.md) — consistent recovered snapshots, catalog-verified manifests, atomic restore to a new directory and recovery drills after source loss.
- [ADR-1548: Owned-node replication journal and catch-up](1548-owned-object-storage-replication-journal.md) — opt-in ordered mutation capture, retained replay bytes, authenticated transfer and fenced asynchronous followers.
- [ADR-1549: Planned owned-node primary switchover](1549-owned-object-storage-primary-switchover.md) — irreversible source fencing, one-follower checkpoint grants, verified promotion with a new authority epoch and safe gateway recovery.
- [ADR-1550: Replica-confirmed owned-node acknowledgements](1550-owned-object-storage-replica-durability.md) — persisted optional replica policy, authenticated applied confirmations, gated S3 outcomes and recovery, and preserved capacity holds.
- [ADR-1551: Owned-node journal retention and compaction](1551-owned-object-storage-journal-compaction.md) — sealed offline floor checkpoints, registered follower cursors, bounded replay retention and explicit reseeding.
- [ADR-1552: Repeatable local object-service qualification](1552-owned-object-storage-local-qualification.md) — bounded mixed gateway traffic, real process failures and maintenance drills, exact correctness checks and JSON performance reports.
- [ADR-1553: Bounded replication change waits](1553-owned-object-storage-replication-waits.md) — immediate backlog draining, authenticated bounded idle waits and reconnect observations without changing durability.
- [ADR-1554: Prepare replay payloads outside catalog publication](1554-owned-object-storage-publication-preparation.md) — immutable file proofs, bounded preparation and paced follower replay with preserved crash boundaries.
- [ADR-1555: Qualify disposable object nodes across SSH hosts](1555-owned-object-storage-ssh-qualification.md) — private remote lifecycle and maintenance, host identity evidence, configurable network/work profiles and measured recovery.
- [ADR-1556: Bounded automatic journal compaction](1556-owned-object-storage-automatic-compaction.md) — live primary/follower retention, checkpointed batches, pending-proof pins and cached cleanup metrics.
- [ADR-1557: Bounded object-storage soak qualification](1557-owned-object-storage-soak-qualification.md) — sustained traffic, interval evidence, disk/work limits and follower interruptions during cleanup.
- [ADR-1558: Profile replication and fetch bounded journal pages](1558-owned-object-storage-replication-profiling.md) — attempt timings and authenticated bounded prefetch with individual durable checkpoints.
- [ADR-1559: Admit new object writes within retention and disk headroom](1559-owned-object-storage-write-admission.md) — conservative concurrent reservations, pressure hysteresis and advisory gateway checks that preserve uncertain holds.
- [ADR-1560: Inspect qualification hosts and storage before creating node data](1560-owned-object-storage-host-storage-preflight.md) — read-only SSH inspection, exact mount observations and an optional guard for dedicated local filesystems.
- [ADR-1561: Settled recovery proof collection on the owned object node](1561-owned-object-storage-recovery-retention.md) — durable settlement acknowledgements, bounded request identities and journaled receipt/tombstone maintenance.
- [ADR-1562: Durable owned-node settlement acknowledgement delivery](1562-owned-object-storage-settlement-outbox.md) — transactional terminal outbox, fenced asynchronous delivery and preserved recovery placement.
- [ADR-1563: Indexed online recovery cleanup on the owned object node](1563-owned-object-storage-indexed-recovery-cleanup.md) — persistent local eligibility, bounded online retirement and cached maintenance metrics.
- [ADR-1564: Qualify format-5 recovery protection with built object nodes](1564-owned-object-storage-retention-qualification.md) — lost settlement replies, fresh-proof protection and acknowledgement persistence through restart and promotion.
- [ADR-1565: Indexed object listings and bounded multipart expiry](1565-owned-object-storage-indexed-listing-expiry.md) — generation-scoped key seeks, persistent expiry scheduling and bounded part reclamation.
- [ADR-1566: Journal-backed logical usage counters on owned object nodes](1566-owned-object-storage-usage-counters.md) — coherent bucket totals, audited projections and opt-in ordinary accounting with preserved capacity fences.
- [ADR-1567: Bounded, resumable source inventory audits](1567-owned-object-storage-incremental-audits.md) — checkpointed work budgets, source-revision restarts and conservative coverage freshness.
- [ADR-1568: Source coverage during concurrent object mutations](1568-owned-object-storage-mutation-aware-audits.md) — independent coverage, bounded change reconciliation and conservative overflow recovery.
- [ADR-1569: Keep self-hosted object storage behind a development gate](1569-owned-object-storage-development-gate.md) — default-off private startup and backend loading, separate from customer S3 availability.
- [ADR-1570: Separate private object-storage capability gates](1570-owned-object-storage-capability-gates.md) — default-off server, backend, settlement and usage-counter controls under the private master gate.
- [ADR-530: S3 compatibility and multipart capacity admission](530-s003-compatibility-and-multipart-capacity.md)
- [ADR-531: S3 multipart transfer fencing and cleanup](531-s003-multipart-transfer-fencing-and-cleanup.md)
- [ADR-532: Conditional S3 multipart completion](532-conditional-s003-multipart-completion.md)
- [ADR-533: Safe object capacity reconciliation](533-safe-object-capacity-reconciliation.md)
- [ADR-534: Recoverable application object uploads](534-recoverable-application-object-uploads.md)
- [ADR-535: Recoverable S3 gateway PUTs](535-recoverable-s003-gateway-puts.md)
- [ADR-536: Recoverable S3 gateway copies](536-recoverable-s003-gateway-copies.md)
- [ADR-537: Customer object write receipts](537-customer-object-write-receipts.md)
- [ADR-538: S3 multipart copy and source ETag conditions](538-s003-multipart-copy-and-source-etag-conditions.md)
- [ADR-539: Historical S3 write receipt recovery](539-historical-s003-write-receipt-recovery.md)
- [ADR-540: Native S3 version capacity inventory](540-native-s003-version-capacity-inventory.md)
- [ADR-541: Immutable S3 copy sources and date conditions](541-immutable-s003-copy-sources-and-date-conditions.md)
- [ADR-542: Customer S3 version identities and reads](542-customer-s003-version-identities-and-reads.md)
- [ADR-543: Customer-selected S3 copy sources](543-customer-selected-s003-copy-sources.md)
- [ADR-544: Durable S3 multipart completion identities](544-durable-s003-multipart-completion-identities.md)
- [ADR-545: Durable bucket versioning configuration](545-durable-bucket-versioning-configuration.md)
- [ADR-546: Permanent immutable S3 version deletion](546-immutable-s003-version-deletion.md)
- [ADR-547: Durable ordinary and mutable null S3 deletion](547-durable-s003-mutable-deletion.md)
- [ADR-548: Coordinate immutable deletion with version inventory](548-immutable-deletion-inventory-coordination.md)
- [ADR-549: Tag current and retained object versions](549-version-specific-object-tagging.md)
- [ADR-550: Durable object lifecycle discovery and expiration](550-durable-object-lifecycle.md)
- [ADR-551: Atomic object mutation events](551-atomic-object-mutation-events.md)
- [ADR-552: Owned S3 notification destinations](552-owned-s003-notification-destinations.md)
- [ADR-553: Bounded production object transfers](553-bounded-production-object-transfers.md)
- [ADR-554: Owned key bindings and native S3 encryption](554-owned-key-bindings-and-native-s003-encryption.md)
- [ADR-555: Durable object encryption journals](555-durable-object-encryption-journals.md)
- [ADR-556: Customer S3 encryption and owned response metadata](556-customer-s003-encryption.md)
- [ADR-557: Branded object URL capabilities with one write receipt](557-branded-object-url-capabilities.md)
- [ADR-558: Branded control multipart uploads](558-branded-control-multipart-uploads.md)
- [ADR-559: Owned encryption on upload routes](559-owned-encryption-on-upload-routes.md)
- [ADR-560: Atomic fixed multipart admission](560-fixed-multipart-admission.md)
- [ADR-561: Bucket default encryption](561-bucket-default-encryption.md)
- [ADR-562: Owned cross-bucket copy sources](562-owned-cross-bucket-copy-sources.md)
- [ADR-563: Native Object Lock protocol and versioning lock order](563-native-object-lock-protocol.md)
- [ADR-564: Durable owned bucket Object Lock](564-durable-bucket-object-lock.md)

## Snapshot restore optimization decisions

- [ADR-147: request activity flush cadence](147-request-activity-flush-cadence.md)
- [ADR-148: nonblocking resume hardware entropy](148-nonblocking-resume-hardware-entropy.md)
- [ADR-149: prepared unused network cache](149-prepared-unused-network-cache.md)
- [ADR-150: firecracker tsc restore order canary](150-firecracker-tsc-restore-order-canary.md)
- [ADR-425: retained cache materialization](425-retained-cache-materialization.md) — avoid the redundant copy while retaining the opened artifact through eviction

## Environment intent decisions

- [ADR-568: Git-owned environment intent](568-environment-gitops-contract.md) — explicit field ownership, reviewed adoption and continuous reconciliation

## Managed service recovery decisions

- [ADR-420: continuous service recovery](420-continuous-service-recovery.md) — periodic desired-capacity reconciliation with durable claims and retry deadlines
- [ADR-421: continuous app ownership recovery](421-continuous-app-ownership-recovery.md) — paged periodic ownership transfer with node-health fencing
- [ADR-422: bare-metal service recovery capacity](422-bare-metal-service-recovery-capacity.md) — durable admission protection for one-host recovery

## API hosting verification decisions

- [ADR-433: candidate connectivity and HTTP health verification](433-candidate-connectivity-verification.md) — preserve TCP-ready API compatibility with proof of a candidate response
- [ADR-434: atomic hosting failure finalization](434-atomic-hosting-failure-finalization.md) — commit failed verdicts with terminal state and retry interrupted persistence through the existing notification outbox
- [ADR-459: candidate verification cache isolation](459-candidate-verification-cache-isolation.md) — bypass response caches for validated candidate probes
- [ADR-481: durable challenge-publication recovery](481-durable-challenge-publication-recovery.md) — recover temporary publication outages on the same candidate with a persisted deadline
- [ADR-482: candidate verdict attribution and durable verification recovery](482-candidate-verdict-attribution.md) — require proof for app verdicts and retry unavailable gateway/transport evidence
- [ADR-483: node-owned deployment handoffs](483-node-owned-deployment-handoffs.md) — only the owning imaged claims or acknowledges a local builder export
- [ADR-484: resumable image preparation](484-resumable-image-preparation.md) — resume layer publication, scanning and snapshot handoff across imaged restarts
- [ADR-485: renewable notification ownership](485-renewable-notification-ownership.md) — share fenced delivery claims between imaged LISTEN and replay, with renewal during long work
- [ADR-486: recover interrupted snapshot primes](486-recover-interrupted-snapshot-primes.md) — keep graceful schedd shutdown from terminally failing snapshot preparation and clean up its specific VM before recovery
- [ADR-843: pre-promotion API route checks](843-pre-promotion-api-route-checks.md) — probe explicitly selected read-only OpenAPI operations against the exact candidate and persist per-route evidence before promotion

## Route review and release protection

- [ADR-435: Read-only preview route change reports](435-preview-route-change-reports.md)
- [ADR-436: Customer-owned route requirements in preview reports](436-route-requirements.md)
- [ADR-437: Read-only route policy patch planning](437-route-policy-plans.md)
- [ADR-438: Transactional application of reviewed route policy plans](438-transactional-route-policy-apply.md)
- [ADR-439: Static FastAPI route impact between Git source revisions](439-static-fastapi-route-impact.md)
- [ADR-440: Function references and semantic source changes for route impact](440-function-level-route-impact.md)
- [ADR-441: Source impact joins and priorities in preview reviews](441-source-impact-preview-reviews.md)
- [ADR-442: Directional request compatibility in preview reports](442-request-input-compatibility.md)
- [ADR-443: Validation bounds and nullable request unions](443-request-validation-compatibility.md)
- [ADR-444: Captured route authentication comparison](444-declared-route-security-comparison.md)
- [ADR-445: Preview route-family policy coverage](445-preview-route-family-policy-coverage.md)
- [ADR-446: Route-group policy plans and captured impact](446-route-group-policy-planning.md)
- [ADR-447: Opt-in route-group budget consolidation](447-route-group-budget-consolidation.md)
- [ADR-448: Saved app route requirements and snapshot checks](448-saved-route-requirements.md)
- [ADR-449: Durable automatic route checks and freshness](449-automatic-route-checks.md)
- [ADR-450: Opt-in route safety gates for canary advancement](450-canary-route-safety-gates.md)
- [ADR-451: Continuous route policy checks and safety transition events](451-continuous-route-policy-monitoring.md)
- [ADR-452: Repair plans bound to saved route intent](452-saved-route-policy-repairs.md)
- [ADR-453: Retained route checks and finding regressions](453-route-finding-history.md)
- [ADR-454: Observed route health gates for canary progression](454-observed-route-canary-health.md)
- [ADR-455: Critical route p95 latency budgets during canaries](455-critical-route-canary-latency.md)
- [ADR-456: Saved canary route health decisions and explanations](456-saved-canary-route-health-decisions.md)
- [ADR-457: Critical route health hold and resume notifications](457-route-health-transition-notifications.md)
- [ADR-458: Opt-in automatic recovery for critical route error regressions](458-critical-route-automatic-rollback.md)
- [ADR-844: Default report-mode route health selectors](844-default-report-mode-route-health.md) — seed observed critical routes for unconfigured apps on their first canary advance
- [ADR-846: Stage-pooled evidence for low-traffic critical routes](846-pooled-route-health-evidence.md) — re-evaluate sparse routes over two halves of the stage so far with unchanged thresholds
- [ADR-847: Opt-in synthetic probes for routes without organic traffic](847-synthetic-route-probes.md) — probe opted-in GET/HEAD routes on candidate and stable with auth gates intact and no telemetry
- [ADR-976: Route advisor](976-route-advisor.md) — suggest cache, async and per-consumer throttle rules from observed traffic with what-if estimates; rules are created disabled
- [ADR-844: Default report-mode route health selectors](844-default-report-mode-route-health.md) — seed observed critical routes for unconfigured apps on their first canary advance
- [ADR-846: Stage-pooled evidence for low-traffic critical routes](846-pooled-route-health-evidence.md) — re-evaluate sparse routes over two halves of the stage so far with unchanged thresholds
- [ADR-847: Opt-in synthetic probes for routes without organic traffic](847-synthetic-route-probes.md) — probe opted-in GET/HEAD routes on candidate and stable with auth gates intact and no telemetry
- [ADR-977: Opt-in service wake-ahead along measured edges](977-service-wake-ahead.md) — wake the services a waking app is measured to call, below a 60% residency guard, through the ordinary wake path (amends ADR-196)
- [ADR-978: Route priority for the warm-capacity queue](978-route-priority-under-load.md) — order saturated requests critical/normal/bulk and let higher classes take lower-class queue places
- [ADR-1152: Route feature evidence, advisor settings, and token-free rollout completion](1152-route-feature-evidence-and-rollout-completion.md) — show learned wake-ahead edges and queue outcomes, suggest the settings, and complete finished rollouts without the safe-release tokens (#1926)
- [ADR-1187: Route awake report](1187-route-awake-report.md) — which routes keep a scale-to-zero app awake, what removing each would save, and edge-cache or polling suggestions
- [ADR-1188: Learned cacheability](1188-learned-cacheability.md) — compare anonymous GET bodies with the previous response per URL to measure whether a route is safe to cache, and for how long
- [ADR-1189: Safe Deploy and scheduled prewarm on by default](1189-safe-releases-and-prewarm-on-by-default.md) — the control-plane role provisions the Safe Deploy token pair and schedd runs the prewarm drain
- [ADR-1198: Proactive route insights](1198-proactive-route-insights.md) — apid records routes that keep apps awake or are safe to cache, and notifies app webhooks of each transition
- [ADR-1199: Route evidence — cache rules and awake sources](1199-route-evidence-cache-rules-and-awake-sources.md) — the gateway observes cache rule refills so stale or too-short rules become insights, and the awake report credits route probes and prewarm
- [ADR-480: Platform paths reserved on platform hosts only](480-platform-paths-reserved-on-platform-hosts.md)
- [ADR-493: Observed customer exposure for route changes](493-route-customer-exposure.md) — bounded, read-only request-time customer usage evidence in preview reports
- [ADR-494: Advisory customer route health](494-advisory-customer-route-health.md) — compare tenant or consumer route health while preserving sparse and attribution coverage
- [ADR-495: Advisory watched response codes](495-advisory-route-client-errors.md) — detect selected 4xx regressions without changing rollout decisions
- [ADR-496: Route regression investigation](496-route-regression-investigation.md) — connect route findings to bounded, scoped request examples
- [ADR-497: Route latency investigation](497-route-latency-investigation.md) — add dependency and execution evidence to route latency findings
- [ADR-498: Advisory production route budgets and saved incidents](498-production-route-monitoring.md) — continuously evaluate serving-route budgets and retain bounded incidents
- [ADR-845: Opt-in automatic rollback for early production route incidents](845-route-monitor-automatic-rollback.md) — request one checked rollback to the saved healthy baseline when an error budget is violated within 30 minutes of release
- [ADR-499: Customer-cohort production route monitoring](499-customer-cohort-production-route-monitoring.md) — attribute incidents to request-time tenant or consumer cohorts with bounded recovery tracking
- [ADR-576: Private TCP addressing between services](576-private-tcp-service-addressing.md)
- [ADR-593: Static Go net/http route impact](593-go-nethttp-route-impact.md) — map Go ServeMux source changes to route-level review evidence
- [ADR-594: Static Go Chi route impact](594-go-chi-route-impact.md) — map literal Chi routes, groups, mounts, and middleware to route-level review evidence

## Customer operation decisions

- [ADR-521: customer operations above execution ledgers](521-customer-operations.md) — typed application contracts, customer ownership, separate business and delivery outcomes, and controlled recovery
- [ADR-517: version customer workflow contracts and require transition evidence](517-versioned-customer-workflow-contracts.md) — pin workflow meaning and verify required milestones across app commit and platform publication
- [ADR-518: business workflow observations and transactional evidence](518-business-workflow-observations-and-evidence.md) — scoped workflow assessments, typed business evidence, reconciliation, and compensation observations

- [ADR-571: S3 write proof custody and owned cleanup](571-s003-write-proof-custody-and-owned-cleanup.md) — retain pending key evidence and coordinate protected version/account cleanup.
- [ADR-658: Private result files for workflow Operations](658-workflow-operation-artifacts.md)

## Automation recovery lifecycle

- [ADR-1000: Reviewed workflow event recovery jobs](1000-reviewed-workflow-event-recovery-jobs.md) — durable submission of pinned automation event plans.

- [ADR-1001: Durable recovery of reviewed native workflow runs](1001-durable-workflow-run-recovery.md)

- [ADR-1002: Observe the admitted workflow recovery generation](1002-workflow-recovery-execution-observations.md)
- [ADR-1003: Retain workflow recovery outcomes and notify execution completion](1003-workflow-recovery-terminal-results.md)
- [ADR-1004: Reviewed retries from saved workflow recovery failures](1004-reviewed-workflow-recovery-retries.md)
- [ADR-1005: Server-side native workflow recovery preflight](1005-workflow-recovery-preflight.md)
- [ADR-1006: Workflow recovery execution attention](1006-workflow-recovery-execution-attention.md)
- [ADR-1007: Reviewed workflow recovery cancellation](1007-reviewed-workflow-recovery-cancellation.md)
- [ADR-1008: Saved workflow recovery response plans](1008-saved-workflow-recovery-response-plans.md)
- [ADR-1009: Preview workflow recovery policies](1009-preview-workflow-recovery-policies.md)

- [ADR-1010: Policy recovery review decisions and live freshness](1010-policy-recovery-review-decisions.md)

- [ADR-1011: Durable recovery review-ready notifications](1011-recovery-review-ready-notifications.md)

- [ADR-1012: Recovery review execution outcomes and completion notification](1012-recovery-review-execution-completion.md)

- [ADR-1013: Bounded automatic recovery retries](1013-bounded-automatic-recovery-retries.md)
- [ADR-1014: Recovery queue visibility and operator controls](1014-recovery-queue-and-operator-controls.md)
- [ADR-1015: Queued retry suppression and blocked notifications](1015-queued-retry-suppression-and-blocked-notifications.md)

- [ADR-1016: Automatic retry queue evaluation evidence](1016-automatic-retry-queue-evaluation-evidence.md)

- [ADR-1017: Recovery policy change preview](1017-recovery-policy-change-preview.md)
- [ADR-1018: Recovery automation health](1018-recovery-automation-health.md)
- [ADR-1019: Recovery health alert incidents](1019-recovery-health-alert-incidents.md)
- [ADR-1020: Read-only retained recovery history](1020-recovery-retained-history.md)
- [ADR-1021: Durable recovery policy, worker and incident events](1021-durable-recovery-history.md)
- [ADR-1022: Observed recovery automation effectiveness report](1022-recovery-effectiveness-report.md)
- [ADR-1023: Evidence-limited historical recovery policy evaluation](1023-historical-recovery-policy-evaluation.md)

- [ADR-1024: Durable recovery candidate evidence](1024-durable-recovery-candidate-evidence.md)
- [ADR-1025: Policy-independent workflow failure evidence](1025-policy-independent-workflow-failure-evidence.md)

- [ADR-1026: Later eligibility observations for workflow failures](1026-workflow-failure-eligibility-observer.md)
- [ADR-1259: Reviewed first recovery from captured workflow failures](1259-reviewed-initial-workflow-recovery.md)

## Data API execution and recovery

- [ADR-1729: Data API connection-capacity observations](1729-data-api-connection-capacity-observations.md)

- [ADR-1730: Data API runtime connection-demand observations](1730-data-api-runtime-connection-demand.md)

- [ADR-1731: Data API database timing telemetry](1731-data-api-database-timing-telemetry.md)

- [ADR-1732: Data API per-user concurrent request limits](1732-data-api-user-concurrency-limits.md)

- [ADR-1733: Data API lock waits and contention recovery](1733-data-api-lock-contention-policy.md)

- [ADR-1734: Data API deadlines across SQL transaction retries](1734-data-api-sql-retry-deadlines.md)

- [ADR-1735: Owner-approved deadline grants during credential rotation](1735-data-api-deadline-grant-rotation.md)

- [ADR-1736: Zero-downtime daemon restarts](1736-zero-downtime-daemon-restarts.md)

- [ADR-1737: Internal mail service with independent activation and delivery gates](2026101501-internal-mail-service.md)

- [ADR-1738: Durable producer handoff for internal account-deletion email](2026101601-mail-producer-outbox.md)

- [ADR-1667: Coordinated SQL refresh across credential rotation](1667-data-api-coordinated-refresh-rotation.md)

- [ADR-1668: Read-only Data API rotation preflight](1668-data-api-rotation-preflight.md)

- [ADR-1669: Read-only mutation receipt recovery](1669-data-api-read-only-mutation-recovery.md)

- [ADR-1670: Versioned Data API replay expiry](1670-data-api-versioned-replay-expiry.md)

- [ADR-1671: Versioned update and delete receipts](1671-data-api-versioned-update-delete-receipts.md)

- [ADR-1672: Recoverable atomic note batches](1672-data-api-atomic-note-batches.md)

- [ADR-1673: V2 Data API transaction generation](1673-data-api-v2-transaction-generator.md)

- [ADR-1674: Durable client mutation recovery](1674-data-api-durable-mutation-journal.md)

- [ADR-1675: Data API receipt storage diagnostics](1675-data-api-receipt-storage-diagnostics.md)

- [ADR-1676: Incremental note change feed](1676-data-api-incremental-note-change-feed.md)

- [ADR-1677: Ranked, indexed note search under RLS](1677-data-api-ranked-note-search.md)

- [ADR-1678: Read-only team order aggregation](1678-data-api-team-order-aggregation.md)

## Events and delivery

- [Node and Python stored acknowledgment policy parity](20261017213800-cross-sdk-stored-acknowledgment-policies.md)

- [Stored Go acknowledgment intent retry policies](20261017212845-stored-go-acknowledgment-retry-policy.md)

- [Adaptive Python acknowledgment intent retry scheduling](20261017212320-python-acknowledgment-retry-policy.md)

- [Adaptive Node acknowledgment intent retry scheduling](20261017211931-node-acknowledgment-retry-policy.md)

- [Adaptive Go acknowledgment intent retry scheduling](20261017211526-go-acknowledgment-retry-policy.md)

- [ADR-606: Independent event recipient routing and recovery](606-independent-event-recipient-routing.md)
- [ADR-607: Unified event receipt inspection](607-unified-event-receipts.md)
- [ADR-608: Trusted handler replay lineage in event receipts](608-event-receipt-replay-lineage.md)
- [ADR-609: Safe recovery for failed keyed invocations](609-safe-keyed-invocation-replay.md)
- [ADR-610 · Keyed dead-letter replay respects running claims](610-keyed-dead-letter-replay-claim-exclusion.md)
- [ADR-611 · Invocation-backed event delivery attempt history](611-invocation-backed-event-attempt-history.md)
- [ADR-612: Durable deduplication for plain invocation replay](612-durable-plain-invocation-replay.md)
- [ADR-613: Atomic event routing handoff](613-atomic-event-routing-handoff.md)
- [ADR-614: Event delivery backpressure and fair routing](614-event-delivery-backpressure.md)
- [ADR-615: Customer event storage admission](615-customer-event-storage-admission.md)
- [ADR-616: Bounded event routing history](616-bounded-event-routing-history.md)
- [ADR-617: Event consumer backlog inspection](617-event-consumer-backlog-inspection.md)

- [ADR-638: Object version listing and bound historical downloads](638-object-version-cli-and-bound-downloads.md)

- [ADR-639: Resumable CLI object uploads](639-resumable-cli-object-uploads.md)
- [ADR-645: Subscription-scoped retained-event replay preview](645-subscription-retained-event-replay-preview.md)
- [ADR-639: Durable subscription event backfill](639-durable-subscription-event-backfill.md)
- [ADR-646: Unified backfill delivery inspection](646-unified-backfill-delivery-inspection.md)
- [ADR-647: Independent event routing by default](647-independent-event-routing-default.md)
- [ADR-648: Independent workflow event routing](648-independent-workflow-event-routing.md)
- [ADR-792: Workflow and backfill event backlog discovery](792-event-backlog-workflow-backfill-discovery.md)
- [ADR-793: Workflow event replay preview](793-workflow-event-replay-preview.md)
- [ADR-794: Durable workflow event backfill](794-durable-workflow-event-backfill.md)
- [ADR-795: Opt-in keyed event delivery ordering](795-keyed-event-delivery-ordering.md)
- [ADR-796: Event ordering backlog diagnostics](796-event-ordering-backlog-diagnostics.md)
- [ADR-797: Durable bulk event recovery](797-durable-bulk-event-recovery.md)
- [ADR-798: Subscription pause and paced drain](798-subscription-pause-and-paced-drain.md)
- [ADR-799: Consumer routing health and alerts](799-event-consumer-health-and-alerts.md)
- [ADR-800: Consumer routing retry policies](800-consumer-routing-retry-policies.md)
- [ADR-803: Application subscription schema version selection](803-event-subscription-schema-versions.md)
- [ADR-804: Read-only event schema rollout preview](804-event-schema-rollout-preview.md)
- [ADR-805: Consumer execution health and alerts](805-event-consumer-execution-health.md)
- [ADR-806: Paced event execution recovery](806-paced-event-execution-recovery.md)
- [ADR-807: Recovery execution outcomes](807-recovery-execution-outcomes.md)
- [ADR-808: Recovery job controls](808-recovery-job-controls.md)
- [ADR-809: Recovery job discovery](809-recovery-job-discovery.md)
- [ADR-810: Recovery control audit history](810-recovery-control-audit-history.md)
- [ADR-811: Recovery health and alerts](811-recovery-health-and-alerts.md)
- [ADR-812: Durable recovery lifecycle notifications](812-recovery-lifecycle-notifications.md)
- [ADR-813: Recovery capacity diagnostics](813-recovery-capacity-diagnostics.md)
- [ADR-814: Read-only recovery preflight](814-recovery-preflight.md)

- [ADR-955: GCS conditional PUT capabilities](955-gcs-conditional-put-capabilities.md)
- [ADR-911: Bounded batch event publication](911-batch-event-publication.md)

- [ADR-912: Event retention health and expiry warnings](912-event-retention-health.md)
- [ADR-838: Approved route removal exceptions in the contract gate](838-approved-route-removal-contract-exceptions.md)

- [ADR-839: Telemetry coverage for route removal approvals](839-route-removal-telemetry-coverage.md)

- [ADR-840: App-scoped route removal coverage](840-app-scoped-route-removal-coverage.md)
- [ADR-841: Operation deprecation and sunset headers](841-route-deprecation-headers.md)
- [ADR-903: Gregale route sunset report](829-route-sunset-report.md)
- [ADR-906: Frozen instances](906-frozen-instances.md)
- [ADR-1151: Pause tiers — quiet pause between requests, frozen after idle](1151-pause-tiers.md)
- [ADR-1346: Runtime binding audiences and explicit queue release requirements](1346-binding-runtime-audiences-and-queue-release-requirements.md)
- [ADR-1347: Unified binding-policy verification](1347-unified-binding-policy-verification.md)
- [ADR-830: Saved sunset report comparisons](830-route-sunset-regression-tracking.md)
- [ADR-831: Deployment-specific route lifecycle metadata](831-deployment-route-lifecycle.md)
- [ADR-842: Lifecycle guidance on cached responses](842-cached-route-lifecycle.md)
- [ADR-832: Lifecycle declaration reviews and canary gate](832-route-lifecycle-declaration-gate.md)

- [ADR-833: Durable lifecycle successor compatibility approvals](833-lifecycle-successor-review-receipts.md)

- [ADR-834: Lifecycle review at the production traffic boundary](834-production-lifecycle-transaction-guards.md)

- [ADR-835: Verified lifecycle successors](835-verified-lifecycle-successors.md)

- [ADR-836: Release graph lifecycle successor bindings](836-release-graph-lifecycle-successors.md)

- [ADR-837: Production lifecycle review history](837-production-lifecycle-review-history.md)

## Managed realtime release decisions

- [ADR-1112 — Transactional realtime publishing](1112-transactional-realtime-publishing.md)
- [ADR-1113 — Managed realtime access revocation](1113-managed-realtime-access-revocation.md)
- [ADR-1114: PostgreSQL realtime table-change feeds](1114-postgresql-realtime-changefeeds.md)
- [ADR-1115: HTTP SSE subscriptions for retained realtime channels](1115-realtime-sse-subscriptions.md)
- [ADR-1116: Transactional retained-channel webhooks](1116-realtime-channel-webhooks.md)
- [ADR-1117: Short-lived channel subscription tickets](1117-realtime-subscription-tickets.md)
- [ADR-1322: Realtime channels as function triggers](1322-realtime-channel-function-triggers.md)
- [ADR-1323: Realtime is free, with fair-use limits](1323-realtime-usage-metering-and-plan-limits.md)
- [ADR-1324: Realtime channel occupancy](1324-realtime-channel-occupancy.md)
- [ADR-1321: Batched trigger delivery](1321-batched-trigger-delivery.md)
- [ADR-1027: Receive-only SDK transport fallback](1027-realtime-transport-fallback.md)
- [ADR-1028: Realtime event inspector](1028-realtime-event-inspector.md)
- [ADR 1029: Backend-visible channel processing acknowledgements](1029-realtime-processing-acknowledgement-status.md)
- [ADR 1030: Lease-fenced realtime consumer groups](1030-realtime-consumer-groups.md)
- [ADR 1031: Group-bound realtime worker credentials](1031-scoped-realtime-worker-credentials.md)
- [ADR 1032: Channel-scoped realtime publisher credentials](1032-scoped-realtime-publisher-credentials.md)
- [ADR 1033: Consumer-group dead-letter webhooks](1033-realtime-consumer-group-dead-letter-webhooks.md)
- [ADR 1034: Batch claims and concurrent consumer-group workers](1034-realtime-batch-claims-and-worker-concurrency.md)
- [ADR 1035: Consumer-group pause and resume](1035-realtime-consumer-group-pause-and-resume.md)
- [ADR 1036: Consumer-group drain mode](1036-realtime-consumer-group-drain.md)
- [ADR 1037: Bounded consumer-group long polling](1037-realtime-consumer-group-long-polling.md)
- [ADR 1038: Consumer-group lag alerts](1038-realtime-consumer-group-lag-alerts.md)
- [ADR 1039: Per-entity consumer-group ordering](1039-realtime-consumer-group-entity-ordering.md)
- [ADR 1040: Persistent consumer-group lease fencing tokens](1040-realtime-consumer-group-lease-fencing.md)
- [ADR 1041: Consumer-group dead-letter retention](1041-realtime-consumer-group-dead-letter-retention.md)
- [ADR 1042: Automatic consumer-group retry policies](1042-realtime-consumer-group-retry-policies.md)
- [ADR 1043: Bounded bulk dead-letter recovery](1043-realtime-consumer-group-bulk-dead-letter-recovery.md)
- [ADR 1044: Consumer-group delivery deadlines](1044-realtime-consumer-group-delivery-deadlines.md)
- [ADR 1045: Shared consumer-group claim rate limits](1045-realtime-consumer-group-claim-rate-limits.md)
- [ADR 1046: Immutable consumer-group message filters](1046-realtime-consumer-group-message-filters.md)
- [ADR 1047: Consumer-group worker health heartbeats](1047-realtime-consumer-group-worker-heartbeats.md)
- [ADR 1048: Consumer-group processing latency metrics](1048-realtime-consumer-group-processing-metrics.md)
- [ADR 1049: Delayed publishing visibility in the realtime inspector](1049-realtime-delayed-publish-inspector.md)
- [ADR 1050: Priority-aware consumer group claims](1050-realtime-consumer-group-priority-claims.md)
- [ADR 1051: Consumer-group batch settlement](1051-realtime-consumer-group-batch-settlement.md)
- [ADR 1052: Automatic consumer-group circuit breakers](1052-realtime-consumer-group-circuit-breakers.md)
- [ADR 1053: Consumer-group delivery-attempt history](1053-realtime-consumer-group-delivery-attempt-history.md)
- [ADR 1054: Cooperative consumer-group lease release](1054-realtime-cooperative-lease-release.md)
- [ADR 1055: Consumer-group settlement receipts](1055-realtime-consumer-group-settlement-receipts.md)
- [ADR 1056: Consumer-group event deduplication](1056-realtime-consumer-group-event-deduplication.md)
- [ADR 1057: Read-only dead-letter JSONL export](1057-realtime-dead-letter-jsonl-export.md)
- [ADR 1058: Background dead-letter replay jobs](1058-realtime-background-dead-letter-replay-jobs.md)
- [ADR 1059: Replay-job lifecycle notifications](1059-realtime-replay-job-notifications.md)
- [ADR 1060: Dry-run replay jobs and reviewed recovery](1060-realtime-dry-run-replay-jobs.md)
- [ADR 1061: Python async realtime worker helper](1061-python-async-realtime-workers.md)
- [ADR 1062: Python receipt-backed settlement retries](1062-python-receipt-backed-settlement-retries.md)
- [ADR 1063: Python worker settlement batching](1063-python-worker-settlement-batching.md)
- [ADR 1064: Python worker lifecycle hooks](1064-python-worker-lifecycle-hooks.md)
- [ADR 1065: Python worker processing timeouts](1065-python-worker-processing-timeouts.md)
- [ADR 1066: Python worker graceful draining](1066-python-worker-graceful-draining.md)
- [ADR 1067: Python expiring realtime activity](1067-python-expiring-realtime-activity.md)
- [ADR 1068: Python combined presence directory](1068-python-presence-directory.md)
- [ADR 1069: Python temporary activity publisher](1069-python-temporary-activity-publisher.md)
- [ADR 1070: Python activity inactivity expiry](1070-python-activity-inactivity-expiry.md)
- [ADR 1071: Python realtime activity summaries](1071-python-realtime-activity-summaries.md)
- [ADR 1072: Python realtime activity scopes](1072-python-realtime-activity-scopes.md)
- [ADR 1073: Python confirmed read-progress tracker](1073-python-confirmed-read-progress-tracker.md)
- [ADR 1074: Python coalesced read assertions](1074-python-coalesced-read-assertions.md)
- [ADR 1075: Python aggregate unread summaries](1075-python-aggregate-unread-summaries.md)
- [ADR 1076: Python read-progress freshness](1076-python-read-progress-freshness.md)
- [ADR 1077: Python read-refresh coordinator](1077-python-read-refresh-coordinator.md)
- [ADR 1078: Python unread badge presentation](1078-python-unread-badge-presentation.md)
- [ADR 1079: Python realtime connection-status observation](1079-python-realtime-connection-status.md)
- [ADR 1080: Python realtime connection quality](1080-python-realtime-connection-quality.md)
- [ADR 1081: Python realtime health summary](1081-python-realtime-health-summary.md)
- [ADR 1082: Python reconnect catch-up observation](1082-python-reconnect-catch-up.md)
- [ADR 1083: Python catch-up stall detection](1083-python-catch-up-stall-detection.md)
- [ADR 1084: Python subscription readiness](1084-python-subscription-readiness.md)
- [ADR 1085: Python subscription readiness waits](1085-python-subscription-readiness-waits.md)
- [ADR 1086: Python catch-up completion waits](1086-python-catch-up-completion-waits.md)
- [ADR 1087: Python combined realtime readiness gate](1087-python-combined-realtime-readiness.md)
- [ADR 1088: Python realtime diagnostics export](1088-python-realtime-diagnostics-export.md)
- [ADR 1089: Python realtime incident timeline](1089-python-realtime-incident-timeline.md)
- [ADR 1090: Python realtime reconnect and replay coordinator](1090-python-realtime-recovery-coordinator.md)
- [ADR 1091: Python native managed realtime WebSocket transport](1091-python-native-managed-websocket-client.md)
- [ADR 1092: Python multi-channel subscriptions on one WebSocket](1092-python-multi-channel-websocket-client.md)
- [ADR 1093: Native Python WebSocket presence and activity](1093-python-native-presence-and-activity.md)
- [ADR 1094: Python durable channel subscriptions](1094-python-durable-channel-subscriptions.md)
- [ADR 1095: Python server-side channel event filters](1095-python-server-side-channel-event-filters.md)
- [ADR 1096: Python native read receipts and unread counts](1096-python-native-read-receipts-and-unread-counts.md)
- [ADR 1097: Python native live-only direct messages](1097-python-live-only-direct-messages.md)
- [1098: Python retained-channel SSE fallback](1098-python-realtime-sse-fallback.md)
- [1099: Python realtime subscription pause and resume](1099-python-realtime-subscription-pause-resume.md)
- [1100: Python realtime backpressure observations](1100-python-realtime-backpressure-observations.md)
- [1101: Python retained-channel graceful draining](1101-python-realtime-graceful-drain.md)
- [1102: Delivery expiration for retained realtime channel events](1102-retained-realtime-message-expiration.md)
- [1103: Default and per-message TTL for retained channel batches](1103-retained-realtime-batch-message-ttl.md)
- [1104: Message TTL for scheduled realtime publication](1104-scheduled-realtime-message-ttl.md)
- [1105: TTL for transactional realtime messages](1105-transactional-realtime-message-ttl.md)
- [1106: Message TTL for scoped realtime publishers](1106-scoped-realtime-publisher-message-ttl.md)
- [1107: Realtime message expiry diagnostics](1107-realtime-message-expiry-diagnostics.md)
- [1108: Delivery TTL for direct realtime inbox messages](1108-direct-inbox-delivery-ttl.md)

- [ADR-933: Durable entity outbox relay and transport acceptance](933-durable-entity-outbox-relay.md)

- [ADR-934: Gated durable entity guest outbox protocol](934-durable-entity-guest-outbox-protocol.md)

- [ADR-935: Account-scoped durable entity inspection](935-durable-entity-owner-inspection.md)

- [ADR-936: Owner recovery for exhausted durable entity work](936-durable-entity-exhausted-work-recovery.md)

- [ADR-937: Bounded durable entity operational health](937-durable-entity-operational-health.md)

- [ADR-938: Typed durable entity SDK handles and guest transitions](938-typed-durable-entity-sdk.md)

- [ADR-939: Durable entity application-state schema migrations](939-durable-entity-application-schema-migrations.md)

- [ADR-940: Durable entity application-state export and restore](940-durable-entity-state-export-restore.md)

- [ADR-941: Durable entity owner state recovery API](941-durable-entity-owner-state-recovery-api.md)

- [ADR-907: Balloon free page reporting](907-balloon-free-page-reporting.md)

- [ADR-942: Scheduled durable entity backups and restore preview](942-durable-entity-scheduled-backups-and-restore-preview.md)

- [ADR-943: Application-validated durable entity restore](943-durable-entity-application-validated-restore.md)

- [ADR-519: application-reported workflow blocker ownership](519-workflow-blocker-ownership.md) — public responsibility, next-action guidance, and resolution attribution

- [ADR-904: Automation failure notification policy](904-automation-failure-notification-policy.md)
- [ADR-905: Automation failure admission pauses](905-automation-failure-admission-pauses.md)

- [ADR-934: Transactional event consumer SDK](934-transactional-event-consumer-sdk.md)

- [ADR-935: Commit application event publication](935-commit-application-event-publication.md)

- [ADR-944: Isolated durable entity restore validation](944-durable-entity-isolated-restore-validation.md)

- [ADR-945: Durable entity operation observability](945-durable-entity-operation-observability.md)

- [ADR-946: Durable entity validator release tooling](946-durable-entity-validator-release-tooling.md)

- [ADR-947: Shared durable entity validator artifacts](947-shared-durable-entity-validator-artifacts.md)

- [ADR-948: Built validator artifacts and project publication](948-built-validator-artifacts-and-project-publication.md)

- [ADR-866: Durable entity developer workflow](866-durable-entity-developer-workflow.md)
- [ADR-867: Durable entity operational visibility](867-durable-entity-operational-visibility.md)
- [ADR-868: Durable entity generated clients](868-durable-entity-generated-clients.md)
- [ADR-869: Reconnectable durable entity sessions](869-reconnectable-durable-entity-sessions.md)
- [ADR-870: Durable session recovery and lifecycle](870-durable-session-recovery-and-lifecycle.md)

- [ADR-977: Decompose cmd/apid into pkg/apid domain packages](977-apid-package-decomposition.md)

- [ADR-20261019120044: Deploy acceptance and secret ordering](20261019120044-deploy-acceptance-and-secret-ordering.md)

- [ADR-20261019120048: Saved deploy sessions](20261019120048-saved-deploy-sessions.md)

- [ADR-20261019120047: Atomic deploy settings recovery](20261019120047-atomic-deploy-settings-recovery.md)

- [ADR-20261019120049: Durable deployment submission receipts](20261019120049-deployment-submission-receipts.md)

- [ADR-20261019120050: Durable pre-submission profiling and tracing recovery](20261019120050-durable-deploy-policy-staging.md)

- [ADR-20261019120051: Durable manifest cron staging and recovery](20261019120051-durable-deploy-cron-staging.md)

- [ADR-20261019120010: Progressive project canary policy](20261019120010-project-progressive-canary-policy.md)

- [ADR-20261019120011: Durable project worker release pins](20261019120011-durable-project-worker-release-pins.md)

- [ADR-20261019120012: Retained project worker pools for pinned jobs](20261019120012-retained-project-worker-pools.md)

- [ADR-20261019120013: Staged project canary publication and transactional promotion](20261019120013-staged-project-canary-publication.md)

- [ADR-20261019120022: Snapshot-aware HTTP environment promotion](20261019120022-snapshot-aware-environment-promotion.md)

- [ADR-20261019120023: Health-gated HTTP environment promotion](20261019120023-health-gated-environment-promotion.md)

- [ADR-20261019120024: Destination-aware HTTP service graph promotion](20261019120024-destination-aware-service-graph-promotion.md)

- [ADR-1237: Local management MCP server](1237-local-management-mcp.md)
- [ADR-1238: Reviewed management MCP releases and delegated OAuth](1238-management-mcp-releases-and-oauth.md)

- [ADR-1239: Native browser authorization and durable management MCP connections](1239-management-mcp-browser-login-and-durability.md)
- [ADR-1240: Management MCP environment consent and real-client CI](1240-management-mcp-environments-and-client-ci.md)
- [ADR-1241: Reviewed named-environment staging and scoped health](1241-management-mcp-named-staging-and-scoped-health.md)
- [ADR-1242: Human browser approval for management MCP promotions](1242-management-mcp-browser-promotion-approval.md)
- [ADR-1243: Reviewed Git source staging through management MCP](1243-management-mcp-reviewed-git-source.md)

- [ADR-1244: Native service and worker rollouts through management MCP](1244-management-mcp-native-workload-rollouts.md)

- [ADR-1245: Environment-attributed management MCP observations](1245-management-mcp-scoped-telemetry.md)

- [ADR-1246: Reviewed native source rollouts through management MCP](1246-management-mcp-native-source-rollouts.md)

- [ADR-1247: Retained GitHub source rollback through management MCP](1247-management-mcp-retained-source-rollback.md)

- [ADR-1248: Current environment route-budget observations](1248-management-mcp-scoped-route-budgets.md)

- [ADR-1249: Bounded passive management MCP rollout watch](1249-management-mcp-rollout-watch.md)

- [ADR-1250: Consent-scoped management MCP resources and prompts](1250-management-mcp-context-resources-prompts.md)

- [ADR-1251: Read-only management MCP preflight](1251-management-mcp-preflight.md)

- [ADR-1252: Independent advisory environment route budgets](1252-environment-route-budgets.md)

- [ADR-1253: Bounded management MCP rollout progress](1253-management-mcp-rollout-progress.md)

- [ADR-1254: Consent-scoped management MCP resource subscriptions](1254-management-mcp-resource-subscriptions.md)

- [ADR-1255: Consent-scoped management MCP argument completion](1255-management-mcp-argument-completion.md)

- [ADR-1256: Bounded management MCP request admission](1256-management-mcp-request-admission.md)

- [ADR-1257: Private aggregate management MCP telemetry](1257-management-mcp-operational-telemetry.md)

- [ADR-1258: Graceful management MCP drain and journal cleanup](1258-management-mcp-graceful-drain.md)

- [ADR-1259: Owner-protected management MCP connection controls](1259-management-mcp-local-connection-controls.md)
- [ADR-1618: Exact retained request diagnostics through management MCP](1618-management-mcp-request-diagnostics.md)
- [ADR-1619: Filtered retained request discovery through management MCP](1619-management-mcp-request-discovery.md)
- [ADR-1620: Retained performance evidence through management MCP](1620-management-mcp-performance-evidence.md)
- [ADR-1621: Observed residency diagnostics through management MCP](1621-management-mcp-residency-diagnostics.md)
- [ADR-1622: Critical-path history through management MCP](1622-management-mcp-critical-path-history.md)
- [ADR-1623: Retained deployment comparison through management MCP](1623-management-mcp-deployment-comparison.md)
- [ADR-1624: Read-only regression inbox through management MCP](1624-management-mcp-regression-inbox.md)
- [ADR-1625: Structured request explanations through management MCP](1625-management-mcp-request-explanations.md)
- [ADR-1626: Per-request critical path through management MCP](1626-management-mcp-request-critical-path.md)
- [ADR-1627: Guest execution evidence through management MCP](1627-management-mcp-guest-execution.md)
- [ADR-1628: Request-linked regression evidence through management MCP](1628-management-mcp-request-regression.md)
- [ADR-1629: Request-linked dependency deployment comparisons through management MCP](1629-management-mcp-request-dependency-comparison.md)
- [ADR-1630: Dependency impact edges through management MCP](1630-management-mcp-dependency-impact.md)
- [ADR-1631: Route request analytics through management MCP](1631-management-mcp-request-analytics.md)
- [ADR-1632: Historical hourly request trends through management MCP](1632-management-mcp-request-timeseries.md)
- [ADR-1633: Grouped error triage through management MCP](1633-management-mcp-error-triage.md)
- [ADR-1634: Retained wake observations through management MCP](1634-management-mcp-wake-history.md)
- [ADR-1635: Exact wake-stage inspection through management MCP](1635-management-mcp-wake-timeline.md)
- [ADR-1636: Request-to-wake drill-down through management MCP](1636-management-mcp-request-wake-association.md)
- [ADR-1637: Lossless retained wake-timeline continuation](1637-management-mcp-wake-timeline-pagination.md)
- [ADR-1638: Window-first wake-history pagination](1638-management-mcp-wake-history-pagination.md)
- [ADR-1639: Read-only sampled CPU profiles through management MCP](1639-management-mcp-cpu-profiles.md)

- [ADR-1260: Authorization-bounded management MCP request leases](1260-management-mcp-authorization-request-leases.md)

- [ADR-1115: Issue occurrence split preview](1115-issue-occurrence-split-preview.md)

- [ADR-1116: Durable issue occurrence split rules](1116-durable-issue-occurrence-split-rules.md)

- [ADR-1117 · Cause-aware issue grouping](1117-cause-aware-issue-grouping.md)

- [ADR-1118 · Issue split-rule management](1118-issue-split-rule-management.md)

- [ADR-20261016110803 · Workflow Operation execution budget](20261016110803-workflow-operation-execution-budget.md)
- [ADR-20261016123221 · Customer Operations telemetry](20261016123221-customer-operations-telemetry.md)
- [ADR-20261017100853 · Customer self-service Operation signals](20261017100853-customer-self-service-operation-signals.md)
- [ADR-20261017102231 · Leaving the Customer Operations preview](20261017102231-operations-preview-exit.md)
- [ADR-20261016100929 · Webhook-delivered Operation signals](20261016100929-webhook-delivered-operation-signals.md)
- [ADR-1197 · Operation event waits and signals](1197-operation-event-waits-and-signals.md)
- [ADR-1196 · Operation deadlines and budgets](1196-operation-deadlines-and-budgets.md)
- [ADR-1194 · Operation waits and approvals](1194-operation-waits-and-approvals.md)
- [ADR-1193 · Webhook-submitted customer Operations](1193-webhook-submitted-customer-operations.md)

- [ADR-981: Scenario TCP fault injection](981-scenario-tcp-fault-injection.md)
- [ADR-982: Decompose cmd/apid into pkg/apid domain packages](982-apid-package-decomposition.md)

- [ADR-1111: Typed Go SDK methods for the managed realtime release](1111-go-sdk-managed-realtime-release.md)

- [ADR-983: Firecracker 1.17 and guest kernel 6.18](983-firecracker-1-17-and-guest-kernel-6-18.md)


- [ADR-1027: Object storage compute clients and write canaries](1027-object-storage-compute-clients-and-write-canaries.md)

- [ADR-1028: Object storage operation release gates](1028-object-storage-operation-release-gates.md)

- [ADR-1029: Binding preflight against stored release policies](1029-binding-preflight-stored-release-policy.md)

- [ADR-1030: Policy-driven storage canary verification](1030-policy-driven-storage-canary-verification.md)

- [ADR-1031: Node compute secret reload coordinator](1031-node-compute-secret-reload-coordinator.md)
- [ADR-1032: Go and Python compute secret reload coordinators](1032-go-python-compute-secret-reload-coordinators.md)
- [ADR-1033: Storage client leases and retirement](1033-storage-client-leases-and-retirement.md)

- [ADR-1034: Managed storage Node compute starter](1034-managed-storage-node-compute-starter.md)
- [ADR-1035: Managed object storage for developer sessions](1035-developer-managed-object-storage.md)

- [ADR-1036: Declarative bucket triggers for the owning compute app](1036-managed-object-storage-compute-triggers.md)
- [ADR-1038: Durable S3 multipart checksums](1038-durable-s3-multipart-checksums.md)
- [ADR-908: Shared runtime base through virtio-pmem DAX](908-shared-runtime-base-pmem-dax.md)

- [ADR-909: Admission by measured memory](909-measured-memory-admission.md)

- [ADR-910: Per-plan block rate limits](910-block-rate-limits.md)

- [ADR-1456: Deployment-bound Issues source-map artifacts](1456-deployment-issue-source-map-artifacts.md)

- [ADR-1457: Labeled Issues grouping evaluation](1457-issue-grouping-evaluation-corpus.md)
- [1458: Bounded operational context for Issues](1458-bounded-issue-operational-context.md)

- [ADR-1459: Observed release comparison and atomic bulk issue triage](1459-issue-release-comparison-and-bulk-triage.md)

- [ADR-1460: Shared issue views and source/tag ownership routing](1460-shared-issue-views-and-source-routing.md)
- [ADR-1348: Checked project binding release graphs](1348-checked-project-binding-release-graphs.md)

- [ADR-1352: Push queue binding handler completion canaries](1352-push-queue-binding-completion-canaries.md)

- [ADR-1353: Bounded parallel binding verification](1353-bounded-parallel-binding-verification.md)

- [ADR-1354: Ledger-backed canary dispatch authority](1354-ledger-backed-canary-dispatch-authority.md)

- [ADR-1355: Durable single-app binding verification runs](1355-durable-single-app-binding-verification-runs.md)

- [ADR-1356: Durable project binding verification runs](1356-durable-project-binding-verification-runs.md)

- [ADR-1357: Server-generated binding verification reviews](1357-server-generated-binding-verification-reviews.md)

- [ADR-1358: Reusable binding verification runners](1358-reusable-binding-verification-runners.md)

- [ADR-1359: Binding verification run diagnostics](1359-binding-verification-run-diagnostics.md)

- [ADR-1360: Shared verification admission budgets](1360-shared-verification-admission-budgets.md)

- [ADR-1361: Automatic verification recovery diagnostics](1361-automatic-verification-recovery-diagnostics.md)

- [ADR-1365: Verification qualification waiting](1365-verification-qualification-waiting.md)

- [ADR-1366: Verification write reconciliation](1366-verification-write-reconciliation.md)

- [ADR-1367: Binding CLI progress and recovery summaries](1367-binding-cli-progress.md)

- [ADR-1368: Verification creation reconciliation](1368-verification-creation-reconciliation.md)

- [ADR-1369: PostgreSQL pool runtime bindings](1369-postgres-pool-runtime-bindings.md)

- [ADR-1370: Canonical PostgreSQL starter bindings](1370-canonical-postgres-starter-bindings.md)

- [ADR-1371: Async Python PostgreSQL bindings](1371-async-python-postgres-bindings.md)

- [ADR-1372: Python PostgreSQL starter recovery](1372-python-postgres-starter-recovery.md)

- [ADR-1373: Async Python object-storage bindings](1373-async-python-object-storage-bindings.md)

- [ADR-1374: Python object-storage starter recovery](1374-python-object-storage-starter-recovery.md)

- [ADR-1375: Node object-storage starter recovery](1375-node-object-storage-starter-recovery.md)

- [ADR-1376: Native object-storage starter acceptance](1376-native-object-storage-starter-acceptance.md)

- [ADR-1377: Python storage explicit TLS trust](1377-python-storage-explicit-tls-trust.md)

- [ADR-1378: Native storage starter runtime matrix](1378-native-storage-starter-runtime-matrix.md)

- [ADR-1379: PostgreSQL native acceptance repair](1379-postgres-native-acceptance-repair.md)
- [ADR-1380: Python PostgreSQL native acceptance](1380-python-postgres-native-acceptance.md)
- [ADR-1381: Queue worker completion acceptance](1381-queue-worker-completion-acceptance.md)

- [ADR-1658: Guided migration of an existing service](1658-guided-service-migration.md)
- [ADR-1659: Resumable migration verification](1659-resumable-migration-verification.md)
- [ADR-1660: Background migration verification and service recovery](1660-background-migration-verification.md)
- [ADR-1661: Saved production cutover and DNS recovery](1661-guided-production-cutover.md)
- [ADR-1662: Saved multi-service migration progress](1662-project-migration-journal.md)
- [ADR-1663: Separate migration service names from app names](1663-migration-service-app-names.md)
- [ADR-1664: Map migration runtime variables to selected services](1664-migration-runtime-connections.md)
- [ADR-1665: Configure public frontend URLs before migration builds](1665-public-frontend-migration-urls.md)
- [ADR-1666: Wait for selected APIs before migration builds](1666-migration-build-dependency-waits.md)

- [ADR-1260: Initial-failure policy dry run](1260-initial-failure-policy-dry-run.md)
- [ADR-1261: Durable first-recovery reviews](1261-durable-initial-recovery-reviews.md)
- [ADR-1135: Sparse extent encoding for remote drives and snapshot memory](1135-sparse-extent-encoding-for-remote-artifacts.md)
- [ADR-20261015120000000: Operator incident evidence](20261015120000000-operator-incident-evidence.md)
- [ADR-20261016120000000: Operator alert workload summary](20261016120000000-operator-alert-workload-summary.md)


[ADR-202610171028: Site hosting GitHub OIDC](202610171028-site-hosting-github-oidc.md) specifies explicit immutable trust and short-lived preview credentials.

[ADR-202610171059: Read-only hosting diagnostics](202610171059-read-only-hosting-diagnostics.md) defines bounded evidence and advisory recovery without provider probes or mutations.
- [ADR-1133: Undelivered service calls re-route outside the retry budget](1133-undelivered-service-calls-reroute.md)
- [ADR-1131: Customer service map from unsampled service-proxy edges](1131-customer-service-map.md)

- [ADR-1134: Wake-ahead learns private TCP edges](1134-wake-ahead-learns-private-tcp.md)
- [ADR-1132: Wake-ahead evidence survives gateway restarts](1132-wake-ahead-evidence-survives-restarts.md)

- [ADR-1331: Retained GitOps workload retirement and reviewed recovery](1331-gitops-workload-retirement.md)
- [ADR-20261017190801: Provider-bound GitLab review and workload provenance](20261017190801-gitlab-reviewed-gitops.md)

- [ADR-1520: Rate-limit token leasing](1520-rate-limit-token-leasing.md)
- [ADR-1522: Recoverable operator restart execution](1522-recoverable-operator-restart-execution.md)
- [ADR-1521: The request-ID journal is written in batches](1521-batched-request-id-journal.md)
- [ADR-1523: Durable operator cold-boot recovery](1523-durable-operator-cold-boot-recovery.md)
- [ADR-1524: Durable operator recovery submissions](1524-durable-operator-recovery-submissions.md)

- [ADR-1525: Recoverable operator park execution](1525-recoverable-operator-park-execution.md) — generation-bound capture/stop, durable cleanup receipts and atomic verified completion; native qualification pending.

- [ADR-1526: Atomic operator node maintenance](1526-atomic-operator-node-maintenance.md) — claim-fenced lifecycle transitions, immutable outcomes and transactional audit handoff.

- [ADR-1527: Generation-fenced node lifecycle completion](1527-generation-fenced-node-completion.md) — captured maintenance cycles, atomic work checks, immutable receipts and completion audit handoff.

- [ADR-1528: Node admission generation fencing](1528-node-admission-generation-fencing.md) — transactional lifecycle/generation checks, fresh placement retries and migration destination validation.

- [ADR-1529: Durable inbound migration reservations](1529-durable-inbound-migration-reservations.md) — persistent destination capacity, one adoption dispatch, original-generation cleanup and atomic ownership conversion.
- [ADR-1740: Frozen fast path — a thaw is a ledger check, the vmmd call beside one read, and one guarded update](1740-frozen-fast-path.md)
- [ADR-1741: schedd owner cache — per-app runtime generations and one guarded write per wake](1741-schedd-owner-cache.md)

- [ADR-1530: Durable migration source retirement](1530-durable-migration-source-retirement.md) — original physical source receipts, atomic retirement accounting and maintenance fencing after ownership moves.

- [ADR 1461: App-scoped issue teams and explicit CODEOWNERS import](1461-issue-teams-and-codeowners.md)

- [ADR 1462: Slack alerts through verified Issues webhook receivers](1462-issue-slack-webhook-bridge.md)

- [ADR 1463: Native issue tracker references](1463-issue-tracker-links.md)

- [ADR 1464: Explicit provider ticket creation with durable CLI recovery](1464-issue-ticket-creation-journal.md)

- [ADR 1465: Explicit tracker lifecycle synchronization](1465-explicit-tracker-lifecycle-sync.md)

- [ADR 1466: Native GitHub connections for issue status](1466-native-github-issue-status.md)

- [ADR-1467: Native GitHub ticket creation receipts](1467-native-github-ticket-creation.md)

- [ADR-1468: Opt-in automatic native GitHub lifecycle reconciliation](1468-automatic-native-github-lifecycle.md)

- [ADR-1469: Verified Linear credential boundary](1469-linear-connection-credential-boundary.md)

- [ADR-1470: Durable Linear Issues connections](1470-durable-linear-issue-connections.md)

- [ADR-1471: Native Linear ticket creation and UUID recovery](1471-native-linear-ticket-creation.md)

- [ADR-1472: Explicit native Linear lifecycle sync](1472-native-linear-lifecycle-sync.md)

- [ADR-1473: Automatic native Linear lifecycle sync](1473-automatic-native-linear-lifecycle.md)

- [ADR-1474: Dashboard tracker sync controls](1474-dashboard-tracker-sync-controls.md)

- [ADR-1475: Named Linear workflow picker](1475-named-linear-workflow-picker.md)

- [ADR-1476: Provider issue callbacks as durable refresh hints](1476-provider-issue-callback-hints.md)

- [ADR-1477: Callback rejection diagnostics and setup evidence](1477-callback-rejection-diagnostics-and-setup-evidence.md)

- [ADR-1478: Linear Issue tracker OAuth](1478-linear-issue-tracker-oauth.md)

- [ADR-1479: Read-only inbound tracker lifecycle assessment](1479-issue-tracker-inbound-preview.md)

- [ADR-1480: Explicit inbound tracker lifecycle application](1480-explicit-inbound-tracker-lifecycle.md)

- [ADR-1481: Opt-in directional tracker sync and automatic reopenings](1481-directional-issue-tracker-reopenings.md)

- [ADR-1482: Tracker closure review queue and deployment evidence](1482-tracker-closure-review-queue.md)

- [ADR-1483: Durable sync health and read-only recovery inspections](1483-tracker-sync-health-and-inspections.md)

- [ADR-1484: Opt-in tracker sync notifications and ownership routing](1484-tracker-sync-notifications-and-team-routing.md)

- [ADR-1485: Tracker notification acknowledgment and claims](1485-tracker-notification-acknowledgment-and-claims.md)

- [ADR-1486: Tracker incident snoozing and automatic resurfacing](1486-tracker-incident-snoozing-and-resurfacing.md)

- [ADR-1487: Bounded reminders for unacknowledged tracker incidents](1487-unacknowledged-tracker-incident-reminders.md)

- [ADR-1488: One-step backup team escalation for tracker incidents](1488-backup-team-tracker-incident-escalation.md)

- [ADR-1489: Team Slack destinations for tracker incidents](1489-team-slack-incident-destinations.md)

- [ADR-1490: Application issue event-frequency alert rules](1490-issue-frequency-alert-rules.md)

- [ADR-1491: Native Slack delivery for application issue alerts](1491-native-application-issue-slack.md)

- [ADR-1492: Native application issue acknowledgment and snoozing](1492-native-application-issue-response.md)

- [ADR-1493: Reminders for unacknowledged native application issues](1493-native-application-issue-reminders.md)

- [ADR-1494: Backup-team escalation for native application issues](1494-native-application-issue-escalation.md)

- [ADR-1495: Native application issue response inbox](1495-native-application-issue-response-inbox.md)

- [ADR-1496: Native Slack response actions and account links](1496-native-slack-response-actions.md)

- [ADR-1497: Native Slack OAuth installation](1497-native-slack-oauth-installation.md)

- [ADR-1498: Native Slack channel routing rules](1498-native-slack-channel-routing.md)

- [ADR-1499: Native issue lifecycle alert policy](1499-issue-lifecycle-alert-policy.md)
- [ADR-1500: Source-built apps run on the shared runtime base](1500-shared-runtime-source-apps.md)
- [ADR-1501: Reconcile worker pools once at scheduler start](1501-worker-startup-reconcile.md)
- [ADR-1591: Managed Postgres data pipelines](1591-managed-postgres-data-pipelines.md)

- [ADR-20261017190802: Reviewed source-backed scheduled Jobs](20261017190802-gitops-source-scheduled-jobs.md)

- [ADR-20261017190803: Read-only GitOps promotion proposals](20261017190803-gitops-promotion-proposals.md)

- [ADR-20261017190804: Reuse signed build artifacts across GitOps environments](20261017190804-gitops-built-artifact-reuse.md)

- [ADR-20261017190805: Reviewed GitOps rollback proposals](20261017190805-gitops-reviewed-rollback-proposals.md)

- [ADR-20261017190806: GitOps recovery catalog](20261017190806-gitops-recovery-catalog.md)

- [ADR-20261017190807: Bounded GitOps recovery artifact retention](20261017190807-gitops-bounded-recovery-retention.md)

- [ADR-20261017190808: Signed artifact promotion and rollback for scheduled Jobs](20261017190808-gitops-scheduled-job-artifact-promotion-and-rollback.md)

- [ADR-20261017190809: Reuse signed builds for unchanged isolated GitOps contexts](20261017190809-gitops-unchanged-build-context-reuse.md)

- [ADR-20261017190810: Reviewed GitOps promotion pull and merge requests](20261017190810-gitops-promotion-review-requests.md)

- [ADR-20261017190811: Reviewed GitOps recovery pull and merge requests](20261017190811-gitops-rollback-review-requests.md)

- [ADR-20261017190812: Wait for a GitOps commit's recorded serving evidence](20261017190812-gitops-wait-for-serving-commit.md)

- [ADR-20261017190813: Durable GitOps rollout outcome notifications](20261017190813-gitops-rollout-outcome-notifications.md)

- [ADR-20261017191501: Browser and Task migration parity for Go/Python MCP starters](20261017191501-mcp-go-python-starter-parity.md)
- [ADR-20261017191502: Bounded starter protocol observations](20261017191502-mcp-starter-protocol-telemetry.md)
- [ADR-20261017191503: Read-only MCP operations dashboard](20261017191503-read-only-mcp-operations-dashboard.md)
- [ADR 20261017191504: MCP dashboard grant retirement recovery](20261017191504-mcp-dashboard-grant-retirement-recovery.md)
- [ADR 20261017191505: MCP browser OAuth and guarded renewal recovery](20261017191505-mcp-browser-oauth-and-renewal-recovery.md)
- [ADR 20261017191506: MCP protocol performance aggregation](20261017191506-mcp-protocol-performance.md)
- [ADR 20261017191507: Customer MCP performance alerts](20261017191507-customer-mcp-performance-alerts.md)
- [ADR 20261017191508: Independent MCP telemetry heartbeats](20261017191508-mcp-telemetry-heartbeats.md)
- [ADR 20261017191509: Public deployment MCP readiness](20261017191509-deployment-mcp-readiness.md)
- [ADR 20261017191510: Durable platform MCP readiness admission](20261017191510-platform-mcp-readiness-gate.md)
- [ADR-20261017191511: Continuous MCP readiness monitoring](20261017191511-continuous-mcp-readiness.md)
- [ADR-20261017191512: Managed OAuth credentials for MCP readiness](20261017191512-managed-oauth-readiness.md)
- [ADR 20261017191513: Environment-scoped MCP OAuth connections](20261017191513-environment-scoped-mcp-oauth.md)
- [ADR 20261017191514: Environment-scoped MCP readiness policies](20261017191514-environment-readiness-policies.md)
- [ADR 20261017191515: MCP readiness policy history and restore](20261017191515-mcp-readiness-policy-history.md)
- [1274: Environment-scoped MCP readiness alerts](20261017191516-environment-scoped-mcp-readiness-alerts.md)
- [1275: MCP readiness incident lifecycle](20261017191517-mcp-readiness-incident-lifecycle.md)
- [1276: Independent MCP readiness incidents and delivery outbox](20261017191518-independent-mcp-readiness-outbox.md)
- [1277: MCP readiness maintenance windows and notification silences](20261017191519-mcp-readiness-maintenance-windows.md)
- [1278: Readiness notification queue health and operator recovery](20261017191520-mcp-readiness-notification-recovery.md)
- [1279: Incident acknowledgement and finite readiness reminders](20261017191521-mcp-readiness-incident-attention.md)
- [1280: Bounded readiness webhook failover and incident routing](20261017191522-mcp-readiness-fallback-delivery.md)
- [ADR-20261017191523: Independent MCP readiness destination queues](20261017191523-mcp-readiness-destination-queues.md)
- [ADR-20261017191524: Versioned readiness reminder defaults](20261017191524-mcp-readiness-reminder-defaults.md)
- [ADR-20261017191525: Native PagerDuty readiness delivery](20261017191525-mcp-readiness-pagerduty.md)
- [ADR-20261017191526: Regional PagerDuty readiness delivery](20261017191526-mcp-readiness-pagerduty-regions.md)
- [ADR-20261017191527: Signed PagerDuty readiness acknowledgements](20261017191527-mcp-readiness-pagerduty-acknowledgements.md)
- [ADR-20261017191528: Native PagerDuty readiness fallback](20261017191528-mcp-readiness-pagerduty-fallback.md)
- [ADR 20261017191529: Independent PagerDuty acknowledgement bindings](20261017191529-mcp-readiness-independent-pagerduty-bindings.md)
- [ADR 20261017191530: Opt-in PagerDuty acknowledgement release](20261017191530-mcp-readiness-pagerduty-release.md)

- [Durable worker-pool recovery](20261017221000-durable-worker-recovery.md)
- [Worker recovery diagnostics and alerts](20261017223000-worker-recovery-diagnostics.md)
- [Worker admission explanations](20261018090000-worker-admission-explanations.md)
- [ADR 202610171900: Release compatibility gates (N±1, deploy order, skew and migration checks, SLO gate)](202610171900-release-compatibility-gates.md)
- [ADR-1700: Platform flags: per-account release gates managed at runtime](1700-platform-flags.md)

- [Strict app compute reservations](1750-strict-app-compute-reservations.md)
- [ADR-1746: Shared account compute funding](1746-account-strict-compute.md)
- [ADR-1747: Independent job compute funding](1747-job-strict-compute.md)
- [ADR-1748: Shared account compute and egress funding](1748-account-strict-egress.md)
- [ADR-1749: Funded public forwarding](1749-public-forwarding-egress.md)
- [ADR-1650: Per-unit control-plane releases](1650-per-unit-control-plane-releases.md)

- [ADR-20261017210000: Reviewed regression triage through management MCP](20261017210000-management-mcp-regression-triage.md)

- [ADR-20261017222622: Consented regression resource snapshots and subscriptions](20261017222622-management-mcp-regression-resources.md)

- [ADR-20261017224029: Selected-app regression inbox through management MCP](20261017224029-management-mcp-fleet-regressions.md)

- [ADR-20261017232000: Incident evidence bundles through management MCP](20261017232000-management-mcp-incident-bundles.md)

- [ADR-20261017233500: Explicit selected-app traces through management MCP](20261017233500-management-mcp-selected-traces.md)

- [ADR-1136: Project environment configuration reaches every service as environment variables](1136-project-environment-config-as-environment-variables.md)
- [ADR-1137: Declared dependencies seed opt-in wake-ahead](1137-declared-dependencies-seed-wake-ahead.md) — an opted-in app wakes its declared dependencies until 20 wakes of evidence decide (amends ADR-977)
- [ADR-1138: Service calls join the caller's trace when it serves one request](1138-service-calls-join-the-callers-trace.md) — a call without traceparent from an instance serving exactly one request continues that request's trace, marked trace_inferred
- [ADR-1139: Project environment secrets reach every service of the environment](1139-project-environment-secrets.md) — sealed secrets set once per project environment, below each app's own values, covered by rekey and key retirement (amends ADR-1136)

- [ADR-20261018181846: Route priority in the cold-wake queue](20261018181846-route-priority-in-the-wake-queue.md)

- [ADR-20261020161310: Route insight fixes, dashboard panel and alerts](20261020161310-route-insight-fixes-and-alerts.md)

- [ADR-20261022184757: Background work and waitUntil in the awake report](20261022184757-route-awake-background-work.md)
- [ADR-20261023220532: Move the Debian-based runtime bases to Debian 13](20261023220532-debian-13-runtime-bases.md) — accepted; clears the bookworm `<no-dsa>` glibc/SQLite criticals (#5206), matches Railpack's Debian 13 builder, audits `base-minimal` apps first
- [ADR-20261024154324: Route subsystems at public scale](20261024154324-route-subsystems-at-public-scale.md) — per-gateway per-route series budget with idle eviction, fair route insight scheduling, concurrent route monitor drain, route worker alerts (amends ADR-093, ADR-1198)
- [ADR-20261024230731: Rehearsal checks on webhook deliveries and private DNS stay off other apps' critical path](20261024230731-rehearsal-webhook-and-dns-hot-paths.md)
- [ADR-20261025114554: Anonymous Docker Hub pulls go through operator mirrors](20261025114554-docker-hub-mirrors.md) — `FAAS_DOCKER_HUB_MIRRORS` / `faas_docker_hub_mirrors`: imaged and builder BuildKit try mirrors (e.g. `mirror.gcr.io`) before Docker Hub; never with a credential
- [ADR-20261025124445: Cloud registries: Basic challenges and ECR access keys](20261025124445-cloud-registry-auth.md) — Basic-challenge registries (ECR, htpasswd) get the pull credential over HTTPS; an IAM access key stored for an ECR host is exchanged for ECR's 12-hour password; GAR/ACR documented
- [ADR-20261025111339: Guests give workloads the environment a container runtime provides](20261025111339-guest-container-runtime-parity.md) — `/dev/shm`, `/dev/pts`, 65,536/1,048,576 open files, hostname and `/etc/hosts` entries, image `/tmp` files kept, passwd HOME, default PATH and created WORKDIR for apps
- [ADR-20261026090000: Dockerfile builds pull private base images with the app's registry credentials](20261026090000-builder-private-base-images.md) — builderd forwards the sealed rows, vmmd (now a `build`-domain reader) opens them into the builder's Docker config; no warm builder, own cache scope, ECR access keys skipped
- [ADR-20261026143210: Route labels go to routes the app serves](20261026143210-route-labels-for-served-routes.md) — admit a route label only after the app answers it (scanner 404s and made-up methods no longer blind routes features), 10 s bound on routes analytics reads, Free-plan checks on route controls (amends ADR-093 D2)
