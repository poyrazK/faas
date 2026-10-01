# Managed exclusive operations implementation

Working branch: `codex/exclusive-operations-01a0f345`.
Contract: [ADR-387](../adr/387-managed-exclusive-operations.md).

The feature contract and database acceptance checklist:

- [x] Account-owned policies with explicit app membership and environment boundaries.
- [x] Trusted account/customer scope, persisted separately from the business key.
- [x] Separate queue, reject, join-existing, and request-idempotency behavior.
- [x] Durable monotonic lane generations and token-bound ownership.
- [x] Atomic claim, renewal, expiry, replacement, retry, completion, effect commit,
      cancellation, and reservation recovery in PostgreSQL and MemStore.
- [x] Manual invocation, cron, inbound webhook, queue/inbox, and broker triggers
      share the same ownership boundary.
- [x] Manual Job runs, recurring Job schedules, and deployment-attached AppTasks share the managed ownership boundary and carry generation-fenced results.
- [x] Host renewal is bounded by the attempt deadline and ownership loss cancels delivery.
- [x] VM incarnation binding, parking guard, and authority invalidation on restore are enforced in the platform store.
- [x] Authenticated internal delivery rejects forged ownership metadata.
- [x] API, CLI, generated Go SDK/OpenAPI, inspection, quotas, audit, and bounded metrics.
- [x] YAML/TOML policy and trigger-binding declarations, explicit CLI reconciliation, and a customer example.
- [x] Recovery/operator documentation and capability registry evidence.
- [x] PostgreSQL contention and integration acceptance run without skipped database tests.

Host-specific release validation remains:

- [ ] Native KVM stale-snapshot acceptance, metal checks, and leakcheck.

Progress note: manual app, Job, and AppTask submissions, HTTP cron/fire-now,
recurring Job schedules, signature-verified Stripe webhooks, and in-platform
queue plus external broker triggers share the operation admission and worker
path. Job schedule occurrences use stable retry identity before the schedule
cursor advances. Job and AppTask rows carry an operation generation, and
platform result commits reject stale generations. PostgreSQL 16-backed
acceptance and focused scheduler integration tests ran with database tests
enabled, including the snapshot/restore fencing case. A Linux real-daemon E2E
also passed under `-race`, covering the API, PostgreSQL, schedd, gatewayd, and
VMMD delivery path. The full `pkg/sched` suite currently has a separate failure in
`TestWakeBurstSpreadsSnapshotRestoresAcrossPgEngines`: its fixture's first
wake returns `no live deployment to wake`, including when run alone. YAML/TOML
declarations reconcile account-owned policies and trigger bindings through
the CLI. Native KVM stale-snapshot acceptance, metal checks, and leakcheck
remain open because this host has no `/dev/kvm`.
