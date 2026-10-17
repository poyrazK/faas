# ADR-1657: Default-off GitHub Checks for internal CI executions

- Status: implemented behind default-off gates
- Date: 2026-10-16
- Extends: ADR-1572, ADR-1654 and ADR-1656

## Decision

Publish one GitHub Check for a fresh, explicitly opted-in Jobs execution,
whether admitted manually, through a saved profile or by push automation.
`FAAS_CI_EXECUTION_CHECKS_ENABLED=1` and `FAAS_CI_GITHUB_CHECKS_ENABLED=1`
must both be set in apid at fresh admission to create delivery intent.
githubd requires both flags, internal/GitHub enablement and the account
allowlist to publish. Generated apid and githubd units keep the new flag at `0`.
This adds no public compute capability, deployment or flag enablement.

Blocked metadata receipts keep their existing action-required publisher.
Qualification executions never publish execution results. Old executions do
not acquire Check intent after flag changes, repeated admission or restart.
Clients cannot request publication through JSON fields. Capture failure rolls
back execution creation and automatic-event acknowledgement in the same apid
transaction. The existing 32 retained Jobs executions/account bound also caps
the number of delivery records; there is no additional unbounded queue.

## Durable latest-state delivery

`ci_execution_checks` stores delivery status, claim token/lease, retry budget,
GitHub Check ID, last delivered execution version and projection digest. The
existing atomic `ci_executions.version` is desired-state authority. githubd
joins it to find dirty records and reads the latest committed execution after
locking delivery bookkeeping. It never writes execution state or Jobs intent.
Intermediate versions can coalesce; publication may move directly from queued
to completed for a fast execution. Lease-only version changes are acknowledged
without redundant GitHub writes when the bounded projection is unchanged.

Claim acquisition commits before network work. Delivery then locks only its own
row across the bounded provider calls, rechecking token and unexpired lease.
Other publishers and expiry queries use `SKIP LOCKED`, even after a live
publisher's lease expires. Execution writers never acquire the delivery lock,
so cancellation and native cleanup do not wait for GitHub. A state change
during publication leaves a newer execution version dirty for the next pass.
An expired publisher cannot acknowledge or release a newer claim.

Use PostgreSQL time, 60-second leases, 15-second combined verification/publication
budgets, eight consecutive failed or abandoned attempts, capped persisted
backoff and the existing bounded worker batch. A successful delivery resets
the retry budget. Authority loss permanently ignores delivery; exhaustion is
dead. Database/provider details are excluded from persisted reasons and worker
logs. No automatic redrive or pruning is introduced. Rollback refuses to drop
any nonempty delivery journal because it retains remote Check identities.

## GitHub ownership and result contract

githubd rechecks active account authority, installation ownership and exact
repository ID before each changed projection. The admitted binding snapshot
provides source identity. Disabling a profile, automation policy or binding
stops future intent but does not suppress an admitted execution's final Check;
current installation/repository access remains mandatory. Turning off Check
publication or removing the account pauses its delivery journal. Re-enabling
reconciles only records already captured by fresh opt-in admission.

Check names use the pipeline and binding UUID. The stable external identity is
`gregale-ci-execution:<execution UUID>`, separate from blocked receipts and
deployment Checks. Missing create acknowledgements are recovered by bounded
discovery using name, external ID and commit SHA. Before PATCH, read the exact
remote Check and reject mismatched identity, an observed newer version or an
attempt to replace a completed result with progress. The output title carries
the delivered execution version. Missing/malformed remote version metadata
fails closed. No remote Check ID supplied by a client is accepted.

GitHub offers neither conditional Check updates nor an idempotent create key.
Local serialization, identity discovery and remote version checks reduce stale
and duplicate writes; they cannot guarantee exactly-once creation or prevent a
GitHub request from completing late after process/network loss. This is not a
native qualification result. Real App API behavior remains a verification item.

| Execution | GitHub status | Conclusion |
|---|---|---|
| queued | queued | none |
| running / cancelling | in_progress | none |
| succeeded | completed | success |
| failed | completed | failure |
| failed with a timed-out attempt | completed | timed_out |
| cancelled | completed | cancelled |

Terminal results come only from the existing execution state machine, which
waits for native stop and verified output. Summaries contain execution UUID,
execution/job states and completed/total step counts, bounded to 8 KiB. They
exclude commands, step names, raw logs, artifact paths, download links,
credentials and provider errors. This is an allowlisted projection, not a
redaction pass over repository-controlled output.

## Verification

Portable PostgreSQL tests exercise lifecycle outcomes, policy/profile disable,
fresh-admission gates, no backfill, claim fencing, retry exhaustion, admission
atomicity, rollback protection, cancellation during delivery and expired live
publishers. Fake GitHub HTTP covers create/update response loss, remote identity
and version guards, terminal payloads and signed-webhook admission through
restart and final-result delivery. No real GitHub publication or native compute
qualification runs in the cloud environment.
