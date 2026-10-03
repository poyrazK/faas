# ADR-435 · Versioned inherited application standards

- **Status:** implementation in progress; acceptance required before release
- **Date:** 2026-09-30
- **Decision:** Add organization-owned immutable application-standard versions,
  scope assignments, automatic application enrollment, effective configuration
  with field provenance, bounded approved exceptions, and durable reviewed
  rollouts. apid owns intent; existing gateways, imaged, schedd and vmmd retain
  their enforcement and lifecycle ownership.
- **Why:** Per-app controls otherwise require copied configuration and manual
  enrollment. Company logging and security requirements should follow service
  ownership, survive every deployment entry point, and update through a visible,
  recoverable process.

This decision originally used ADR-387 on the implementation branch. It was
renumbered after upstream assigned that number to FOCUS invoice projection.
Already committed migration comments retain their original citation; the
migration files remain immutable.

It subsequently used ADR-393 on this branch. Latest-main integration assigned
ADR-393 to managed exclusive operations, and this decision moved to ADR-429.
Historical migration comments retain the numbers originally issued; no applied
or committed migration contents are changed during renumbering.

Upstream then assigned ADR-429 to handled internal service request evidence.
This decision moved to ADR-430. Frozen SQL migration bytes and historical
migration citations remain unchanged.

Upstream assigned ADR-430 to the managed PostgreSQL Commit outbox. This decision
moved to ADR-431; frozen SQL files and their historical citations remain unchanged.

Upstream assigned ADR-431 to bounded gateway trace retention. This decision now
uses ADR-435; frozen SQL files and their historical citations remain unchanged.

## Contract

Standards contain an explicit supported vocabulary: logging destinations,
signature requirements, security posture, trusted publishers, outbound CIDRs
and extra outbound ports. Each field declares `default`, `mandatory`, or
`restricted` mode. Defaults yield to permitted local intent; mandatory values
cannot be weakened; restricted sets allow a subset. Logging can explicitly
permit additional destinations. More-specific assignments cannot weaken an
ancestor's mandatory constraint. Conflicting requirements fail with field-level
provenance. Unsupported controls and unknown JSON fields fail validation.

Versions are immutable and hash canonical definitions. Assignments select an
explicit admission version; publishing a version never silently activates it.
Apps inherit through persisted organization/project/app ownership. Scope
selection is not based on caller-controlled labels or the active API-key org.
The shared selectors distinguish admission from existing adoption. New services
use explicit admission versions; existing services resolve only their saved
adoption pins. A retained assignment disabled for admission can still govern an
existing service during its controlled removal. UUID spelling changes between legacy memory fixtures and
PostgreSQL cannot reset an adoption. Selected definitions are checked against
their canonical hashes and owning organization before the resolver receives them.
Creating apps, project reconciliation, GitHub deployment and clones share the
same enrollment boundary. Local intent is retained separately from the effective
projection. Mutations of managed settings pass the shared resolver.

Organization roles receive dedicated read/manage/approve actions. Owners and
admins manage standards; developers and viewers can inspect effective settings.
Exceptions name an app, version, field, replacement, reason, approving identity
and expiry. They cannot bypass platform restrictions or plan entitlements. An
expired exception is ineffective at admission even if repair has not run yet.
Credentials use organization-owned destination references and sealed storage;
definition, diff, audit and rollout responses contain no credential material.

The legacy project account scope must be verified against every participating
app's persisted organization; cross-organization project assignment is denied.
App-wide controls affect all deployments of that app. Production-only named
environment standards require explicit environment-aware enforcement; the API
must reject unsupported scope/control combinations rather than quietly affecting
staging. Application-level production scope remains supported.

App creating-account attribution and deployment app identity are retained.
Organization/project moves reenroll the existing app; cloning for a different
creating account creates a new app. Artifact and control writers participate in
advisory input fences so approval can take a stable cut without adding reverse
parent-row waits to legacy child updates or deletion.

## Updates and recovery

A preview captures target membership, current adoption, effective/local values,
exceptions, artifact evidence and the target standard hash. Approval is bound to
that complete plan. Apply locks and recomputes its inputs; changed inputs return
a stale-plan conflict without writes. Per-app checkpoints and a fenced worker
lease make batch operations resumable. New apps use the assignment's explicit
admission version during a rollout. Rollback previews restoration against current
platform restrictions and other mandatory standards; it is an audited operation.

Logging converges through existing drain delivery, egress through schedd's live
network repair and next-wake configuration, and image security through admission
and artifact verification. Desired, persisted and observed versions are separate.
An operation is complete only when its defined verification gates pass; absent
consumer acknowledgment is pending, never fabricated success. Durable change
records repair missed notifications. Partial convergence is visible and pauses
the next batch. No direct apid-to-vmmd calls and no hot-request inheritance query.

## Registry publisher signature transport

This decision supersedes ADR-058's registry raw-signature transport. The OCI
blob endpoint must serve bytes matching the requested digest; an image
manifest's digest cannot address a separate signature. The implementation uses
the keyed Cosign simple-signing attachment format documented by the
[Cosign signature specification](https://github.com/sigstore/cosign/blob/main/specs/SIGNATURE_SPEC.md)
and the [OCI distribution specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md).
It does not infer trust from certificates or attachment-provided keys.
Attachment limits are 1 MiB per manifest, 64 payloads, 64 KiB per payload,
80 bytes per decoded DER signature and 32 JSON nesting levels, centralized
in `pkg/api/limits.go`. The shared registry JSON manifest reader retains its 8 MiB
limit and now refuse overflow rather than parsing silently truncated bytes.

A single `sha256-<subject-hex>.sig` manifest read supplies bounded payload
layers of type `application/vnd.dev.cosign.simplesigning.v1+json`. Each payload
is fetched by its own descriptor digest and checked against its size and actual
SHA-256. The `dev.cosignproject.cosign/signature` annotation holds a base64
ASN.1 ECDSA P256 signature over SHA-256 of those exact payload bytes. The
case-sensitive critical claim vocabulary must identify a container image
signature and name the exact resolved subject digest. Unknown critical fields,
duplicate members, excessive nesting and trailing JSON refuse. Optional
annotations do not grant authority. This supports explicitly approved P256 keys;
keyless identities, Rekor trust, DSSE and alternate bundle/referrer formats are
not implemented. Signing tooling must emit this supported attachment format.

The immutable signed subject may be an index. Platform resolution verifies the
selected linux/amd64 child descriptor, child content and config before publisher
verification; subsequent executable reads use that child reference. Signature
lookups retain the same repository-scoped registry credentials. A missing
attachment manifest is distinct from an authentication, transport, payload or
format failure. No signature is fetched from the image's own digest blob URL.

Successful verification returns the subject digest, publisher label, canonical
SPKI DER SHA-256 fingerprint, actual attachment manifest digest, payload digest
and signature digest. Labels and PEM spelling are not key identity. Publisher
approval currently constrains keys, not repository names: copying the same
approved digest to another repository does not create different content.
ADR-038's platform ext4 raw digest signer/verifier remains unchanged.

This transport implementation is only one artifact evidence boundary. Durable
proof storage must bind the selected image/config/layers to the converted
rootfs and sidecars, current approved keys, current scan evidence and storage-owned
expiry. Native admission must require those proofs. Live revalidation must use
the retained immutable subject rather than the customer's mutable tag. Source
builds need scoped source/rootfs evidence from an explicitly approved build
publisher; the platform signer is not automatically an approved company key.
Native two-drive consumption, current scan and native-consumer bindings remain
pending, and public standard activation stays disabled.

Registry verification now retains the exact signed payload and DER signature in
private immutable deployment/workload records. The store rechecks cryptography
against the current account-scoped `app_trusted_signers` key under nonwaiting
ownership, control and artifact fences. A stale publisher mirror cannot approve
a rotated or deleted key. Records bind persisted customer intent, organization,
account, app and deployment identity, signed source and selected child; they
receive a storage-owned 24-hour expiry (`ImageSignatureVerificationTTL`). Exact
ID retries preserve the original clock and binding, and changed inputs conflict.
Main and sidecar images share this gate; the full-rootfs fallback consumes the
resolved child rather than resolving the customer tag again. Sidecar compatibility
metadata retains that immutable child reference.

Conversions now retain their exact storage-issued registry verification ID and
input hash. Rootfs builders hash the complete ext4 stream actually sent to
storage, including metadata and unused capacity, and return that digest and byte
count separately from staged content size. Signed main, full-rootfs and sidecar
publication freshly hashes the stored object against that produced identity.
The private store rechecks current ownership, original workload reference,
publisher key, conversion status and storage-clock expiry before atomically
recording immutable producer evidence, selecting it and updating compatibility
metadata. The producer expiry cannot extend its parent verification. Exact ID
retries preserve the original evidence and cannot reactivate an older selection.
Failed transactions publish neither producer selection nor deployment metadata.

Registry resolution now retains the exact signed source/index, selected child
and config bytes in private immutable verification records. Storage recomputes
their SHA-256 and descriptor sizes, independently selects the compatible child,
and validates the ordered layer descriptors and config DiffIDs. Direct manifests
retain a single source buffer. Raw image config may contain image environment
values; preflight JSON excludes retained evidence, and these private records must
not be exposed by public responses or logs. Registry limits are 8 MiB per
manifest, 1 MiB per config, 1,024 layers, 16 GiB per compressed layer and 64 GiB
per uncompressed layer, centralized in `pkg/api/limits.go`. Config readers reject
overflow and trailing JSON rather than accepting a prefix. Converted app layers
still obey their creating account's plan limit.

Conversion wraps the actual registry layer streams with compressed SHA-256/size
and uncompressed DiffID verification. Tar end-of-archive alone cannot produce
evidence: layer application drains through gzip CRC/footer and both byte-stream
ends before mkfs. The full-rootfs first-layer spool preserves the verifier during
its resolver replay. Main two-drive conversions retain the exact above-base
suffix and its original positions, including repeated DiffIDs; full-rootfs and
sidecar conversions retain all layers. A builder that skips consumption cannot
publish a verified producer. Storage matches each ordered consumption fact to
the immutable signed chain before atomically selecting the converted artifact.
Optional JSON fields preserve existing record hashes; older records remain
historical and acquire no image-chain or native authority.

Shared base conversion now records the complete ext4 identity separately from
staged content size, plus the actual injected guest-init digest and consumed
source layers. Immutable private base producer records select a storage key
under nonwaiting key fences. Parent-based conversion binds an exact retained
parent producer and a separate `MaterializeVerifiedParentExt4` capability.
vmmd hashes the same downloaded bytes copied into its root-owned temporary
loopback source before mounting it. A receipt follows copy, unmount and bounded
cleanup; an older server/backend refuses the capability. Child layer selection
keeps descriptor positions, including repeated DiffIDs. The final child ext4 is
hashed during publication and freshly checked from storage before selection.

A two-drive registry app producer retains its exact base producer ID and input
hash. Publication checks the base's uncompressed prefix against the retained
signed app chain and rechecks current base selection under a key fence. Cached
bases require matching retained source, layout, guest-init and parent intent,
and fresh complete stored-byte identity; old config-digest sidecars cannot mint
producer evidence. Exact producer retries preserve the storage clock and cannot
reactivate an older selection. An already-published child retains its historical
parent even if that parent's current selection changes. Shared artifact bytes
are bounded at 16 GiB, keys at 512 bytes and materialization paths at 2,048 bytes
in `pkg/api/limits.go`; cleanup uses the central five-second deadline. These
platform producer records confer no company key approval or scan authority.

Private component scans now bind the selected rootfs producer ID and input
hash, its exact complete artifact digest and byte size, workload, deployment,
tenant and scope. imaged copies the bounded storage stream into a private
0600 scratch file for local and remote backends, hashes the same bytes it
writes, and verifies the protected file and canonical storage bytes again
after scanning. Main and declared sidecars receive separate scans. Grype
extracts ext4 read-only with debugfs, checks the root inode and extraction
diagnostics, and refuses missing match arrays or oversized output. The scanner
name, version and vulnerability database metadata come from the result;
neither caller-supplied completion times nor inconsistent severity counts can
be published as complete evidence.

Publication rechecks the selected producer, original workload reference,
current company publisher key, signed layer chain and any current shared-base
binding under the existing nonwaiting owner fences. Immutable scan creation,
current selection and the main compatibility report update are one transaction.
Sidecar reports never replace the main report. A failed attempt selects explicit
failed component evidence when current inputs permit publication. Revoked or
replaced current approval inputs cannot publish a scan; historical reads remain
available without asserting authority. Exact retries retain the
original storage clock and cannot reactivate an older selection. Complete
high/critical/unknown findings remain visible evidence and block enforce mode;
a sidecar scan failure or unsafe result also uses the live quarantine path.

Component scans have a storage-owned five-minute lease. Legacy scan inputs clamp
that lease to the original producer expiry. Renewed scans name a separate current
registry verification ID and input hash and clamp their lease to that fresh
verification's expiry. The original conversion, signature and scan clocks never
change. The scan pass has a five-minute deadline, and vulnerability databases
may be at most 30 days old and cannot be future-dated. Central bounds are 8 MiB
per retained report, 16 MiB scanner stdout, 64 KiB per diagnostic stream,
100,000 findings, 256-byte version metadata, 128 paths per finding and 4,096
bytes per path. Complete artifact reads use the existing 16 GiB cap. Subprocess
diagnostics are bounded and are not echoed into logs or reports.

The complete shared-base drive now has its own immutable private scan record,
bound to the current base producer ID, input hash, artifact digest, byte size and
retained source reference. Publication and selection share the base key fence.
Fresh reads check the current producer, scan lease and database age against an
advancing storage clock; selection history alone does not assert freshness.
Cached reuse freshly hashes canonical bytes and rechecks the lease without
extending the original scan clock. A new scan attempt receives a new immutable
record. Enforce-mode registry app-layer scanning checks this shared-base report
before scanning the app drive; unsafe base findings refuse deployment.

Before a verified base rebuild or scan refresh, imaged writes a compatibility
refusal so a failed conversion, producer publication or scan publication cannot
leave an older clean sidecar usable. This preflight refusal has a zero timestamp:
it is not a scan attempt. Successful compatibility output is derived from the
separately retained private scan. Failed private evidence keeps zero finding
counts and a closed failure reason; only the legacy compatibility output uses
its refusal sentinel. Shared-base scans use the same protected byte-copy and
scanner bounds as deployment component scans. These platform base records do
not establish approval by a company publisher or native consumption.

For a retained registry conversion, imaged retrieves the exact original proof
through a tenant-scoped historical getter. It cryptographically verifies the
retained signed bytes again under the current stored publisher key, receiving a
new immutable storage-clock verification record. A registry outage does not
prevent that check when the original signature still matches the approved key.
If key rotation invalidates the retained signature, imaged fetches supported
signature attachments only for the retained immutable source/index digest. It
never resolves the customer's mutable tag or selects a different child. Scan
publication rechecks the current key under the owner fences and requires the
same source/index, selected child, config and ordered layer chain as the original
conversion. An expired original producer remains a historical conversion fact;
only a fresh signature verification and a new actual scan provide current
component evidence. Optional scan fields preserve older immutable hashes.

A separate two-minute worker considers the private evidence for the main image,
all declared image sidecars and any shared-base binding. Missing, failed or due
component evidence triggers renewal; the legacy six-hour scanner cadence remains
separate. Shared-base reuse refreshes scans aged two minutes or within two minutes
of expiry through its existing fresh-read and scan path. A busy
owner fence remains a retryable refusal at admission and does not manufacture
failed findings or extend a lease; live workers retry without treating contention
as signer revocation. Other actual verification or scan failures retain the live
quarantine path. This scheduling mechanism does not guarantee fleet renewal
before expiry or establish current evidence at a native consumer.

These are component producer facts under the existing trusted imaged/database
writer boundary. Portable fixtures verify real byte copies, real layer
consumption, PostgreSQL atomicity and refusal behavior; injected mkfs and Grype
fixtures do not establish native scanner execution or consumer ACKs. Complete
two-drive runtime approval, native capture/consumption and dedicated Linux
Grype/KVM/leakcheck acceptance remain pending. Ext4 extraction resource and
cleanup behavior also requires native acceptance.

Separate fresh component reads now revalidate selected producer identities,
current company publisher cryptography, signature and scan leases, and scanner
database age using the storage clock. A coherent deployment read holds the
owner, control, artifact and bound-base publication fences through the complete
set: main image, every declared image sidecar, and each distinct shared base.
It checks all leases again at its final storage clock and returns their minimum
expiry. A deployment index supports the retained-lineage lookup. Historical
retrieval remains separate; neither path rewrites evidence
clocks. PostgreSQL lock contention returns a bounded retryable refusal. Unsafe
complete findings remain visible for the enforcing consumer to reject.

A private runtime-artifact input read now captures producer IDs, immutable input
hashes, storage keys, complete blob digests and byte counts inside those same
fences. It retains the main/sidecar membership and explicit shared-base
associations as separate artifacts. Its scoped, canonical input hash excludes
renewable publisher/scan IDs and clocks: renewing current evidence for the same
producer keeps the byte identity, while replacing a producer changes it. The
returned lease ends at the earliest evidence expiry or scanner-database age
deadline, including the shared base. This input projection rejects missing,
duplicate and inconsistent membership without changing historical evidence.

The input read still supplies stored producer facts. It does not invent an
unbound runtime-default base, scan the actual guest overlay, authorize a native
boot or advance observed adoption. Native grant storage now freshly rechecks its
approval leases. vmmd must verify
the actual consumed base, app and sidecar bytes and return the bound consumer
acknowledgment before those release gates can pass. That integration remains
pending.

New durable admission captures additionally retain that scoped producer identity
under the runtime owner/control/artifact fences and nonwaiting shared-base key
fences. Main, declared image sidecars and explicit bases retain distinct complete
blob digests and byte counts. The PostgreSQL runtime snapshot and the MemStore
compare current selections to the saved inputs before resident publication;
replacing a producer at the same key with the same compatibility size still
invalidates the earlier capture. Retained incomplete lineage cannot fall back
to a capture without producers. Immutable captures expose a separate canonical
producer-set hash that matches the fresh input read and survives evidence
renewal. Reads copy the identities and never backfill historical captures.

New native captures omit the compatibility scan status and report hash only
when the complete scoped private producer identity is present. Current approval
records and leases remain separate from this stable identity. Historical
captures retain their original bytes, hashes and storage clocks. Comparing a
validated producer projection allows these older captures to hand off without
rewriting history; artifacts without private lineage still bind both mirrors.
Native comparison keeps account plans strict and retains every owner, control,
producer, complete-byte, membership, node and egress input. The scheduler applies
the same mirror exclusion to its earlier reads. Managed resident lifecycle
writes additionally require current private approval, so a failed or expired
scan cannot gain authority through the stable comparison. Initial publication
and promotion independently recheck fresh approval; exact committed receipt
recovery does not issue a new grant or renew an old clock.

Producer metadata alone neither authenticates current publisher
approval nor proves actual file consumption. Runtime-default bases without an
explicit producer binding remain unapproved, and source-build publisher
authority and dedicated native consumption acceptance remain release gates.
Protocol 2 now carries content-bound grants and measured drive receipts, as
described below. No observed adoption or public activation follows this change.

Native initial boot and warm promotion now recheck current component evidence
inside their existing owner/control/artifact and explicit-base fences. Go
reverifies retained publisher signatures against the current scoped keys. The
database guards independently bind private proof provenance, current keys and
producer/scan selections, exact ownership and source, complete scan status,
storage-owned lease limits and scanner-database age. Enforce posture refuses
high, critical and unknown findings in main, declared image sidecars and explicit
bases; off/warn posture keeps those complete findings advisory. A signing
requirement cannot acquire a grant from compatibility metadata without a private
producer. Unsigned/source-build producer authority still needs its own boundary.

Storage caps new grants at the earliest component/base evidence or database-age
deadline and the existing ten-minute admission limit. Its final advancing clock
refuses an approval that expires during a slow locked read. Exact token retries
preserve the original issuance and expiry. Boot receipt recording, first runtime
publication and warm promotion recheck fresh approval; saving a receipt alone
cannot authorize later publication after base approval changes. An expired
initial grant remains paused identity history, while resume requires a new grant
and fresh evidence. Recovering the exact acknowledgment of an already committed
promotion does not issue new authority or require an old approval to be renewed.
These checks neither scan the guest overlay nor verify physical bytes consumed
by vmmd, and they never advance observed standard adoption.

Native receipt publication also participates in the existing service recovery
capacity and exclusive-operation lifecycle guards. Memory-store preflight checks
capacity before recording a receipt or changing ownership; PostgreSQL retains
the corresponding statement and runtime triggers in the publication transaction.
A refusal leaves the saved grant, paused runtime and receipt history unchanged.
Eligible recovery can retry the same unexpired grant after capacity returns.
Portable storage tests cover initial paused publication and warm promotion,
including an ineligible host and recovery on a surviving eligible host. They
do not establish a native consumer acknowledgment or release acceptance.

The periodic live lease checker uses this private complete set when retained
producer lineage exists. A missing sidecar, failed or replaced base, expired
lease, metadata drift or revoked publisher cannot fall back to the clean main
compatibility report. It parks enforce-mode applications through the existing
durable quarantine path. Legacy compatibility checking remains available only
for unmanaged applications without retained private lineage. Managed enrollment
without private evidence refuses that fallback. Busy reads defer to the next
worker pass without renewing evidence or manufacturing findings.

These reads authenticate stored producer facts, not the mutable artifact bytes
consumed by vmmd or a scan of the actual guest overlay. A periodic quarantine
worker also does not prove immediate in-flight or cached-route revocation.
Native capture/consumption, complete two-drive runtime approval and live/cached
gateway revocation remain pending. Public activation remains disabled.

Three early application-standard migration IDs had invalid timestamp seconds
and were already committed and applied. Their ledger identities and SQL remain
immutable. The namespace test freezes only those exact filenames and SHA-256
contents; changed bytes, renamed files and new invalid timestamps fail. Future
migrations continue to use the generator and strict UTC validation. This is a
closed compatibility set for issued ledger entries, not a new naming convention.

This is a producer boundary under the existing imaged/database writer trust
model. Source-build publisher approval, complete runtime scans and native boot
consumption remain separate. Artifact keys remain mutable; later consumers must
freshly validate the selected expected digest and size. Existing admission
captures now retain these producer identities when private lineage is present. Historical
current-selection reads may return expired or revoked registry evidence and
must not be treated as runtime authority or observed adoption. Older rootfs
records lacking a base binding retain their immutable hashes and acquire no
base or native authority. Portable tests use real layer application with an
injected mkfs runner, real PostgreSQL and wire capability refusals; they do not
establish native ext4 materialization or KVM acceptance. Public activation and
the remaining acceptance gates stay pending.

Historical record retrieval deliberately does not assert current approval or
freshness: it retains the immutable source needed for a future refresh, including
after key revocation or expiry. Retained metadata can authenticate the
index-to-child mapping, but historical reads do not freshly check mutable stored
ext4 bytes, current publisher approval or scans. Those require consumer binding
and native-admission work. Raw inserts and mutations are guarded against
accidental alternate writers; the trusted imaged/database writer boundary is
not a cryptographic database attestation. Parent erasure cascades delete the
retained evidence.

## Protected native source staging

Private scheduler boot requests now deliver the captured base, main and sidecar
producer blob identities inside the complete hashed boot envelope. vmmd checks
that the set contains exactly one source for every drive, with distinct canonical
storage keys and bounded complete-blob sizes. A runtime-default base without
producer evidence is not exempted. Empty legacy envelopes gain no image evidence.
The existing immutable protocol-1 grants and receipts retain their interpretation.

The native manager requires an explicit verified-source backend before allocating
a lease. JailerVMM reads each complete source through Storage.Get, hashes the same
bounded stream copied into a private native-owned file, and checks digest and byte
count before kernel staging or Firecracker launch. LocalPath and LocalFileLinker
shortcuts cannot borrow a mutable backend inode. Cancellation closes and joins a
blocked reader; failed or partial preparation releases earlier source references.
Concurrent consumers share the sealed read-only base inode. Each writable main
drive still receives its own copy or CoW clone before runtime-file injection;
the two-drive guest overlay and read-only sidecar contract remain unchanged.

Sealed sources now live under a private node-local parent outside jail tmpfs.
Before reading or exposing a source, vmmd syncs a canonical, bounded record of
the owning instance and holds an OS lock on its private process root. The last
release removes the record, files and lock. Startup and periodic sweeps retain
locked roots, young roots, every live durable owner, malformed records, symlinks
and unknown database state. A complete dead owner set permits reclamation after
the age guard. These records grant cleanup authority only; they do not attest to
image approval, consumed drives, guest readiness or adoption. Older unjournaled
temporary files are not inferred to be dead. Process-kill and portable race
tests validate this host-file boundary; dedicated native leakcheck remains open.

Source verification is not snapshot approval. A request carrying these identities
currently uses verified cold boot even when a snapshot cache entry exists. A
paused restore refuses before allocation, because its frozen writable drive has
no bound source-to-snapshot lineage yet. Restoring that capability with verified
snapshot artifacts and promoting the exact retained lease remain full-feature
requirements; the cold-boot restriction is an interim implementation boundary.

The governed cold-boot path now pins the provisioned drive descriptors before
runtime-file injection and hashes every complete staged file against its producer
identity. Drive IDs, root/read-only settings, membership and writable-main inode
isolation must match. After injection, immediately before handing the exact
configuration bytes to Firecracker, vmmd rechecks each pathname's pinned inode
and measures the final complete drives. Read-only base and sidecar bytes must
remain unchanged; the main drive retains separate producer and injected hashes.
Configuration digests contain no configuration or environment bytes.

After readiness, a Linux observer checks the registered live native process's
actual procfs descriptors against every measured drive inode, fixed byte count
and required access mode. It binds the lease UID, process PID and start time,
checks process identity before and after the walk, and rejects O_PATH handles,
missing drives and changed process identity. Retrieval repeats the native check;
teardown closes the pinned descriptors and removes the observation. Portable
fixtures establish refusal behavior, while Linux process and dedicated two-VM
Firecracker tests cover real descriptor access and shared-base/private-main
teardown. Those Linux/native tests have not yet run on the acceptance host.

Protocol 2 now binds the complete captured source set with a canonical,
length-framed SHA-256 hash. Native capability registration saves the supported
protocol with the process incarnation. The scheduler prepares that source hash
before issuing its immutable grant. vmmd returns measured config, process and
drive facts after readiness; missing or changed observations destroy the boot
and return no receipt. Returned facts own their slices independently of saved
history. PostgreSQL and MemStore refuse incomplete source sets, unsupported
protocols, changed process identities and invalid measured facts. Receipt and
runtime publication commit together, including through the raw SQL fence.

The protocol-1 native input digest includes delivered source identities and
retains its original meaning; it cannot acknowledge consumed artifacts. Protocol
2 currently authorizes unpaused verified cold boots. Snapshot restore and warm
promotion still require separately frozen artifact lineage. Neither receipt
advances observed standard adoption or acknowledges log delivery or live egress.
Whole guest-overlay scans,
default-base and source-build publisher approval, snapshot lineage, durable
consumer acknowledgments, full daemon restart recovery, and
dedicated Linux ext4/KVM/leakcheck acceptance remain release gates. Public standard
activation remains disabled. Portable stream and adapter tests establish only the
boundaries described here.

## Measured native snapshot capture

Native snapshot capture evidence version 1 now retains the exact protocol-2
boot receipt and full-stream identities for memory, VM state and the frozen
private main drive. Manager supplies its owned receipt and compares the fresh
catalog grant's parent to that exact receipt. Capture checks the same registered native
process and drive handles at the pause boundary, remeasures read-only drives,
and compares the frozen main with the paused live main. The changed main digest
is separate from the original approved producer and boot-injected digests.
Storage must consume the complete pinned streams with matching hashes before
the capture can return evidence. Cancellation, partial consumption, changed
files and invalid parent facts return no evidence. Teardown cancels and joins
the capture before closing retained drive handles. Temporary Firecracker
outputs are removed before restoring the ordinary memory fence.
Measured warm capture suspends native probes during capture/publication and
restarts them after successful resume. Paused byte measurement must not itself
trigger liveness teardown; pause and publication costs need native acceptance.

Both warm and terminal snapshot responses carry these versioned facts through
the private RPC. Fresh coupled captures use a keyed VM-state artifact on local
nodes as well as remote nodes; legacy local captures retain their host-path
carrier. Legacy protocol-1 receipts retain their original snapshot behavior
and cannot become measured capture evidence.

This evidence is historical lineage, not current scan/publisher approval,
consumer convergence or restore authority. The parent boot grant can expire
without erasing its historical facts. Native namespace preflight protects
already published captures from retry overwrite and cleanup; it is not a
distributed capture claim. A fenced durable capture grant/catalog, current
review and a separately versioned measured restore/promotion protocol are
independent of these byte facts.

The private `application_standard_snapshot_captures` catalog now saves a fresh
version-1 grant before accepting a version-1 acknowledgment. The grant binds
the exact retained protocol-2 boot, source start time, scope, node incarnation,
Firecracker version, capture mode, callback intent and coupled UUID namespace.
Issue and first publication lock current ownership, inherited inputs and
producer/scan/publisher approvals; the grant expires at the earlier of the
runtime admission TTL and the artifact approval deadline, including while the
source is already running. PostgreSQL raw writes enforce the same scope,
canonical scalar shapes, chronology and immutable history as the store APIs.
Numeric JSONB equality cannot substitute decimal or string spellings for
typed protocol integers. A refused write publishes no acknowledgment.

The catalog retains its own parent and byte facts after instance, boot and
source-node cleanup. Reads and exact acknowledgment retries recover committed
history without issuing new authority; expired namespaces cannot be renewed.
Application/deployment/account erasure removes the owned catalog. MemStore
owns all nested parent drive slices, with PostgreSQL parity tests for retries,
stale intent, native process restart, renewed approval and raw-write refusal.
These tests simulate native measurements and do not replace KVM acceptance.

The distinct `CaptureAdmittedRuntime` RPC now requires the scheduler peer and
the fresh typed catalog grant. Native consumption fences the exact retained
parent, node incarnation, Firecracker version and installed outbound revision;
the single-use token and per-instance flight are consumed before pause. The
outbound read gate remains held through final acknowledgment. Cancellation or
invalid completion joins the flight and destroys the source without returning
capture evidence. Generic snapshot entry points refuse protocol-2 residency
without this authority. Older nodes have no legacy fallback for this RPC.
Warm and park are wired; measured migration still refuses before native work
until its recovery and destination admission are implemented.

The scheduler resolves the actually published source receipt, saves the capture
grant before RPC and commits its matching acknowledgment before emitting
`snapshot_written`. The notification carries only the catalog token; it cannot
carry or invent authority. imaged resolves that token in account/app/deployment
scope and checks source, start time, node, coupled keys, sizes, tier and
Firecracker version before its ordinary snapshot publication. Omitting the
reference for a known managed namespace is refused. Catalog history can expire
without being renewed by delayed publication. The immutable memory key links
the ordinary row to its unique catalog namespace. The database-enforced immutable
snapshot/catalog association is now implemented; current restore review remains
required.

Source-bearing managed wakes continue to use verified cold boot, and measured
init-cache reuse remains disabled until a separately versioned restore and
promotion protocol is implemented. Store/RPC/ordering tests simulate native
consumers and do not establish physical-byte acceptance. Dedicated native
capture correctness, pause cost and leakcheck evidence remain pending, and
public activation stays disabled.

## Acceptance checklist

- [x] PostgreSQL and MemStore versioning, tenancy, immutable hashes and parity.
- [x] Resolver defaults, required settings, narrowing, conflicts and provenance.
- [ ] Organization authorization, API, SDK, CLI, generated contract and docs.
- [ ] Org/project/app assignments and automatic enrollment on every create path.
- [ ] Required log destination creation, protected mutation and credential safety.
- [ ] Image signatures/trusted publishers and current artifact verification.
- [ ] Outbound restrictions, runtime convergence and native network acceptance.
- [ ] Approved exceptions, revocation/expiry, repair and admission enforcement.
- [ ] Exact-plan approval, batch rollout, pause/resume, restart fencing, rollback.
- [ ] Effective-state and affected-app views with desired/observed adoption.
- [ ] End-to-end multi-service onboarding and controlled standard-update scenario.
- [ ] Product registry, operational guide and recovery evidence.
- [ ] Verified repair of missing migration ledger entries without changing frozen migrations.

This ADR records the complete intended feature. Individual green tests do not
declare the feature launched or satisfy the entire checklist.

### Native boot contract

The private native capability uses separate `RuntimeAdmissionIdentity` and
`CreateAdmittedRuntime` RPCs. An older server or backend refuses them before VM
allocation; there is no fallback to a legacy wake with a manufactured receipt.
vmmd's native manager owns a startup-bound compute-node identity and a random
process incarnation. Restart invalidates all grants from the earlier process.
Remote grants and identity probes require schedd's verified daemon certificate;
the default-local Unix socket retains its existing filesystem access boundary.

A grant binds a single token and instance, app/deployment/account identities,
desired standard revision, effective/captured-input hashes, complete wire boot
payload hash, actual egress revision and bounded issue/expiry times. Hashing
covers the cold/restore variant, snapshot locators, paused intent, all AppSpec
fields, sidecars and sealed environment bytes. Unknown fields at every depth
are rejected rather than acknowledged without support. No payload bytes or
credentials appear in the receipt. The native adapter additionally hashes its
complete exported input projection and the manager clones it before use.

The manager consumes tokens and instance identities once within a bounded
expiry window, including failed boots. Before allocation it requires an exact
installed egress revision and complete tuple under the per-app read gate. It
does not replace an admitted tuple silently with newer intent. The gate remains
held through receipt creation. Destroy and stop cancel and join admitted boot
flights; cancelled or expired boots receive no receipt, including a late
successful native return. Receipts come from the backend and bind the actual
lease UID, host IP, namespace, boot method and paused state. Both wire boundaries
validate them and clean up a successful boot with a malformed acknowledgment.

The protocol-1 path for managed cold boots, initial snapshot prime and restores saves an
immutable private grant before invoking the native capability. Initial warm
restores retain that protocol with explicit paused intent. Protocol 2 currently
requires unpaused verified cold boot and separate complete source membership.
Storage owns the
issue/expiry clock; grants bind the captured node, actual durable egress revision
and native process incarnation registered by vmmd before serving. The complete
egress projection is installed through its revisioned RPC before the boot.
PostgreSQL retains nonwaiting instance/input/node fences until receipt and
runtime tuple/state commit atomically; MemStore mirrors this boundary. A failed
publication leaves neither a receipt nor a partial runtime tuple. Raw managed
runtime publication requires the saved matching receipt. An initial receipt
cannot authorize another boot on its instance row. Each instance has one saved
initial grant; a storage retry recovers only its exact existing token. Owner
erasure removes its private capture/grant/receipt history. No legacy history
gains native authority.

Managed warm promotion saves a distinct, single-use grant before resuming the
same resident lease. Its payload uses a promotion-specific hash domain and binds
the complete historical paused receipt. The parent identifies the lease after
its initial grant expires; only the fresh promotion grant authorizes resume.
Native validation holds the per-app policy gate through resume and receipt,
checks the installed full egress projection and refuses legacy managed resume.
The guest resume hook completes entropy reseeding and clock correction before
monitors and receipt publication. A failed hook destroys the resumed VM.
Cancellation joins the native flight and destroys a late success. Promotion
receipt and WARM-to-RUNNING publication commit together, retaining the original
capture and initial boot history. Publication retry accepts only the exact
already-committed receipt, including after grant expiry, while current input and
process fences remain valid. History cannot recreate residency after cleanup.
These unit and storage boundaries do not establish native KVM acceptance or
complete daemon restart recovery.

Native publication also passes the ordinary recovery-capacity and exclusive
owner guards before recording its receipt or changing lifecycle state. A refusal
preserves the previous runtime and any active owner. Unmanaged resident guests
retain their admitted shape across an account plan change without rewriting the
capture. Only the plan comparison is relaxed, and only when both snapshots have
no adoption or retained managed fields. New boots and managed native grants stay
strict; current eligibility, controls, artifact identity and capacity still apply.

Fresh install and normal upgrade are verified, but the whole added migration
set currently fails the missing-ledger replay gate at the frozen initial
`20260930170711001_application_standard_versions.sql` table creation. Passing
replay checks for the latest additive migrations does not satisfy this release
gate. Recovery must verify the expanded schema and repair its ledger explicitly;
no committed or applied migration may be edited and no runtime authority may be
manufactured from that repair.

A managed node requires vmmd's existing compute-node registration with database
configuration (including a named default-local node on a single box). A legacy
local process without registered native identity or an older native backend
refuses managed admission before allocation. Native process registration does
not query inherited customer intent.

This remains partial enforcement. Protocol 2 binds measured native drive
handoffs; log delivery, established-flow tightening and all live-instance egress
convergence still require consumer acknowledgments. Neither protocol advances
an observed standard revision. App tasks,
migration attempt authority, restart reconciliation and existing gateway revocation
remain acceptance work. Dedicated native x86_64 KVM and leakcheck evidence is
still required.

### Source-build content verification

imaged now verifies builderd's local OCI export before container conversion,
function layer selection or Go executable normalization. One opened archive
supplies the index, manifest, config and layers. Manifest/config/compressed layer
bytes must match their descriptor digests and sizes; the full decompressed gzip
stream must match each config DiffID. This prevents a false base-layer prefix
from dropping different runtime content. Duplicate index, manifest, config or
layer entries and nonregular target entries are refused. Repeated layer
descriptors remain supported with independent readers. All production readers
use the imaging context for cancellation.

The intermediate export is bounded independently of billing: index 1 MiB,
manifest 8 MiB, config 16 MiB, archive 16 GiB plus 32 MiB of metadata allowance,
1,024 layer descriptors, aggregate compressed layers 16 GiB and aggregate
uncompressed layers 64 GiB. These safeguards live in `pkg/api/limits.go`.
Final app-layer limits still come from the creating account's plan.

These checks follow the [OCI descriptor contract](https://github.com/opencontainers/image-spec/blob/main/descriptor.md)
and [uncompressed DiffID definition](https://github.com/opencontainers/image-spec/blob/main/config.md#layer-diffid).
They establish internal content integrity, not an approved publisher or a
durable runtime artifact proof. Company publisher verification of source-build
output, the actual rootfs/sidecar binding, current-key revocation and scan expiry
remain required before public activation. No artifact check advances an observed
standard revision.

### Enrollment evidence

Unowned legacy app inserts outside assigned scopes retain their compatibility
without inventing a company enrollment. Project review and assignment reject
every current unowned member; nullable ownership cannot escape that check.
An organization owner cannot be cleared, even before any standard is assigned.
Attaching a verified owner captures its admission pins and requires installation
before deployment or resident runtime transitions. Restoration and application
UUID reuse revalidate retained project and application assignments.

Unowned runtime compatibility is limited to nondeleted apps of active or
past-due accounts without an abuse hold or retained company intent. It creates
no application-standard capture, native grant, receipt or observed adoption.
Direct native authorization still requires a verified owner and enrollment.
Owner attachment and runtime admission share the persisted app row fence;
unowned legacy residency cannot become company runtime authority. Portable
MemStore and real PostgreSQL tests cover these ownership and residency
boundaries, while native consumer and full release acceptance remain pending.

The storage boundary now captures admission pins at every app insert, including
raw project/reconcile/preview inserts, and revalidates restore and scope changes.
Deployment insertion is fenced in PostgreSQL as well as MemStore while enrollment
is pending, applying or blocked. Project-row membership locks prevent an
activation from overlooking a concurrent member owned by another organization.
Assignment identities are fixed, updates require the next revision, and direct
deletion is fenced except through owning-organization erasure. Tests cover both
orderings of the project membership race, foreign tombstone restore, transaction
rollback, immutable adoption reads and candidate publication without activation.

Private persisted assignment reviews now bind affected service inputs, scoped
membership, local values, resources, account entitlements and artifact metadata
to an approval digest. Freshness probes reread storage; they cannot be used as
authority for an unlocked later write. Reviews validate admission combinations
for future services and empty projects, preserve explicit defaults/logging extras,
and reject aggregate quota or unverified artifact changes. Tests exercise
PostgreSQL/MemStore parity, input mutation, cross-organization scope denial,
immutable operation history and account/organization erasure boundaries.

Private atomic approval now advances the explicit admission pointer and saves
the operation, frozen per-app inputs/projections and audit in one transaction.
Current actor authorization is required even for an idempotent retry. Expiry,
staleness, blockers and overlapping operations cause no intent writes. Parent
input fences cover controls, account quota, artifact children and retained live
artifacts; bounded nonwaiting lock acquisition avoids legacy lock-order cycles.
Tests cover both project membership orderings, duplicate approvals, injected
audit failure rollback, existing-pin preservation and restore lease revocation.
No approval advances a persisted or observed application revision.

The private PostgreSQL/MemStore materializer now claims operations with expiring
worker generations and installs each frozen target atomically into actual app
settings, log drains and trusted signer rows. Logical resource bindings keep
physical identifiers out of inheritance; private ciphertext backups preserve
legacy controls for reviewed removal. Installation rechecks the approved app,
current entitlements and resolver projection. Managed writes are protected at
both store and raw SQL boundaries, with nonwaiting child-row acquisition.
Persistence advances only persisted revisions. A later wave waits for actual
observation; no consumer acknowledgment is fabricated. Tests cover all six
controls, sealed baseline restoration, stale inputs, lease replacement, restart
wave gates, raw SQL protection, child-row contention and transaction rollback.

apid now runs bounded repair passes over durable reviewed operations and automatic
enrollments. Automatic installation resolves captured adoption pins, checks
current creating-account entitlements and fences authority by generation, desired
revision and storage-owned expiry. Reviewed queued targets take precedence.
Reenrollment retains last-installed field context, so project removal restores
original controls and restore cannot turn an inherited default into local intent.
Tests exercise PostgreSQL/MemStore onboarding, detach, restore, blocked recovery,
review precedence and worker replacement; PostgreSQL expiry after physical writes
rolls the entire installation back. Persisted installation does not acknowledge
any runtime consumer.

The durable schedd egress consumer now sends the complete CIDR/extra-port tuple
and its actual revision through a separate `UpdateAppEgressPolicy` RPC. Old
nodes refuse the method before network mutation. New clients require an exact
echoed revision before recording success. Within a vmmd process, a cancellable
per-app gate orders writes and denies older, conflicting or legacy updates once
a revisioned policy has been accepted. Failed physical writes retain the newer
accepted intent for retry. Wakes share that gate, use the latest accepted tuple,
and publish before an update enumerates live instances. Different apps and
ordinary simultaneous wakes still progress independently. A live plan that
cannot represent the complete port projection is rejected rather than silently
truncated and acknowledged. Unit and wire tests cover these orderings and
failures; the native network gate remains open.

The revision cache is process-local. Managed initial boots and warm promotions
bind its installed revision and complete projection to the native process.
Migration admission and vmmd restart convergence still need evidence. Existing native CIDR patch failure
recovery, established-connection tightening and host firewall reload outcomes
must be verified before these RPC acknowledgments can advance standard rollout
observation. This addition does not advance any standard observed revision or
enable public activation. Upgrade vmmd before deploying the revisioned schedd
consumer; unsupported nodes remain pending rather than receiving an unversioned
fallback.

Public review/activation, permitted local intent mutations, exceptions and
consumer observation are still pending.
Schedd now checks the persisted enrollment envelope before wake reuse, new
admission, explicit-deployment prime/smoke, warm-pool creation, restart, app-task
runtime setup and migration-spec construction. Pending, applying, blocked,
missing or mismatched scope/revision envelopes refuse admission. An unavailable
reader fails closed. A valid persisted projection may boot to obtain actual
consumer evidence; these reads never fabricate observation. Tests use the real
reviewed MemStore admission and automatic materializer, including a restored app
that still has a running instance. Denied paths perform no VM lifecycle work.

The read guards alone do not close the concurrent change window after a spec is
captured. Initial native boot and warm promotion now add durable grants and
matching atomic publication receipts. Migration tickets, the separate app-task
runtime owner and already-cached gateway routing remain required. Current image proofs and existing-runtime behavior remain part of the
runtime acceptance work before public activation is enabled.

New wake instances now capture persisted standards intent, app controls,
account entitlements, log destination/auth hashes, signer fingerprints and
deployment/sidecar-layer identities in the same transaction as their admission.
Nonwaiting parent and advisory fences retain that input cut through commit.
PostgreSQL raw state/runtime writes and the MemStore refuse publishing a capture
whose covered inputs changed. Captures are immutable and erased with their
instance; managed legacy instances cannot obtain invented historical captures.
Cleanup remains possible while intent is pending, and billing grace retains
serving eligibility. Wake, prime, warm preparation and migration-spec construction
also compare the scheduler's earlier app/account/artifact reads with the capture.
Prime now publishes runtime and RUNNING through the checked atomic CAS, destroys
a refused VM and releases its ledger reservation before returning an error.

The capture identifies control-plane inputs; separate durable grants bind the
complete delivered boot payload and native incarnation, and initial boot or warm
promotion publication requires the matching native receipt. Together these
boundaries still do not cover the separate app-task runtime table, migration
attempts, expired exceptions, content-bound image proofs, cached gateway routes,
complete live-egress/log convergence or consumer observations. Native KVM
acceptance, existing-runtime convergence and the remaining acceptance gates are
required before public activation. No capture or native boot/promotion receipt
advances an observed standard revision.

Managed cold boots with artifact protocol 2 now publish the measured producer
and injected drive facts. Warm and park capture use a separate single-use grant
and native RPC. The grant binds the published source residency and a private
memory, VM-state and frozen writable-drive namespace. The scheduler saves that
grant before capture and publishes the exact native acknowledgment before
notifying imaged. The catalog survives source instance cleanup; it is erased
with the owning application, deployment or account. Shared base and sidecar
drives remain read-only; the per-instance main drive remains private and writable.

The ordinary snapshot row retains an immutable foreign key to this published
capture. Database triggers and MemStore compare ownership, namespace, capture
mode/tier, Firecracker version and memory/VM-state byte counts. Catalog grant
insertion and snapshot publication serialize on the memory key, so a preceding
legacy row cannot later gain capture authority and a saved grant cannot publish
without its reference. The additive upgrade links only exact published history
and marks known mismatched rows stale. Stale/delete-pending and physical allocation
updates remain possible, and cache GC preserves the historical catalog.

This association is historical evidence, not a current restore grant. Unknown
legacy rows remain unproved cache data. Protocol 2 measured sources currently
take a verified cold fallback rather than restoring or promoting a cache with
old protocol semantics. Fresh approval of the overlaid runtime, a separately
versioned restore/promotion grant, migration recovery, real consumer convergence
and dedicated Linux amd64 Grype/ext4/KVM/leakcheck acceptance remain required.
The public activation gate remains disabled.

### Bounded scanner filesystem handoff

The default Grype directory path now stages a separate readable filesystem
projection before executing the scanner. Exact source snapshots fence the copy;
the retained result binds complete regular-file bytes, paths, node types and raw
symlink targets. A second digest binds symlink visibility within the guest root.
Absolute links refer to that virtual root, including component-by-component
resolution of symlink/parent combinations. Staged links are relative and remain
within the private scanner tree; dangling and cyclic links stay unreadable.
Mode and ownership metadata are not scanner identity and the readable projection
is never a bootable rootfs or writable runtime drive.

The bounds in `pkg/api/limits.go` allow at most 32 GiB of logical regular-file
bytes, one million entries including the root, 64 MiB of aggregate path/link
text, 4,096 bytes per path and 40 symlink hops. Directory entries are read in
batches of 512 and retained name budgets are checked before sorting. The empty
destination check reads only one entry. Stable directory handles confine reads;
changed file identities, nested filesystem directories, devices, sockets and
FIFOs refuse the handoff. A replaced final symlink or FIFO is not followed or
opened with blocking semantics.

After the scanner exits, its actual staging tree must still match the complete
retained identity. Same-size content changes and raw broken-link changes refuse
even when the scanner returns clean JSON. Cancellation, scanner/parse failure
and ordinary completion remove private staging; cleanup failure cannot return
a successful scan result. These guards are exercised through the real default
dispatch with executable scanner fixtures, not a substituted scan callback.

This is the filesystem handoff needed for composed-runtime scanning. It does
not implement a native read-only OverlayFS view, bind that view to a whole-runtime
approval or authorize restore/promotion. Raw upper whiteouts are not interpreted
as a merged guest root. The earlier debugfs extraction itself still needs resource
and native ext4 acceptance; the projection bounds start after extraction. Real
Grype, native composition, KVM and leakcheck evidence remain pending. Public
activation remains disabled.
