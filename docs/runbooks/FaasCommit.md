# Gregale Commit delivery recovery

These alerts cover the qualification-gated PostgreSQL outbox relay. They do not
establish customer production qualification. Delivery remains at least once.

## Inspect the source and event

```sh
gregale commit doctor <source-id>
gregale commit info <source-id>
gregale commit blocked <source-id>
gregale commit inspect <source-id> <event-id>
gregale commit wait <source-id> <event-id> --until accepted --timeout 2m
```

`doctor` exits 1 for a failed or unknown check. With `--file /private/database-url`,
it additionally checks TLS connectivity, schema, binding and relay-role grants
from the machine running the CLI. The file is never uploaded or printed. These
local checks do not prove scheduler network access. Provision the matching CA
through `PGSSLROOTCERT`. The doctor never installs DDL or claims events.

A missing receipt means Gregale acceptance has not been established; it does
not mean that the business transaction rolled back. A blocked snapshot proves
that the relay previously observed a committed row, not that it remains blocked
now. Retained acceptance and completion facts take precedence over snapshots.
Wait timeout or interruption does not cancel work.

## Alert actions

- **FaasCommitBacklogOverdue:** a fresh known observation contains a pending row
  inserted more than fifteen minutes ago, sustained for five minutes. Check
  destination readiness and Operations capacity before requesting replay. The
  age uses row insertion time, not transaction commit time.
- **FaasCommitBlockedEvents:** inspect the bounded event snapshot and its reason
  codes. Correct only an unaccepted row, then run `gregale commit replay
  <source-id> <event-id>`. Preserve the ID. Do not clear accepted identities.
- **FaasCommitSourceObservationUnknown:** a source is unconfigured, unreachable,
  or has no complete observation within five minutes. Verify both Commit flags,
  scheduler age identities, approved hostnames/CIDRs, PostgreSQL CA, schema and
  owner binding. Credential rotation and resume intentionally invalidate earlier
  health until a fresh pass. Unknown backlog counts do not mean zero events.
- **FaasCommitRelaySourceFailure:** follow the source's bounded relay health code.
  Recover credentials, database access, schema, binding, replay or cleanup as
  appropriate. Preserve pending and blocked rows throughout recovery.
- **FaasCommitObservationUnavailable:** the platform health-ledger query failed.
  Check platform PostgreSQL and scheduler metrics before trusting backlog totals.
  Existing daemon-down alerts cover missing scrapes; this alert covers successful
  scrapes whose observation query failed.

The scheduler exposes aggregate source gauges without customer IDs or payloads.
Each scheduler reads the same ledger; use `max`, not `sum`, across schedulers.
Fresh known source totals are partial when `unknown_sources` is nonzero. Paused
and deleted sources are excluded. A failed snapshot emits only success=0,
rather than retaining misleading backlog gauges. Receipt identities are not
pruned by source-row cleanup. Production receipt-storage qualification remains
required.
