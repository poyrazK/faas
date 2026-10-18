# ADR-20261018000200: Scoped PostgreSQL disconnect and recovery rehearsals

Status: Accepted; native execution pending, admission disabled by default

## Context

A worker can duplicate a notification when its database connection fails after
a commit. Simulated HTTP errors do not exercise database sessions or reconnects.

## Decision

Add `managed_work.postgres_fault`, binding a worker, named checkpoint, unavailable
interval and recovery budget into the acknowledged plan. Support one warm job,
two participants (primary and async worker), and one isolated fixture database.
Both participants need PostgreSQL permission. Exclude replacement workers,
cold start, suspend/resume and worker termination. Format bounds are 100–30,000 ms
unavailable and 1–120,000 ms recovery; existing account quotas remain applicable.

The first dispatch's contained checkpoint atomically records a recovery
obligation with its original recorder request, running VM, deployment, job and
ready fixture identity. The gateway holds its response until apid confirms the
physical cut. The apid-owned fixture service sets `ALLOW_CONNECTIONS false` on
only the generated database, terminates its sessions and joins their closure.
The administrative database and observer remain available. Database advisory
locks serialize cut, restore, collection and drop across owners. Recheck the
original cutting authority and live resource under that physical lock, so a
delayed old owner cannot disconnect after a replacement restored access. Committed data
survives; open transactions roll back. This models a database-specific connection
fence, not a server crash, packet fault, provider failure or production outage.

Persist phases `requested`, `cutting`, `active`, `recovering`, `recovered`,
`failed`. After the minimum unavailable interval, restore without changing
passwords. Independently confirm the catalog flag, a fresh connection, and login
and CONNECT privileges for primary/participant roles. The probe uses the fixture
administrator; the native journey separately tests guest credentials and driver
recovery. `outage_ms` measures the confirmed active interval before restoration
starts. `recovery_ms` covers scheduling delay after the planned restore time
through the fresh connection and privilege probe.

Cleanup runs independently of enrollment flags. Shutdown attempts bounded
recovery. A new owner may restore an expired obligation on the same backend but
cannot adopt passing proof. Lost acknowledgements, interrupted checkpoints,
owner loss and cleanup failures retain failure. Delete restores before teardown;
verified drop can retire an obligation without producing success. Retain journals
until fixture cleanup. Down migration refuses retained enrollment or fault rows.

Seal the receipt inside managed-work evidence. Validate exact run, plan,
resource, first dispatch, recorder and checkpoint identities; retries retain the
original worker. Compare real retry outcomes, SQL, object mutations and simulated
actions. A dependency group records recovery or a budget violation. Ignore timing
jitter and generated IDs in equality. Missing proof is inconclusive; confirmed
recovery over budget fails even if both revisions exceed the budget.

New enrollment additionally requires default-off
`FAAS_REHEARSAL_POSTGRES_FAULTS=1` on apid and the preceding rehearsal gates.
Deploy migration, control plane and gateway before qualification/enrollment.
Old peers cannot acknowledge the changed managed-work digest. No new VM owner,
direct apid-to-vmmd call, external transport or promotion of fixture data is added.

## Validation

Portable tests cover both state stores and actual PostgreSQL through the private
proxy: existing-session closure, reconnect denial, same-credential recovery,
another database's isolation, open-transaction rollback, preserved commits,
duplicate notifications, missing proof, worker replacement, acknowledgement
loss, owner restart, interrupted checkpoints, shutdown and cleanup failures.
Full CI runs these fault cases with a separate disposable PostgreSQL service;
the platform metadata service never receives the connection fence.

The native coordinator adds `--journey postgres-recovery`. Four pinned runs must
prove a passing control and only a duplicate-notification regression. The worker
must observe failed SQL during the cut, survive idle-pool errors and reconnect
on its real retry. Verify command receipts, resources, cleanup, app deletion,
installed-release identity and leakchecks. Native execution remains pending,
including the preceding native packet/credential denial, cancellation, restart,
cleanup-failure and expiry matrices. This is not full production rehearsal.
