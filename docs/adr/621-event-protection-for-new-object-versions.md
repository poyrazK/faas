# ADR-621: Event protection for new object versions

Status: Accepted
Date: 2026-10-05
Amends: ADR-618 and ADR-620

## Context

Per-version event mutations are durable, but creation receipts still reject event
holds and event defaults. A successful write does not prove that its new version
has the admitted variable retention policy. Re-reading changing defaults during
recovery could settle a version against a different policy.

## Decision

Extend the existing immutable write and multipart snapshots to retain event
policies. Explicit ON requires exactly one positive days or years duration and
allows a minimum date. Explicit OFF at creation requires a fixed date and omits
duration; an undated OFF remains an existing-version release in ADR-620.
Omitted retention inherits the captured bucket default, including its event
period and any fixed minimum. An explicit retention selection overrides that
default for the created version. Independent legal holds remain unchanged.

Check the separate backend event-hold enrollment at ingress and again under
admission's account/bucket locks. The admission-only capability is excluded from
the persisted policy and proof. It cannot authorize recovery or change accepted
snapshots. Advertise `write_event_hold` alongside the existing capabilities.
Accepted branded URLs, multipart sessions, route writes and recovery retain their
original policy after enrollment or ingress is disabled.

The snapshot's conservative retention bound is the later of its explicit/fixed
minimum and admission plus the event duration. Event years normalize to 365 days.
S3 computes the moving native date; Gregale never dispatches the calculated bound
as a replacement for the requested policy. Defaults remain native defaults, and
bucket configuration changes continue to drain accepted writes and sessions.

Send standard signed event status/duration headers on PUT, CopyObject and
CreateMultipartUpload. Multipart parts and completion cannot replace initiation
policy. Owned signed-upload and multipart APIs and Go/Node/Python clients share
the existing typed retention fields. Upload routes inherit captured defaults.

Settlement and current/retained-history recovery require exact native version,
receipt/session, size, ETag, private policy proof, requested mode, event status,
equivalent duration and a date preserving the snapshot's minimum. Missing,
duplicate, unknown, conflicting or malformed protection headers retain custody
and quota. Explicit fixed policies cannot settle from an unexpected active event
hold. Acknowledgments alone do not settle protected writes; recovery sends no
body or replacement protection policy.

New independent SQL validators and constraints accept typed event snapshots and
bound URL selections while retaining the existing immutable ownership, aware
dispatch and verified-settlement triggers. Replaying fixed-write validators
cannot weaken the event constraints. A historical URL constraint replay may
reject existing event URL history; it fails closed and must be followed by the
current migration. Rollback refuses event snapshot or URL history, including
terminal receipts. No old migration is edited.

## Limits

Existing snapshot, lease, transfer, checksum, history and request-metering bounds
remain in effect. Date overflow is rejected. Deployment enrollment stays disabled
by default. Governance bypass and replication remain outside this amendment.
Local native HTTP/TLS fixtures qualify the contract without live providers.

## References

- [S3 variable retention and default behavior](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock.html)
- [PUT event headers](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)
- [Exact-version HEAD protection](https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html)
