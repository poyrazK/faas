# ADR-20261105130932: Preserve PostgreSQL state on rejected work

- **Status:** accepted
- **Date:** 2026-11-05
- **Amends:** [ADR-1027](1027-native-managed-postgres-on-firecracker-database-nodes.md) and [ADR-20261105123632](20261105123632-managed-postgres-pooling-and-upload-isolation.md)

Three failure paths changed state before work was accepted: a failed protocol
batch retained skipped named-statement changes, a rejected resize discarded its
healthy standby, and parallel archive streams each spent the same free space.

The transaction pool now tracks tentative Parse and Close changes for every
client statement name. An extended-protocol error rolls skipped changes back
in reverse order. A failed unnamed Parse destroys the earlier unnamed statement;
a failed named Parse preserves the earlier named statement. Backend preparation
markers for skipped Parses are still cleared so a later borrow can prepare the
statement the client actually owns. Simple-query invalidation remains limited
to the unnamed statement.

Leaving the production class now has an admission preflight before any VM
stops. Invalid shapes, unavailable primary nodes and insufficient capacity are
rejected while the original primary and standby keep running. A clean park then
preserves the old synchronized standby and its volume while the store performs
its final atomic resize admission. Only an admitted shape may discard the old
standby volume. If final admission fails, the scheduler attempts to wake the
unchanged database with its trusted standby; a failure to resume is returned
alongside the admission error. Cleanup after a committed downgrade uses the
existing orphan recovery path. Node capacity checks and the storage ledger
remain owned by the stores; no new reservation rows or catalog fields are added.

The archive server now shares a per-database budget across active write streams.
WAL reserves its declared size before receiving the payload. Quota-aware local
backup writers reserve actual compressed output and manifest bytes before
writing them. These reservations supplement committed LocalBytes, which omits
temporary files. The quota check and reservation are atomic for that database.
Requests release reservations on commit, rejection or cancellation, and unused
tracking entries are removed. Databases have separate budget locks.

Backups and WAL do not share a lock for the duration of their streams:
pg_basebackup can need WAL archiving to finish. The existing incoming backup
stream size bound remains, while output reservations prevent concurrent writes
from exceeding the disk cap. Capped backup streams require the local archive's
quota-aware writer; unsupported backup implementations fail closed. Existing
uncapped stores keep their prior behavior.

RPCs, SQL schema, journal records and completed archive formats are unchanged.
Protection requires upgrading the affected proxy, scheduler and backup daemon;
older processes retain their previous admission behavior.

Validation includes real PostgreSQL pipeline recovery and resize-store checks,
rejected and admitted class changes, a late admission rejection with service
restoration, concurrent Unix-socket archive uploads, cancellation cleanup,
manifest accounting and WAL progress during a pending base backup. Hardware
acceptance still requires a native KVM host.
