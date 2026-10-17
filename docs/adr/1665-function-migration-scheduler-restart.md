# ADR-1665 · Reconstruct measured function handoffs at scheduler startup

- **Status:** implementation for qualification; native execution pending; production off
- **Date:** 2026-10-17
- **Decision:** Before ledger seeding and admission, the destination schedd reads
  its durable ready and committed ADR-1664 handoffs and completes their existing
  ownership protocol. It does not resume an issued restore or reattach a guest.
- **Why:** A scheduler process can disappear after native readiness or after
  ownership commits. Its replacement must not depend on the departed process's
  AppSpec, wake allocation, client response or memory ledger.

The startup scan uses keyset pagination by instance UUID, scoped to the local
destination node and attempts that retain their source cleanup lease. Successful
cleanup may remove rows between pages. The cursor advances past failed entries;
the overall time budget can still defer later work. Issued destinations and expired uncommitted leases retain the
existing fenced expiry/rollback path.

For a ready destination, read the source's persisted runtime configuration
receipt and publish the exact recorded destination wake, receipt and network
identity through the existing ownership transaction. That transaction rechecks
the source ownership and lease, current configuration, grant expiry, policy,
artifact approval, target incarnation and measured readiness. No current-value
rebuild, fresh wake or new restore permission substitutes for the recorded
attempt. A commit error is resolved against the exact durable epoch. An aborted
or unresolved decision cannot authorize activation or source retirement.

For an already committed destination, never rewrite ownership. Check current
committed authority, repeat only the separate idempotent activation RPC, then
acknowledge the original lease-bound source. Activation executes no application
callback. Failed activation/retirement retains the durable cleanup record for
the existing expiry reconciler. Failed startup recovery also withholds optional
environment qualification dispatch; it does not erase capacity claims.
The startup pass is bounded by the existing commit-recovery timeout; remaining
attempts retain the same durable claims for expiry reconciliation.

Concurrent original coordinators and startup recovery serialize through the
same ownership CAS. Identical activation retries are safe. The destination's
durable capacity reservation remains independent of process-local ledgers until
retirement or replacement by the committed instance. Startup seeds its ledger
after this recovery pass.

The native fixture adds process-death cases on both sides of ownership commit.
It kills only its own migration coordinator child with SIGKILL and starts a new
child running the production startup recovery and ledger-seeding methods with a
new PostgreSQL connection and VM RPC clients. Reports require distinct observed
process identities and the same executable hash, actual signal termination,
unchanged destination/SQL identities, no additional lifecycle callbacks and
confirmed source and final physical cleanup. Prepared host services remain
running. These cases exercise coordinator process loss; they do not restart the
systemd schedd service, kill vmmd, emulate PostgreSQL failover or qualify HA.

Native execution of the prior eight-case baseline and these four restart cases
remains required on designated physical x86_64 KVM hosts. Portable tests and
native compilation supply no native acceptance. Unknown VM ownership after a
vmmd restart stays quarantined under ADR-1664. Production capabilities, complete
failure-matrix qualification and release activation remain off.

Local validation and its limits are recorded in the
[implementation evidence](../ops/evidence/20261017-function-migration-scheduler-restart/REPORT.md).
