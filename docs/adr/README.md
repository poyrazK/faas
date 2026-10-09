# Architecture Decision Records

ADR-001 through ADR-010 are **accepted and locked for v1**; they live inline in
[`../faas_implementation_spec.md`](../faas_implementation_spec.md) §3, not as
separate files here. This directory holds ADRs made *after* the spec.

Any deviation from the spec requires a new ADR here first (spec §3, CLAUDE.md).

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
| 740 | [Customer service map](740-customer-service-map.md) | proposed | Account-scoped caller → target map from unsampled service-proxy edge metrics |
| 731 | [Durable PostgreSQL lifecycle qualification](731-managed-postgres-durable-qualification.md) | accepted | Version-8 SQL restart, encrypted credential delivery, workload rotation and cleanup evidence |
| 687 | [Object version listing and bound historical downloads](687-object-version-cli-and-bound-downloads.md) | accepted | Public immutable version identities, bounded listings and exact-version gateway read authority |
| 688 | [Resumable CLI object uploads](688-resumable-cli-object-uploads.md) | accepted | Private fingerprint-bound multipart checkpoints and uncertain-completion recovery |
| 650 | [Schema-generated Data APIs](650-schema-generated-data-api.md) | accepted | Ordinary app lifecycle, schema-restricted bindings, private type export and typed application clients |
| 712 | [Object-storage durable entities](712-object-storage-durable-entities.md) | internal prototype; qualification pending | SQL-free entity state and retry receipts, opt-in alarms and checkpointed cleanup |
| 630 | [Guest-init-only release reuse](630-guest-init-only-release-reuse.md) | proposed | Patch PID 1 into staged bases instead of rebuilding them, and key the builder cache on the builder image plus a guest-init build contract version |
| 634 | [Asynchronous request-ID journal](634-async-request-id-journal.md) | proposed | The exact request-ID index is queued and written by bounded workers; a failed or dropped write never fails the request |
| 635 | [Authenticated tenant workflow continuations](635-tenant-workflow-continuations.md) | accepted | Tenant-authenticated event and callback continuation routes with atomic live-link checks |
| 636 | [Tenant-scoped scheduled workflow starts](636-tenant-scheduled-workflow-starts.md) | accepted | Per-tenant schedule cursors and atomic tenant-bound run admission |
| 637 | [Tenant-configurable workflow schedules](637-tenant-configurable-workflow-schedules.md) | accepted | Explicitly opt in to tenant-owned cadence, overlap, and enablement settings |
| 640 | [Watchdog claims WAKING rows before teardown](640-watchdog-claims-waking-rows-before-teardown.md) | proposed | Every WAKING row gets the restore + cold-boot budget, and the watchdog CASes it to COLD_BOOTING before Destroy so a peer schedd's RUNNING publication can never point at a destroyed VM |
| 641 | [Edge health follows the last instance](641-edge-health-follows-last-instance.md) | proposed | A parked app's edge health answer comes from its last instance (only FAILED is unhealthy), cached 15 s per gateway, instead of the process-local wake outcome that went unknown after every restart |
| 642 | [Restore-safe guest timers](642-restore-safe-guest-timers.md) | proposed | Guests boot with kvm-clock and the one-shot LAPIC timer because Firecracker 1.7 restores lost TSC-deadline interrupts; backing identity v2 refuses captures booted with the old profile |
| 643 | [Park-to-admit for refused wakes](643-park-to-admit-refused-wakes.md) | proposed | A gateway wake refused for fleet capacity parks one idle, floor-respecting instance of another owned app and retries once, instead of answering 503 until the idle timeout |
| 680 | [Restored processes reseed their userspace random generators before serving](680-restore-userspace-rng-reseed.md) | accepted | guest-init reseed barrier with Node (N-API RAND_poll addon) and Python preloads; fails closed to cold boot; GHSA-24j2-p895-mwc9 |
| 790 | [meterd catches up closed minutes it did not roll](790-meterd-closed-minute-catch-up.md) | proposed | A sample tick re-rolls, from the billing ledger, closed minutes of the last 24 h never recorded compute-complete (≤15 per tick); idempotent through first-write-wins; H5-55 |
| 644 | [Verified MCP promotion and resource policy](644-mcp-verified-promotion-and-resource-policy.md) | accepted | Verify zero-traffic candidates, gate caller catalogs, and reuse gateway JWT resource authorization |
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
| 576 | [Private TCP addressing between services](576-private-tcp-service-addressing.md) | proposed | Account-scoped service addresses in 198.19.0.0/16 give non-HTTP protocols natural-port private reachability through a node-local TCP proxy |
| 520 | [Self-hosted TLS for customer custom domains](520-self-hosted-custom-domain-tls.md) | proposed | Caddy on-demand certificates gated by gatewayd-public's ask endpoint; durable per-wildcard issuance budget; no CDN in the customer-domain path |
| 580 | [Control-plane role convergence in CD](580-control-plane-convergence-in-cd.md) | proposed | CD runs the control-plane bootstrap play before release activation whenever a hash of its inputs (roles, inventory, operator vars) changes |
| 567 | [Runtime base convergence](567-runtime-base-convergence.md) | proposed | Keep each node's cached runtime base byte-identical to its shared publication so ADR-510 snapshots restore on any node |
| 510 | [Snapshot backing image identity](510-snapshot-backing-image-identity.md) | proposed | Restore only onto the kernel and read-only base a capture was taken with; refuse and cold-boot otherwise |
| 516 | [Replay recent managed PostgreSQL usage corrections](516-managed-postgres-usage-correction-replay.md) | accepted | Recover missing windows first, bound recent revision replay, and reject unsafe collection windows |
| 566 | [Financial visibility and scoped spending controls](566-financial-visibility-and-scoped-budgets.md) | visibility preview; enforcement pending | Retained compute/egress costs, forecasts and disabled budget drafts; runtime enforcement has separate acceptance gates |
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
| 424 | [Managed outbound integrations for stateless Runs](424-run-scoped-managed-outbound-integrations.md) | proposed | Explicit account grants and a bounded vsock broker; the execution VM remains networkless |
| 427 | [Exclusive operation policy retirement](427-exclusive-operation-policy-retirement.md) | accepted | Idle retirement preserves ownership history and releases the active policy quota slot |
| 428 | [Native gRPC request stream admission](428-native-grpc-request-stream-admission.md) | accepted | Incremental bounded native gRPC requests and duplex response controls through the gateway handler |
| 429 | [Handled internal service request evidence](429-handled-service-request-evidence.md) | accepted | Actual guest response evidence, exact registered-scenario timestamps and truthful cleanup phases |
| 423 | [Scenario-scoped chaos testing](423-scenario-scoped-chaos-testing.md) | accepted | Bounded request faults on registered test-run service calls, with deterministic selection, expiry, and lifecycle-profile execution |
| 392 | [Event and trace wire conformance](392-event-and-trace-wire-conformance.md) | accepted | OTLP/HTTP encodings and standard responses, CloudEvents attributes, and pinned official AsyncAPI validation |
| 391 | [Provider invoice history discovery and backfill](391-provider-invoice-history-backfill.md) | accepted | Authenticated provider history discovery with bounded, customer-scoped imports |
| 390 | [Authenticated provider invoice refresh](390-provider-invoice-refresh.md) | accepted | Authenticated provider facts, bounded reads, and atomic enrichment |
| 389 | [Invoice detail lifecycle tracking](389-invoice-detail-lifecycle.md) | accepted | Independent record timestamps, exact row fingerprints, and historical coverage |
| 388 | [Provider invoice facts for FOCUS](388-provider-invoice-facts.md) | accepted | Durable provider facts, reconciled line classifications, and source coverage |
| 387 | [FOCUS invoice projection](387-focus-invoice-projection.md) | accepted | Account-scoped invoice CSV and metadata, exact reconciliation, and declared source gaps |
| 461 | [Managed PostgreSQL credential privileges](482-managed-postgres-credential-privileges.md) | accepted for gated preview | SQL runtime and migration logins, stable schema ownership, and verified role isolation |
| 386 | [HTTP/1 upgrade socket ownership](386-http1-upgrade-socket-ownership.md) | accepted | Hijack successful raw upgrades, retain buffered duplex bytes, and cancel both directions on session closure |
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
| 104 | Dimensional throttle keying (issue #881 Phase 3 + amendment 6) — per-rule opt-in `key_by ∈ {"none","api_key","consumer_id","jwt_subject","jwt_claim","country"}` extends `EdgeRuleThrottleAction` with bounded request-concept buckets; `missing_key_policy ∈ {"shared","reject"}` prevents credential omission bypass; verified scalar JWT claims are available independently of `required_claims`; central mode maps dimensions into bounded deterministic UUID shards without storing raw claims or widening `pg_ratelimit_counters`; local mode retains the non-evicting `__other__` collapse; no DDL because the action is jsonb and central subjects remain UUIDs | accepted | issue #881 Phase 3; amendment 6 (2026-09-22) enables country, arbitrary scalar JWT claims, strict missing identities, and cross-replica dimensional enforcement |
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

- [ADR-530: S3 compatibility and multipart capacity admission](530-s3-compatibility-and-multipart-capacity.md)
- [ADR-531: S3 multipart transfer fencing and cleanup](531-s3-multipart-transfer-fencing-and-cleanup.md)
- [ADR-532: Conditional S3 multipart completion](532-conditional-s3-multipart-completion.md)
- [ADR-533: Safe object capacity reconciliation](533-safe-object-capacity-reconciliation.md)
- [ADR-534: Recoverable application object uploads](534-recoverable-application-object-uploads.md)
- [ADR-535: Recoverable S3 gateway PUTs](535-recoverable-s3-gateway-puts.md)
- [ADR-536: Recoverable S3 gateway copies](536-recoverable-s3-gateway-copies.md)
- [ADR-537: Customer object write receipts](537-customer-object-write-receipts.md)
- [ADR-538: S3 multipart copy and source ETag conditions](538-s3-multipart-copy-and-source-etag-conditions.md)
- [ADR-539: Historical S3 write receipt recovery](539-historical-s3-write-receipt-recovery.md)
- [ADR-540: Native S3 version capacity inventory](540-native-s3-version-capacity-inventory.md)
- [ADR-541: Immutable S3 copy sources and date conditions](541-immutable-s3-copy-sources-and-date-conditions.md)
- [ADR-542: Customer S3 version identities and reads](542-customer-s3-version-identities-and-reads.md)
- [ADR-543: Customer-selected S3 copy sources](543-customer-selected-s3-copy-sources.md)
- [ADR-544: Durable S3 multipart completion identities](544-durable-s3-multipart-completion-identities.md)
- [ADR-545: Durable bucket versioning configuration](545-durable-bucket-versioning-configuration.md)
- [ADR-546: Permanent immutable S3 version deletion](546-immutable-s3-version-deletion.md)
- [ADR-547: Durable ordinary and mutable null S3 deletion](547-durable-s3-mutable-deletion.md)
- [ADR-548: Coordinate immutable deletion with version inventory](548-immutable-deletion-inventory-coordination.md)
- [ADR-549: Tag current and retained object versions](549-version-specific-object-tagging.md)
- [ADR-550: Durable object lifecycle discovery and expiration](550-durable-object-lifecycle.md)
- [ADR-551: Atomic object mutation events](551-atomic-object-mutation-events.md)
- [ADR-552: Owned S3 notification destinations](552-owned-s3-notification-destinations.md)
- [ADR-553: Bounded production object transfers](553-bounded-production-object-transfers.md)
- [ADR-554: Owned key bindings and native S3 encryption](554-owned-key-bindings-and-native-s3-encryption.md)
- [ADR-555: Durable object encryption journals](555-durable-object-encryption-journals.md)
- [ADR-556: Customer S3 encryption and owned response metadata](556-customer-s3-encryption.md)
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
- [ADR-480: Platform paths reserved on platform hosts only](480-platform-paths-reserved-on-platform-hosts.md)
- [ADR-493: Observed customer exposure for route changes](493-route-customer-exposure.md) — bounded, read-only request-time customer usage evidence in preview reports
- [ADR-494: Advisory customer route health](494-advisory-customer-route-health.md) — compare tenant or consumer route health while preserving sparse and attribution coverage
- [ADR-495: Advisory watched response codes](495-advisory-route-client-errors.md) — detect selected 4xx regressions without changing rollout decisions
- [ADR-496: Route regression investigation](496-route-regression-investigation.md) — connect route findings to bounded, scoped request examples
- [ADR-497: Route latency investigation](497-route-latency-investigation.md) — add dependency and execution evidence to route latency findings
- [ADR-498: Advisory production route budgets and saved incidents](498-production-route-monitoring.md) — continuously evaluate serving-route budgets and retain bounded incidents
- [ADR-499: Customer-cohort production route monitoring](499-customer-cohort-production-route-monitoring.md) — attribute incidents to request-time tenant or consumer cohorts with bounded recovery tracking
- [ADR-576: Private TCP addressing between services](576-private-tcp-service-addressing.md)
- [ADR-593: Static Go net/http route impact](593-go-nethttp-route-impact.md) — map Go ServeMux source changes to route-level review evidence
- [ADR-594: Static Go Chi route impact](594-go-chi-route-impact.md) — map literal Chi routes, groups, mounts, and middleware to route-level review evidence

## Customer operation decisions

- [ADR-521: customer operations above execution ledgers](521-customer-operations.md) — typed application contracts, customer ownership, separate business and delivery outcomes, and controlled recovery

- [ADR-571: S3 write proof custody and owned cleanup](571-s3-write-proof-custody-and-owned-cleanup.md) — retain pending key evidence and coordinate protected version/account cleanup.

## Events and delivery

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
