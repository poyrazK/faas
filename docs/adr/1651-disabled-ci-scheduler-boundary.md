# ADR-1651: Disabled CI scheduler boundary and incomplete-output recovery

- Status: implemented for portable qualification; native acceptance pending
- Date: 2026-10-15
- Extends: ADR-1573 and ADR-1574

## Decision

Connect CI-owned ordinary Jobs runs to the scheduler's existing held-start
protocol. Keep all CI gates off and `FAAS_CI_RUNTIME_URL` empty in generated
schedd units. The integration does not add CI execution admission, an execution
engine dispatcher, mounted runtime HTTP routes or a storage deployment. Metadata
and webhook receipts remain blocked and cannot be promoted into executions.

schedd alone owns instances and native capacity. vmmd and guest PID1 retain
their existing Jobs protocol. Every CI launch uses ordinary Jobs placement,
account concurrency, metering, leases, cancellation and cleanup.

## Launch authority

PostgreSQL CI stores are detected by the engine and default to refusing CI-owned
launches when explicit configuration is absent. `cmd/schedd` supplies the same
internal, GitHub, dispatch and account gates as apid. Every owned launch requires
all gates, an active paid account, unchanged source binding and template,
materialized pinned image, one task at attempt one, zero retries, the exact
manifest command, empty environment, fixed RAM and an unexpired CI deadline.
PostgreSQL timestamp comparisons account for its microsecond precision.

These checks happen before capacity and native lease acquisition. After the
atomic native instance/task claim, grant issuance rechecks policy and binds the
capability to the exact current native lease and instance. The scheduler removes
customer `GREGALE_CI_*` variables from every Job before injecting the CI-only
runtime origin, instance ID and capability. The origin must be credential-free
HTTPS. Runtime credentials are absent from Jobs intent and the CI command.

A CI VM must acknowledge held start. Source reads require a `running` native
instance, so a staged grant cannot fetch source before schedd verifies the held
acknowledgement and publishes runtime identity. After that publication, schedd
rechecks policy and grant authority before releasing the command. A binding
revocation, account downgrade/hold, template change, cancellation or deadline
before release prevents execution. The guest continues enforcing its absolute
deadline and transfer policy after release. Its native timeout is also capped
by the remaining CI deadline, including queue delay.

A lost release acknowledgement is ambiguous: retain the claimed task, supervise
its native receipt and never issue another VM. Scheduler restart cannot reclaim
an already claimed task as fresh work. The normal expired-lease and orphan
reconciliation paths remain responsible for native recovery.

## Failure and cleanup

CI boot rollback records a fenced task failure with retry budget zero and
destroys the bound instance through vmmd. Failed destruction preserves the live
instance state and capacity reservation until orphan reconciliation succeeds.
PostgreSQL boot-failure and lease-reaper transitions independently clamp CI-owned
retry budgets to zero, even if a caller supplies a larger budget. Ordinary Jobs
replay and task retry remain forbidden for owned runs.

For `SealedJobsOutput`, a stopped native task that never sealed its guest result
now produces a durable **platform failure**. A native cancellation remains
cancelled; a native process success with no seal becomes CI failure. The receipt
marks every step skipped, includes a bounded diagnostic saying output is
incomplete and certifies no artifact manifest. Partial objects and receipts stay
retained for operator reconciliation. It never trusts an uploaded `result.json`.
The platform result is frozen in the attempt journal before the engine sees it,
so restart, output paging and late uploads cannot change it.

This refines ADR-1574's missing-publication behavior. Missing stop confirmation
still waits. Corrupt sealed output still waits for repair rather than becoming a
successful result. A stopped task without a seal can no longer wait forever for
an upload whose native authority has already been revoked.

## Validation and activation boundary

Portable tests use full production PostgreSQL migrations and fake native
boot/release/destroy receipts. No repository command executes on a host or in a
host container. Coverage includes default-off/missing configuration, pinned
snapshot drift, account and binding revocation, cold-boot source refusal,
held-start publication order, boot/cancellation races, lost release ACK,
scheduler/controller restart, failed destroy and later reconciliation,
no-retry store transitions and stopped-without-seal recovery.

`make test-ci-runtime` and `make test-ci-scheduler` exercise these contracts.
The managed development environment has no `/dev/kvm`; it supplies no native
execution, metering or cleanup qualification. The repository's `CLAUDE.md`
requires `test-metal` and `leakcheck` on a dedicated native x86_64 KVM host for
lifecycle qualification. Native acceptance must use the pinned runner image,
mounted HTTPS transport and real two-job pipeline, then repeat failure,
timeout, cancellation, pressure and restart cases with tenant wakes and billing
evidence. Container leak checks alone do not satisfy that requirement.
The local `leakcheck` attempt fails closed because sysfs loop entries have no
corresponding `/dev/loop*` devices in this container, so their state cannot be
inspected. No native leak gate is recorded as passing.

Before activation, finish route/storage wiring, qualified image materialization,
retained storage quotas and cleanup, authenticated artifact downloads and image
lifetime through recovery. An explicit CI admission/dispatcher integration is
still required. No flag is enabled and no deployment is performed here.
