# ADR-1652: Disabled CI HTTPS transport, private storage and retention

- Status: implemented for portable qualification; native and real GCS acceptance pending
- Date: 2026-10-15
- Extends: ADR-1573, ADR-1574 and ADR-1651

## Decision

Mount the scoped guest transport and authenticated artifact downloads in apid,
behind `FAAS_CI_TRANSPORT_ENABLED=0`. Add a dedicated private GCS object driver
and PostgreSQL reservation/recovery journal. A separately disabled
`FAAS_CI_RETENTION_ENABLED=0` starts bounded retirement work. Both flags accept
only the exact value `1`. Generated units keep all CI flags off, the account
allowlist empty, and both the runtime origin and bucket unset.

When both new flags are off, apid constructs no storage client and performs no
bucket discovery or provider I/O. An enabled storage component requires the
PostgreSQL CI store and an existing qualified bucket. No bucket, IAM policy,
credential or deployment is created by this change. Enabling retention alone
can drain existing data with transport and admission disabled.

The CI execution admission and reconciliation worker remain unwired. Metadata
and GitHub receipts stay blocked, and capabilities continue reporting compute
and dispatch unavailable. schedd and runtime grant issuance additionally require
the transport flag before launching an explicitly admitted CI-owned Jobs task.

## HTTPS and download authority

Transport configuration requires a credential-free HTTPS origin. Requests must
match its host, and either use TLS directly or arrive from loopback with exactly
one `X-Forwarded-Proto: https` value from the local deployment TLS terminator.
Forwarding headers from remote peers cannot authorize plaintext access. All
responses are marked `no-store`.

Guest routes retain the attempt/instance capability protocol from ADR-1574;
account credentials, cookies and query parameters are refused. There is no HTTP
grant issuance route. Source access still requires current native running
identity and source policy, and cancellation/VM stop revoke authority according
to the existing finalization window.

Artifact downloads require an authenticated active account, internal allowlist,
admin scope and the existing MFA gate. The execution must be terminal, and the
artifact reference must come from the frozen, verified Jobs output observation.
A guest seal or object name alone cannot authorize a download. apid holds the
storage read lease while it verifies bytes in a bounded private spool, rechecks
ownership and expiry, and writes the attachment. No signed or public object URL
is exposed.

## Immutable objects and bounded accounting

Every logical source/log/artifact key has an immutable descriptor and random
physical object ID. The journal reserves its size before provider I/O, after
validating the owning execution/attempt and either current source policy or an
issued output reservation. Only one source object can be reserved per attempt.
Source retry recovery also checks the pinned repository, commit and binding;
after a lost publish acknowledgement it verifies the existing bytes and commits
their receipt without fetching another GitHub archive.

A short global PostgreSQL lock serializes quota reservations and operation
leases. Blob I/O never runs inside that transaction. Pending, ready and deleting
objects all remain charged. A timeout, failed provider operation or lost
acknowledgement cannot release quota. Token-fenced leases reject stale commits
and releases after controller takeover.

Bounds live in `pkg/api/limits.go`:

| Resource | Account | Global |
|---|---:|---:|
| Charged payload bytes | 2 GiB | 8 GiB |
| Charged objects | 1,024 | 8,192 |
| Retained journal rows, including retired objects | 16,384 | 131,072 |
| Concurrent physical I/O operations | 2 | 8 |

Operations use 30-second database leases and 15-second I/O deadlines. Existing
32 MiB source/artifact, 64 KiB log and per-job output bounds still apply. The
journal deliberately has no account/execution foreign key: deleting intent
must not delete the authority needed to retire its remote payloads.

## Retirement and delayed-writer fencing

The worker considers at most 16 eligible objects per 60-second sweep. Ready
objects expire seven days after publication; unpublished reservations become
orphan candidates after two hours. Active executions and controller leases keep
their recovery data even past expiry. Existing attempts require frozen output
and terminal native task/confirmed stopped instance before retirement. If intent
has disappeared, any non-stopped instance of the owning Job blocks cleanup.
Unknown native evidence is retained conservatively. Active read/write leases
also block retirement. Authenticated downloads refuse expired objects even when
recovery still needs them.

GCS writes use create-only generation preconditions. Retirement replaces payload
bytes with a permanent empty tombstone under a generation precondition, creating
the tombstone even if no payload is visible yet. A delayed writer cannot recreate
data after a retirement acknowledgement and quota release. Retirement response
loss leaves the reservation charged; repeating the operation confirms the same
tombstone. Runtime grants are retired only after stop/output reconciliation,
transfer lease expiry and retirement of every managed object for that attempt.

This requires a dedicated bucket with public access prevention enforced, uniform
bucket-level access, no versioning, no soft delete, no retention policy and no
lifecycle rules. Otherwise replaced versions could retain unaccounted payloads,
or lifecycle deletion could remove the delayed-writer fence. apid checks these
properties at startup and before publication/retirement. Operators must preserve
them and restrict writes to the managed service identity. Existing deployment
ADC supplies access; the service does not change IAM or bucket policies.

Journal rows and physical empty tombstones remain bounded by the journal quota.
No automatic journal pruning or execution-metadata pruning is implemented. The
existing 32 retained executions/account ceiling remains. Exhaustion requires
operator reconciliation; this is a finite internal qualification service, with
no public retention promise. Migration rollback refuses any retained journal
row, including retired ones, until recovery authority is explicitly reconciled.

## Evidence and remaining gates

`make test-ci-storage` uses production migrations, PostgreSQL transactions,
fake physical storage and the actual GCS SDK against a simulated HTTP endpoint.
It exercises account/global quotas, lease takeover, source acknowledgement
recovery, read/stop/active-execution cleanup barriers, orphan intent deletion,
retirement acknowledgement loss, conditional generation writes and delayed
writers. API race tests exercise mounted HTTPS routes, guest credentials,
default-off behavior, verified terminal downloads, admin scope and MFA. Runtime
and scheduler regressions continue using synthetic native receipts and fake step
executors. No repository command runs on the host.

Real GCS policy/IAM, resumable upload and failure/restart acceptance remain
pending: this environment has no configured cloud credentials. Native two-job,
cancellation, timeout, restart, pressure, tenant-wake, metering and image-lifetime
acceptance also remain pending. `/dev/kvm` is absent, and the earlier local
`leakcheck` could not inspect sysfs loop entries without `/dev/loop*` devices.
Run `test-metal` and `leakcheck` on the dedicated native x86_64 acceptance host.
No native or real-provider acceptance is claimed, no image is published, and no
flag or deployment is enabled here.
