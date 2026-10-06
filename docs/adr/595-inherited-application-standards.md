# ADR-595 · Versioned inherited application standards

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

Upstream assigned ADR-431 to bounded gateway trace retention. This decision moved to ADR-581; frozen SQL files and their historical citations remain unchanged.

Upstream assigned ADR-581 to managed PostgreSQL uncertain accounting intent.
This decision moved to ADR-586; ADR-585 is reserved by the environment isolation
PR. Frozen SQL files and their historical citations remain unchanged.

Upstream assigned ADR-586 to the transactional operation handler SDK and added
ADR-587 through ADR-591, including ADR-590 for complete environment clones.
This decision moved to ADR-593; frozen SQL files and
their historical citations remain unchanged.

Upstream subsequently assigned ADR-593 and ADR-594 to Go HTTP route impact.
This decision now uses ADR-595. Frozen SQL files and their historical citations
remain unchanged.

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

Project apply's source enqueue follows the same apid-owned projection boundary.
After reconciliation, the request may claim only the exact application,
organization and desired revision it read, using the existing enrollment lease.
It installs the already captured pins before staging source or consuming deploy
rate, then rereads current ownership and enrollment before enqueue. This avoids
making a newly created service's first build wait for a periodic repair tick.
The ordinary deployment insertion fence remains authoritative.

Interactive claims cannot take another application's pending intent, steal an
unexpired lease, retry a blocked projection ahead of its background retry policy,
or overtake a queued reviewed target. Approval arriving after the claim still
fences automatic installation through the existing materializer. Failed requests
release only their own lease generation; late releases cannot revoke a replacement
worker. A request interrupted before release remains recoverable through the
normal lease expiry. All work is intent projection; no apid runtime call,
consumer observation or rollout wave advancement is added.
The request compares enrollment and freshly loaded ownership with the scope
captured by reconciliation. A scope change found at either read refuses the
old project's source enqueue. A deleted project returns a conflict instead of
an internal error when the response reloads its application inventory.

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

Public review/activation, public local intent mutations, exceptions and
consumer observation are still pending. The internal local intent path is
described below.
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

## Guest-compatible whiteout conversion

The optimized app-layer assembler now converts an OCI `.wh.name` entry to a
0/0 character-device whiteout at `name`, as required by
[Linux OverlayFS](https://docs.kernel.org/filesystems/overlayfs.html).
Keeping the archive filename would hide `.wh.name` while leaving the shared
base's `name` visible. The assembler removes the previous upper entry before
creating the whiteout; it does not flatten or modify the shared base.

A later ordinary file, symlink or hardlink replaces that device before being
written. A recreated directory receives the opaque attribute so its previously
deleted lower children cannot reappear. This also applies when a later file
implicitly creates its parent directory, after ancestor symlinks have been
resolved inside the private staging root. Unsupported tar entry types cannot
clear the final whiteout. Ordinary files, directories, symlinks and other device
identities are not mistaken for 0/0 whiteouts.

Opaque conversion requires `trusted.overlay.opaque=y`, the namespace selected by
guest-init's existing mount options. It refuses a failed trusted-xattr write;
falling back to `user.overlay.opaque` would not enforce the deletion in that
guest. No additional daemon capabilities are granted. Native-owner conversion
and its scanner handoff still need to be wired before unprivileged imaging can
approve these cases.

`TestMetalApplicationStandardOverlayWhiteouts` is a new guarded acceptance gate.
It requires explicit opt-in, root, the dedicated acceptance-host marker and a
native amd64 KVM host. It reexecutes in a separate private mount namespace,
creates actual separate ext4 base and app drives, mounts the base read-only and
the app privately read/write, and checks deletions, opacity, supported recreation
and unsupported-entry refusal through the same upper/work shape as guest-init.
It compares that view with an actual read-only multi-lower OverlayFS view and
hands the latter through the bounded scanner projection, including an absolute
guest symlink. The read-only view must refuse writes, the base artifact must
retain its exact bytes, and owned mounts are released in reverse order.

On the designated host, run the focused gate from the reviewed source build:

```sh
FAAS_RUN_APPLICATION_STANDARD_OVERLAY_TESTS=1 \
  go test -p 1 -tags metal -timeout 3m \
  -run '^TestMetalApplicationStandardOverlayWhiteouts$' ./pkg/rootfs
```

Portable tests and Linux compilation do not execute this gate. The gate has not
yet run on the dedicated host; it also does not replace actual Grype execution,
guest-kernel KVM consumption, current whole-runtime approval or leakcheck.
Public activation remains disabled.


## Protected native materialization lifetime

Verified parent materialization reserves a mount slot before storage reads,
source staging or loopback mounting. The owner attaches the exact mountpoint,
source file, storage key and mount kind to that reservation and retains the
lease until copying and cleanup finish. Capacity exhaustion refuses new work
instead of releasing an in-flight mount. Both legacy production mount paths
also reserve before mounting; the legacy mountpoint API explicitly hands its
mount back to its caller and the orphan sweep.

A verified copy cannot be externally unmounted, forgotten, replaced, evicted
or swept while its lease is active. Cleanup has a separate bounded context even
when the imaging request is canceled. The registry serializes each physical
release without holding its mutex across the subprocess. Cleanup failure keeps
the exact ownership record and whether the kernel mount has already been
released. A retry can therefore remove a failed source or mount directory
without trying to unmount the released filesystem again. Unknown mount kinds
remain visible for operator repair; refusal to dispatch does not prove cleanup.

A cleanup failure clears the verified materialization receipt. A later orphan
sweep can recover the resource but cannot upgrade the earlier failed receipt.
The mount RPCs report capacity exhaustion as `ResourceExhausted` and refuse
external release of an active owner with `FailedPrecondition`.

Parent mounts explicitly select ext4 and use `ro,noload,nodev,nosuid,noexec`.
`noload` prevents journal replay from modifying the measured source even on a
read-only mount, as described in the
[Linux ext4 administration documentation](https://www.kernel.org/doc/html/latest/admin-guide/ext4.html).
This does not approve an unclean filesystem for runtime use.

`TestMetalApplicationStandardOwnedParentMount` exercises a real ext4 mount,
copy, capacity refusal, external-release refusal and final source/mount cleanup
on the designated native Linux amd64 KVM host. It re-executes in a private mount
namespace, verifies the namespace against its live parent, and binds a private
fixture over the existing native mount root only inside that namespace:

```sh
FAAS_RUN_APPLICATION_STANDARD_OWNED_MOUNT_TESTS=1 go test -p 1 -tags metal -timeout 3m -run '^TestMetalApplicationStandardOwnedParentMount$' ./pkg/vmmdmount
```

Portable lease, concurrency and wire checks are not native mount acceptance.
This lifecycle change does not implement whiteout conversion for unprivileged
imaged, bounded bootable parent copying, composed-runtime approval, restore or
promotion authority, consumer adoption acknowledgments, or restart recovery of
the in-memory parent-mount registry. Those gates and public activation remain
pending.

## Main and sidecar filesystem composition

The optimized main artifact retains overlay whiteouts because its application
layers can delete content from the shared base. A sidecar receives every OCI
layer and runs from its own read-only root; its builder instead materializes
deletions and opaque directories without device nodes or trusted overlay
attributes. Both paths finish verified layer consumption before publication.
Packaging uses a fresh wrapper directory and preserves every customer path,
including a file, directory or symlink named `/upper` and a customer-owned
`.faas-app-upper-source` directory.

Successful root-level OCI opacity is recorded during conversion and written
onto the wrapped application root. It cannot be inferred from a failed xattr
write. Linux OverlayFS always treats its root as merged; the root inode
construction in the [configured Linux v6.1.134 implementation](https://raw.githubusercontent.com/gregkh/linux/v6.1.134/fs/overlayfs/super.c)
does not exclude the lower root when the upper root is opaque. The guest
therefore selects a verified empty lower directory for an opaque application
root. Its shared read-only boot base and private read/write main drive remain
distinct. Ordinary application roots keep the shared base as their lower.

`pkg/overlaymetadata` defines the supported trusted attribute namespace and
lower selection used by the guest and the guarded scanner-view fixture. Only
an absent attribute or the supported one-byte `y` (opaque) and `x` (whiteout
hint) values are accepted. Permission, unsupported-filesystem, malformed-value
and root-replacement failures refuse composition. The native reader opens a
directory without following the final symlink and reads the attribute through
that descriptor. Empty lower preparation refuses a symlink, non-directory or
nonempty path and checks directory identity around its bounded entry read.

The read-only scanner view uses application-upper plus base for ordinary
roots, or a single application-upper lower for opaque roots. It remains an
OverlayFS view so residual whiteout devices are interpreted rather than
scanned as application files. The native acceptance fixture now covers both
root modes, customer path preservation and an independently mounted read-only
sidecar; it verifies its private namespace against its live parent.

Portable packaging, sidecar conversion and metadata tests do not execute the
Linux kernel or prove that mkfs preserves the attributes. The guarded native
fixture must still run from the reviewed commit on the designated host. Native
conversion for unprivileged imaged, actual composed-runtime materialization,
Grype execution, current durable approval, consumed-byte boot/restore/promotion
authority and consumer rollout adoption remain pending. Public activation
remains disabled.

## Verified composed runtime scan handoff

The additive `MaterializeRuntimeScan` RPC accepts a versioned private producer
input hash and a complete base, main and sidecar artifact set. It validates
unique roles and storage keys and the aggregate source-byte ceiling. vmmd
reserves every source and overlay mount slot before fetching storage bytes.
Each complete stream, including ext4 metadata and unused capacity, is hashed
while being written to a private source inode. Source read/close, sync or byte
identity failure prevents a receipt.

All source drives mount separately as read-only ext4 with
`ro,noload,nodev,nosuid,noexec`. An app-layer main uses an actual read-only
OverlayFS composition, selecting its upper alone when root opacity excludes
the base. A full-rootfs main uses its independent root after marker validation;
sidecars use their independent `upper` roots. Neither a flattened boot image nor
a writable scanner overlay is produced.

Output must be an empty private direct child of the platform staging root with
the runtime-scan prefix. Parent and child directory handles are pinned before
copying. Projection writes use those handles and cannot follow a replacement
output pathname. Receipt verification checks directory identity, owner, group
and privacy after native cleanup. Every copied node inherits the scanner
directory's ownership so unprivileged imaged can read and remove its projection.
The scanner view uses the existing bounds on bytes, entries, paths and links,
with an aggregate budget across main and sidecars. Any cancellation or cleanup
failure clears the receipt; failed native mount cleanup retains its ownership
record for recovery.

The receipt binds the input hash, canonical complete-source hash, raw composed
trees and guest-confined scanner projection trees for every workload. Native
byte verification does not prove that a caller-provided producer hash is
current or authorized. `Handler.ScanProducedRuntime` fetches private inputs
before the handoff, checks each projection before and after scanning, and
fetches fresh producer inputs again before returning evidence. The runtime
runner forces directory scanning; a customer file named `rootfs.ext4` cannot
substitute a nested image. Guest and scanner full-rootfs marker lookup now
share bounded guest-root parent-symlink resolution before pivot.

The scanner now uses a separate producer-only input read, described below.
This capability now has private durable fact publication, described below,
and is called by the automatic deployment and renewal paths described below.
Source-built/function base binding and approved publisher evidence remain
required. Signed full-rootfs image base binding is described below. Component debugfs extraction has
not been replaced. Native conversion for unprivileged main-image whiteouts,
consumer adoption and full onboarding E2E also remain pending. Native
boot/restore/promotion authority is described below. Public activation remains
disabled.

`TestMetalApplicationStandardRuntimeScanMaterialization` requires explicit
opt-in, root, the dedicated host marker, native Linux amd64 KVM and a verified
private mount namespace. It binds only private fixtures over the existing
staging roots, builds real separate ext4 sources, exercises ordinary and opaque
roots through the production native manager, verifies complete cleanup and
unchanged source artifacts, and makes an unprivileged process read and remove
the projections:

```sh
FAAS_RUN_APPLICATION_STANDARD_RUNTIME_SCAN_TESTS=1 go test -p 1 -tags metal -timeout 4m -run '^TestMetalApplicationStandardRuntimeScanMaterialization$' ./pkg/fcvm
```

The guarded gate has not run on the designated host. Portable filesystem,
producer-fence and wire fixtures, and Linux compilation, do not prove native
mount behavior, actual Grype execution, KVM boot or leakcheck.

## Producer-only composed scanner bootstrap

`DeploymentRuntimeProducerInputStore` supplies the composed scanner with the
complete current main, declared image-sidecar and explicit base producer set
without requiring component or base scan records. Its separate result type is
input evidence; it cannot substitute for a durable composed scan. Native
grants and resident lifecycle publication require the fresh composed scan
described below whenever private producer lineage is captured.

The read acquires the existing owner, control, artifact and explicit-base
fences. It selects the latest scoped publisher signature for each workload,
revalidates that signature against the current trusted key, and verifies that
its signed image chain matches the immutable conversion and consumed base
prefix. It checks current producer pointers, deployment/rootfs metadata,
complete sidecar membership and distinct source keys. Retained incomplete or
stale lineage never becomes legacy absence. Unbound runtime-default bases are
still refused; this read does not invent a base for a full-rootfs or source
deployment.

The canonical input hash matches the existing private artifact identity and
excludes renewable signature IDs and clocks. A replacement producer changes
that identity even when the key, size and artifact digest remain identical.
The lease ends at the earliest current publisher signature expiry, rechecked
at the final storage clock. An expired latest signature cannot fall back to an
older one. A fresh signature may cover the same expired original conversion
without extending or rewriting its immutable origin evidence.

`Handler.ScanProducedRuntime` uses this read before materializing the native
views and again after scanning. It can obtain new scan evidence when component
scans are missing, failed or expired. The separate component evidence read
still refuses those conditions; native runtime policy consumes the composed
findings described below. Actual producer, base and sidecar replacement or
publisher revocation during scanning prevents evidence from returning.

Portable store and handoff fixtures and PostgreSQL storage-clock and lock
tests cover this bootstrap boundary. They do not prove whole-runtime approval
publication, real native composition/Grype/KVM execution or observed adoption.
Automatic deployment/renewal integration and native consumer authority are
described below; broader consumer adoption still requires acceptance.

## Durable composed runtime scan facts

`DeploymentRuntimeScanStore` persists an immutable versioned whole-runtime
scan input and a separate current selection. The input contains the scoped
producer identity, canonical complete-source hash, raw composed tree and
scanner projection tree for every main/image-sidecar workload, and its Grype
report. The temporary scanner pathname is discarded. Each report identifies
the raw composed tree in `image_digest` and the complete source set in
`artifact_digest`; these fields describe composed scan facts, not an OCI image
signature or a new flattened drive.

Publication reads and cryptographically checks the complete current producer
set again under the owner, control, artifact and base fences. A receipt for a
replaced producer cannot publish, including replacement with the same source
key, digest and size. Current publisher signatures must be valid at the final
storage clock. Report membership, tree versions, projection bindings, scanner
metadata, finding counts and existing size/path limits are validated before
the record is selected. HIGH and CRITICAL findings are preserved as facts;
publication does not itself approve them.

The immutable lease is bounded by five minutes, every current publisher
expiry and every report's scanner database deadline. Scanner database clocks
are normalized down to PostgreSQL's microsecond precision before hashing.
Reads do not extend the stored lease. Fresh reads check current producers,
signatures and database age again and expose the earlier of the stored and
current publisher deadlines. Historical selection reads do not assert
freshness. Exact retries preserve the stored clocks; retrying a superseded ID
cannot select it again. A bounded failed record replaces the previous current
scan, so a failed rescan cannot leave an earlier success eligible for a fresh
read. A stale failure cannot overwrite a replacement producer's facts.

`Handler.ScanAndPublishProducedRuntime` is a private publication job entry
point. It materializes and scans through the existing native handoff, waits
for cleanup, converts all workload reports to the durable contract and
publishes with the storage fences. A safe failure code is published through a
bounded cleanup context when the producer set remains current. Cancellation,
busy fences and stale producer sets do not publish a success or reselect an
old record. The deployment pipeline and renewal worker now invoke this entry
point as described below.

The append-only runtime scan migration protects immutable rows and current
selection writes, uses the existing artifact-child fence, and permits only
deployment deletion cascades. PostgreSQL timestamps and SQLC queries are the
production persistence path. Frozen migrations are unchanged.

Portable materializer/Grype fixtures and real PostgreSQL tests cover facts,
failure selection, scope, nonwaiting fences, publisher revocation, producer
replacement, immutable guards and database expiry. They do not prove actual
native composition, Grype execution, KVM boot or leakcheck. Native
boot/restore/promotion and resident consumers now require durable composed
scans whenever private producer lineage is captured, as described below. The
source-built/function base binding and publisher proof,
main-image native whiteout conversion, observed adoption and full onboarding
E2E remain required. Public activation remains disabled.

## Native composed scan authority

Native boot and restore grants, promotion, first runtime receipt publication,
resident admission renewal and snapshot capture/publication now read the
selected durable composed scan under the existing owner, control, artifact
and explicit-base fences. The scan must be complete, current and fresh, with
its canonical producer identity matching the immutable instance capture.
Current publisher approval is checked again; Go revalidates its signature
against the current trusted key. Neither a component report nor a base report
can replace a missing composed scan.

Enforce policy rejects CRITICAL, HIGH or UNKNOWN findings in any composed
main or image-sidecar view. Component/base reports remain separate facts. A
component or base HIGH finding does not override a clean current composed view: a
base package may have been hidden or removed by the app layer. Advisory
policy preserves composed findings without using them to reject admission,
but still requires current composed evidence when producer lineage exists.
The deadline is bounded by the immutable scan lease, every report's scanner
database deadline and every current publisher approval expiry. A failed
selected rescan blocks new authority and first publication through an earlier
grant. Historical acknowledgment retries do not issue new authority or renew
clocks.

The PostgreSQL native deadline function uses the same complete source hash,
byte-exact Go identity hash, view membership, tree/projection versions,
aggregate bounds and finding counts. Raw SQL grants and publication use that
function as well as the existing consumed-drive and native protocol guards.
Its final storage clock rejects authority that expires during a read. The
new authority migration and its JSON operator-precedence correction are
append-only; all previously applied migrations remain byte-identical.

This changes the authority read, not the drive architecture or instance
capture history. The shared read-only base, private writable main layer and
independent read-only sidecars retain their separate identities. Signed or
enforcing applications without complete producer lineage remain refused.
Historical unbound runtime-default bases remain refused. Signed full-rootfs
base binding is described below; source-built/function publisher evidence
remains pending. The existing unsigned off/advisory compatibility path without
private lineage still cannot claim composed scan or native consumption proof.

Real PostgreSQL and MemStore fixtures cover composed findings, missing scans,
sidecar database expiry, failed rescans, publisher renewal, consumed receipts,
snapshot gates and historical recovery. Their reports and native receipts are
explicit simulations; they do not prove real Grype execution, native mounts,
KVM boot/restore/promotion or leakcheck. Automatic deployment and renewal
routing now use composed findings, as described below. Native main-image
whiteout conversion, observed adoption and full onboarding E2E remain
required. Public activation remains disabled.

## Automatic composed deployment and renewal routing

The post-conversion deployment scan hook now detects retained private
producer history and calls the composed scan publication job before release
work or snapshot priming. It renews publisher approval against the exact
retained subject, materializes native views, scans every main/image-sidecar
workload, publishes durable facts after cleanup and checks current selected
facts again. Enforce policy blocks CRITICAL, HIGH and UNKNOWN findings in any
view. Off/advisory findings remain visible, while unavailable or invalid
composed evidence refuses every retained producer path: native authority
requires that evidence regardless of findings policy. Unproved legacy
conversions cannot gain a runtime-default base or source publisher through
this hook.

Retained presence has a separate scoped read in both stores. It includes
historical producer rows even when current metadata has drifted or selection
is incomplete, and it neither reads component reports nor asserts freshness.
A validated current-producer getter may return not found after metadata drift;
that result is insufficient to establish legacy absence. Only absent private
history may use the existing unmanaged legacy path. Managed standards keep
their existing refusal for legacy evidence without private lineage.

The existing two-minute private renewal worker now reads composed scan
leases, independent of the six-hour legacy scanner interval. It reconstructs
work from producer and scan rows at startup, retries missing/failed selection,
checks current bindings and publisher deadlines, and renews before the
immutable five-minute lease expires. Live and pending snapshotting
deployments are eligible. Pending release/prime renewal publishes facts without
quarantining the application's previously serving deployment or advancing a
rollout. Parked security regressions are not automatically reopened.

The live scanner and cheap lease checks use composed findings and the
storage-owned clock. Missing component reports, component failures or
component/base HIGH findings cannot quarantine a current composed-clean
service. Unsafe composed views or failed current scans use the existing
durable quarantine path and bounded audit data. Component and base scanners
retain separate diagnostic facts and compatibility output; they do not decide
composed-runtime admission.

A nonwaiting per-deployment coordinator in imaged prevents overlapping deploy,
startup and renewal jobs in the same process from repeating expensive work.
Active entries are removed after success, cancellation and failure. Durable
publication and native ownership fences remain authoritative; this coordinator
is not a lease or a consumer acknowledgment. Busy returns preserve current
scan clocks and do not publish a failed finding or quarantine the application.
The public scan entry point compares parsed UUIDs, so compact MemStore IDs and
canonical stored identities refer to the same owner while cross-scope inputs
remain refused.

Portable pipeline tests use the real private store APIs, explicit two-drive
producer lineage and injected native views/Grype. They cover automatic
publication, main/sidecar policy, hidden component/base findings, closed
failed selections, scoped presence, restart reconstruction, pending snapshot
renewal, contention and cancellation. Real PostgreSQL tests cover retained
presence and owner scope. These checks do not prove actual native mounts,
Grype execution, KVM boot/restore/promotion, leakcheck or observed adoption.
Source-built/function base binding and publisher proof, protected native
whiteout conversion, complete logging/egress adoption, controlled rollout and
all-create-path onboarding E2E remain required. Public activation is disabled.

## Signed full-rootfs runtime-default base binding

A signed full-rootfs image conversion with retained OCI evidence now binds the
current platform base producer ID and immutable input hash. The application
still consumes its complete image chain from layer zero; its runtime-default
base is an independent drive, not a fabricated prefix of that image. The
scheduler and state validator share the canonical runtime/architecture base key.
The production OCI architecture remains linux/amd64.

Before publication, imaged checks the base's configured immutable source,
layout version, actual guest-init digest and freshly hashed complete stored
blob. Mutable development base references resolve to an immutable source before
comparison. It does not regenerate producer facts from a storage key, config
sidecar or legacy conversion. Platform base producers do not become company
publisher approvals through this binding; the application's existing current
company signature proof remains separately required.

Both stores check the exact current base selection under its existing key
fence, the persisted application runtime and distinct base/application keys.
Full-rootfs producers now carry that association through composed scan inputs,
immutable captures and native source envelopes. Database capture and raw native
publisher authority also check the persisted runtime's expected base key and
boot layout. Replacing a base selection prevents the old conversion from
renewing scans or obtaining fresh native authority; historical conversion and
scan clocks remain unchanged. Busy publication fences return without waiting.
Older records without a base association retain their original hashes and
receive no private native authority.

Portable tests exercise complete layer consumption and stored-byte checks with
injected mkfs/Grype and explicit native view fixtures. MemStore and real
PostgreSQL tests cover separate full-rootfs/default-base identity, source and
boot drift, scan/capture/grant publication, current-selection replacement,
owner-derived raw SQL authority and contention. Their native reports and
receipts are simulations; they neither establish physical consumed bytes nor
advance observed standards adoption. Actual native mounts, Grype, KVM
boot/restore/promotion and leakcheck remain required. Source-built/function
producer and approved publisher evidence, logging/egress adoption, controlled
rollout/recovery and all-create-path onboarding E2E remain required. Public
activation remains disabled.

## Approved source-build export publication

builderd can now use an explicitly configured build publisher via
`FAAS_BUILD_PUBLISHER_NAME` and `FAAS_BUILD_PUBLISHER_KEY`. The private key must
be a bounded owner-only PKCS#8 P256 file. No platform key is selected by default,
and configuration never enrolls a key in application or company trust. The
configured name is preferred when present; publisher selection verifies an
already approved application's current stored SPKI key. This also supports the
generated signer names installed by inherited standards. The final private
store check revalidates that name/key under its control fence.

The distinct canonical `gregale.build-export.v1` claim binds account,
organization, application, deployment, exact build claim/start time, verified
source SHA-256, complete opened OCI-export SHA-256 and byte count, application
runtime and builder node. It contains no source URL, local path, environment or
private key. An ASN.1 P256 signature authenticates the exact bounded canonical
JSON bytes; registry image and platform ext4 signature formats cannot substitute
for it. The opaque export digest covers all archive bytes, including padding.
Existing local OCI descriptor/DiffID validation remains separately required.

Both stores retain immutable private records with storage-issued verification
and expiry clocks, bounded to 24 hours. Exact ID retries retain their original
clock. One claim cannot be rebound to different export claims. Current owner,
source intent, runtime, build generation and approved key are checked again.
PostgreSQL uses nonwaiting parent/build and control/artifact fences; raw mutation
and live-owner deletion are refused. Its private insert guard is not database
cryptographic verification: the store authenticates the actual retained payload
and signature. Source-export evidence is erased with its deleted parent.

Publication happens before atomic build completion and notification on both
cold-build and cache-hit paths. A fresh read additionally requires matching
successful build provenance. An interrupted or cancelled completion therefore
leaves historical evidence without a fresh export approval. Cache reuse measures
the current complete export and signs the new build claim; it does not copy a
previous deployment's scope. Missing configuration for `require_signed` or
`security_policy=enforce` source builds, or an unapproved/revoked required key
at publication, prevents build
handoff. Later revocation invalidates fresh reads. Where signature policy
permits unsigned builds and the configured key has no current approval, the
existing build path remains available without publishing an approval record.
This extends ADR-054's imported-image-only gate to source-build publication.

This is an export publication boundary. It does not publish source/function
rootfs producers, bind their runtime/default base or injected runner, supply a
composed runtime scan, authorize native boot/restore/promotion, or acknowledge
observed standards adoption. Revalidating policy changes made during a build
at the final artifact and consumer boundaries remains part of that integration.
Those consumer bindings, actual native
Grype/ext4/KVM/leakcheck, logging/egress adoption, controlled rollout/recovery
and all-create-path multiservice E2E remain required. Public activation is
disabled.

## Approved source-export consumption

The source branches of `snapshot_boot` (tarball, Dockerfile, GitHub and preview)
now consult private account/application/deployment publication history before
conversion. History is only a downgrade guard; it never grants trust. If any
history exists, the latest build must have a fresh scoped export approval with
current matching provenance, runtime, owner and approved publisher key. An
expired, revoked or replaced-build record cannot disappear into unsigned
legacy conversion, including when signature policy has subsequently relaxed.
An application requiring signatures or enforcing security policy also refuses
conversion without publication history. An optional unsigned application with
no retained history keeps its existing conversion path.

The consumer opens a regular non-symlink export without waiting on a FIFO. It
copies and hashes the same bounded stream, including tar padding, into a private
0700 directory and 0400 archive. Size and complete digest must match the current
approved claim before any OCI parser or rootfs converter consumes that copy.
Descriptor and DiffID validation remains required; a signed opaque archive does
not bypass those checks. Replacing or modifying the original export after the
copy cannot change the consumed layer bytes. Normal success and refusal remove
the private copy; crash cleanup remains part of consumer recovery integration.

Approved function conversion requires the produced runtime staging path, which
consumes the verified OCI artifact and avoids reapplying unsigned source bytes.
The hermetic legacy function seam cannot consume an approved export. After
conversion, the consumer reloads the application and fresh approval; changed
approval or a newly required signature blocks this handler's snapshot handoff.
Approved conversion now publishes its distinct source rootfs record and deployment
metadata atomically under the store fences described below. The post-conversion
recheck remains additional refusal at this handler boundary; it does not protect
a later scan/handoff or authorize native boot, restore or promotion. Optional
unsigned legacy conversion retains its existing rootfs stamp.

The source/function producer now binds the selected independent runtime/default
base, exact injected runner and guest-init, conversion inputs and retained export
approval as described below. Fresh exact-claim renewal after archive cleanup and
distinct source composed-scan publication are now wired. Native consumption and
observed standards adoption remain required.
The transient export copy does not flatten the shared read-only base, private
writable main or independent read-only sidecar drives. Public activation remains
disabled.

Consumer tests use authentic company signatures and actual OCI/layer streams.
They cover all four source kinds, original-export replacement, revoked or
expired approval, a different latest build, owner scope, signature policy
changes during conversion and signed malformed OCI refusal. MemStore and real
PostgreSQL tests preserve scoped history after revocation and erase it with its
parent. The Go executable payload, injected runner digest and ext4 result are
fixtures; these checks do not establish native execution or observed adoption.

## Atomic source/function rootfs publication

Approved source conversion passes an explicit immutable binding into both the
local OCI and function converters. It captures the exact scoped export approval,
current independent amd64 runtime/default base producer and hash, and a canonical
hash of the app/deployment conversion inputs before conversion. The intent hash
covers slug, runtime/type, start command, complete lifecycle manifest, signature
policy, handler, scope, and deployment overrides. Equivalent JSONB object ordering
and omitted empty command/dependency defaults hash identically; nil and explicit
empty listener declarations remain distinct. Plaintext environment and secret
references are not retained in the producer or exposed as a conversion result.

The normal rootfs builder returns the digest of the exact guest-init byte slice
it injects, alongside the existing injected runner and complete stored ext4
identity. Replacing the source guest-init file later cannot change that returned
digest. The consumer rereads the produced storage object and selected base bytes,
checks current base configuration and guest-init identity, and then invokes the
private publication store. It never substitutes a registry verification for a
source export approval.

Both stores revalidate current owner, source/build claim, completed provenance,
approved publisher key, deployment stage, unchanged conversion intent and current
base selection. PostgreSQL uses nonwaiting approval/app/deployment/build, control,
artifact and base fences. All existing build rows are locked, including queued
claims, and the deployment's selected build must still match the approved build.
Deployment locking prevents newly inserted build children from bypassing this
check. The SQL lock is a private owner fence; the Go store authenticates the actual
retained P256 payload and signature against the current key.

An immutable source producer, its current selection and deployment rootfs metadata
are written in one transaction. Storage issues the publication clock; expiry is
bounded by the retained export approval. Exact current ID retries preserve those
clocks; changed same-ID inputs or superseded selections cannot be reselected.
Failed stamps roll back producer and selection. Scoped historical reads check
owner, source/runtime and stored deployment metadata without granting fresh trust
after revocation. Live-owner raw mutation and deletion are refused. Parent erasure
is permitted only after the app is deleted, its grace deadline has elapsed and
purge has been claimed, or after the deployment has gone; this supports Gregale's
build-before-deployment purge order without weakening live-owner retention.

Source producer history participates in the runtime-presence downgrade guard.
Fresh scanner bootstrap uses the distinct source approval path described below.
Native capture retains distinct source producer identity; its authority checks
are described below. Missing or stale source selection cannot be treated as absent
or fall back to unsigned handling. Conversion and scanner bootstrap do not grant scan, native
boot/restore/promotion, logging/egress adoption, or rollout acknowledgement. The
shared read-only base, private writable main, and independent read-only sidecars
remain separate drives. All native Grype/ext4/KVM/leakcheck and full multiservice
rollout/recovery acceptance remain pending. Public activation stays disabled.

The private checkpoint has an unresolved merge-policy issue: the two applied
source-rootfs migration IDs end in `000` milliseconds, which the current
`scripts/ci/check_migration_version_hygiene.sh` PR gate refuses. Their applied
sources remain frozen under the append-only rule. This checkpoint is not
merge-ready; a reviewed resolution of that naming-policy conflict is required
before a PR, without silently rewriting applied history or weakening default
missing-ledger duplicate-CREATE refusal.


## Distinct source runtime scanner bootstrap and renewal

Source/function producers now enter the existing composed-runtime scan pipeline
with their original `source-app-layer` or `function-layer` kind, exact producer
ID/hash, complete stored ext4 digest/size, and independent current base ID/hash.
They are never converted into registry rootfs/signature objects. The source kinds
use the app overlay physical layout while remaining distinct in the artifact and
source-set hashes. Native materialization reserves its overlay mount before any
storage read, then composes the separate base and app drives; sidecars retain
independent guest roots.

Both stores recheck scoped current rootfs metadata, unchanged conversion intent,
the exact selected/latest build claim, completed provenance, the latest retained
publication for that claim, its actual P256 signature under the current approved
publisher key, and the captured current runtime/default base. The latest proof
must carry exactly the original claims, including export digest/bytes, source,
runtime, builder identity and claim start. An expired or invalid latest proof
never falls back to an older approval. PostgreSQL locks the source pointer,
producer, original publication, app/deployment and all build rows without waiting;
its deployment lock also fences new build children. It reads sidecar membership
after taking source owner locks, before collecting the complete producer set.
Current publisher/control, artifact-child and base fences remain required.

A scoped historical publication reader retains signed claims without granting
fresh trust. Imaged can submit these exact retained bytes for new cryptographic
verification after the original archive has been removed. That verification
issues a new storage-clock approval, never extends an old row, and fails after
publisher revocation or key replacement until an authentic new proof under the
current key is recorded. An approved new key can verify the same immutable export
claims while preserving the source producer identity. The original conversion,
publication ID and expiry remain unchanged; fresh bootstrap expiry follows the
current exact-claim proof. No export archive is reopened to renew the proof.

The composed scan reader, post-scanner checks and durable scan publisher now
accept these distinct source inputs. A changed intent, selected build, rootfs
metadata, publisher or base refuses current facts and late publication. Retained
history still prevents unsigned fallback. A published composed report does not
create a component scan, native grant, consumed-byte acknowledgement, observed
adoption or rollout-wave completion. Native capture and fresh authority checks
are described below. Actual native Grype/ext4/KVM/leakcheck, source boot/restore/promotion,
consumer crash recovery and multiservice rollout acceptance remain pending.
Portable scan tests inject the materializer and scanner and do not establish
native execution. Public activation remains disabled.

## Distinct source native capture and authority

Native captures now retain `source-app-layer` and `function-layer` producers with
the selected base and registry sidecars. Historical capture checks exact owner,
original build/export lineage, selected build generation, deployment metadata,
runtime kind and current base binding. It does not renew a signature or scan.
Current settings remain captured separately; conversion intent is revalidated
when fresh composed scan evidence is read for native authority.

Boot grants and uncommitted runtime publication require the current composed scan
and latest exact-claim build export approval. SQL takes nonwaiting owner, build,
producer, publisher and base fences and bounds the authority deadline. Go
authenticates retained ECDSA bytes and checks conversion intent in that same
transaction before committing authority. Renewal preserves immutable producer and
capture identity. An expired approval, changed build, command, metadata, base or
publisher refuses pending authority. Source history never enables legacy fallback.

Source native authority requires protocol 2 in both Go and SQL; a registered
consumer offering protocol 1 cannot receive a source boot grant. Protocol 2
binds the distinct source set to consumption receipts and preserves a
read-only base, private writable main and independent read-only sidecars. Portable
store tests use explicitly simulated receipts; they do not prove native byte
consumption or advance observed policy adoption. Dedicated Linux amd64/root/KVM
boot, snapshot restore and promotion, actual Grype/ext4 scanning, crash recovery,
leakcheck and the full controlled multiservice rollout remain acceptance work.

## Durable image preparation integration

Upstream's durable image-preparation path now consumes approved source exports
through the same verified conversion path as legacy handoffs. Main-layer
publication waits until assembly finishes. A private store operation publishes
the exact source or registry producer, its selection, deployment rootfs metadata
and the `layer_published` checkpoint under one memory lock or PostgreSQL
transaction. Replaced worker claims, cancelled deployments and revoked publisher
keys refuse publication. A failed transaction leaves no selected producer or
rootfs stamp. Recovery retains the original input and resumes from the completed
checkpoint without republishing the conversion.

Snapshot operations retain both the standards grant checks and upstream's
destruction-joined instance flights. Teardown cancels and joins the standards
flight before releasing native resources. Snapshot backing-image checks remain
in place; verified source requests still cold-boot until their restore path has
native lineage acceptance. These portable checks do not supply physical consumer
acknowledgments or enable public standards activation.


## Inherited publisher identity

An organization publisher is materialized under `standard-<resource UUID>`.
Signed build exports and registry proofs resolve current approved application and
account keys by canonical SPKI DER fingerprint. Display labels do not choose the
key. The private stores still authenticate the actual P256 signature and retain
owner, control, build-claim and artifact fences. Rootfs conversion, scan publication
and signature renewal use the same identity without renewing retained clocks.

Portable onboarding covers company assignment before source-app, function,
registry-main and sidecar creation. Forged signatures and approval in another
account cannot substitute for a revoked scoped key. This does not establish
composed bytes, runtime ACKs or native acceptance; public activation stays disabled.


## Prospective artifact security reviews

Security-changing previews read the selected typed source/registry producers,
current authenticated publisher approvals and the complete selected composed
scan under existing owner, control, artifact and base fences. Source exports
remain distinct from registry verifications. Prospective publisher resources
are matched by canonical key fingerprint, including verified private legacy
backups; labels confer no authority. Enforce mode uses the same composed-report
severity gate as native admission. Missing or stale evidence, unapproved
publishers and blocking findings remain explicit application blockers.

The approval hash includes exact producer, publisher and scan identities, and
the preview expiry cannot outlive their verification lease. Approval recomputes
these inputs in its transaction; a newly selected scan invalidates old review
authority. This permits compatible policy materialization without manufacturing
source component scans, consumed-byte receipts or observed rollout adoption.
Native scanner, VM and rollout acceptance remain pending; public activation is
disabled.

## Private operator pause, resume and abort (2026-10-04)

The internal operation control interface requires active organization owner/admin
membership and an exact persisted operation timestamp. PostgreSQL locks current
organization, actor and membership authority and the operation with bounded
NOWAIT retries. MemStore makes the same decision under its mutex. A command
atomically changes state, increments the worker generation, clears the lease and
appends an audit event without rewriting approved intent. Stale or unauthorized
commands and audit failures commit no intent or lease changes.

Pause retains target checkpoints and admission pointers. Resume recomputes the
current eligible wave from those checkpoints and requires a new worker claim;
neither command establishes consumer observation. Old claims remain invalid even
after resume. Paused queued targets still exclude automatic enrollment workers.
Abort ends an active operation as `failed` with `operator_aborted`, marks queued
or applying targets `skipped`, and retains persisted/observed/blocked target facts.
It preserves the approved new-service admission version and installed controls.
The aborted history is immutable and cannot be resumed. A current-state retry of
pause or abort is a no-op; a retry with an obsolete timestamp is stale.

Rollback is a newly reviewed assignment change to an earlier version or disabled
admission. It binds current target membership, controls, artifact approvals,
exceptions and quotas; the forward approval is not rollback authority. A partial
forward operation must first end before a replacement operation can be approved.
Portable MemStore and PostgreSQL tests exercise a partial abort followed by a fresh
reviewed rollback, while leaving the rollback waiting for real consumer ACKs.
Public operator routes and native multi-service rollback acceptance remain gated.

## Private permitted local intent (2026-10-04)

Local choices use a complete strict settings object and a separate additional-log
destination set, with a desired-revision compare-and-swap. Active owners, admins
and developers can select only inherited fields; current app-owner entitlements,
resource ownership and every inherited constraint remain enforced. Empty local
choices restore inheritance. Required log references cannot also be registered as
extras, preventing a required destination rotation from retaining the old one.

Both stores serialize authority, current controls, artifact evidence and local
intent under their existing review fences. An active reviewed operation excludes
local mutations. A changed request atomically increments desired revision,
revokes enrollment leases and audits hashes, preserving the installed projection
and actual observation until materialization. Stale, refused and audit-failed
requests leave all those facts unchanged; identical permitted requests are no-ops.

Automatic resolution now applies the prospective artifact security gate as well
as owner, inheritance, resource and quota checks. Source/function approvals remain
typed independently from registry proofs. Portable tests cover authentic current
publishers, selected composed scans, unsafe rescans both before the request and
before worker installation, concurrent local writes, audit rollback and bounded
database contention. Installing a security change can invalidate a source
conversion's old intent; native admission still requires current producer and scan
evidence. These tests manufacture no observed revision or consumer ACK. Public
override routes, exceptions and native end-to-end acceptance remain incomplete.

## Read-only application enrollment view (2026-10-04)

Organization members can inspect live application enrollment through an
organization-scoped GET endpoint, the Go SDK and CLI. Existing read-scope,
membership, account and session MFA gates apply. Current persisted application
ownership is checked before the scoped enrollment reader; foreign and deleted
applications return 404.

The response separates saved local choices and desired revision from the last
installed effective settings, their provenance and persisted revision. Installed
settings are omitted before first installation. Observed revision remains actual
consumer evidence; this read supplies no new authority or acknowledgment. Original
private settings, destination URLs, credentials, control backups and worker leases
are not response fields. Public review/approval, assignment and override mutations,
exception lifecycle and native rollout acceptance remain gated.


## Private exception lifecycle and expiry authority (2026-10-04)

Private memory and PostgreSQL stores now approve application exceptions against
an exact desired revision, captured standard version and inherited field. A
current active owner or admin must approve. Replacement values pass the shared
resolver, including independent inherited requirements, current resource
ownership, platform restrictions, account quotas and artifact security checks.
Approvals retain their reason, approving identity and bounded expiry; their
approved content cannot be edited. Revocation is one-way and retains that history.
Approval and revocation atomically queue new desired intent, revoke worker claims
and append audit hashes without recording configuration values or reason text.

Review snapshots include active approvals and bind their content in the review
hash. A contributing exception also caps preview validity. Materialization stores
the earliest contributing expiry in the installed enrollment. Runtime captures
include that deadline only when present, preserving the input hashes of existing
captures without exceptions. Admission refuses at the exact expiry independently
of the repair worker. Native boot, promotion and snapshot authority share the
exception deadline ceiling with artifact approval deadlines; SQL grant guards
apply the same ceiling to raw grant writes. This is storage authority, not proof
that native consumers converged or that running services were repaired.

Each automatic worker pass queues a bounded batch of expired installed revisions,
with an atomic audit and generation fence, before acquiring a new repair lease.
Repair resolves the captured adoption pins without the expired approval. A saved
local choice that now violates an ordinary inherited requirement leaves the app
blocked; corrective local intent can be saved from that blocked state after all
current resolver, ownership, quota and artifact checks. Repair retains history and
never advances observed revision or acknowledges a rollout wave.

Public exception and other activation mutations remain unavailable. Dedicated
Linux amd64 root/KVM scanner, consumed-byte, restore/promotion and consumer ACK
acceptance remains required before public activation.


## Public preview and inspection checkpoint (2026-10-04)

Public routes now save a non-activating assignment preview and inspect saved
reviews, operation targets, live application enrollment and historical exception
approval/revocation. Preview requires write scope, an active owner/admin and
completed session MFA; reads require read scope and current organization
membership. History does not establish fresh approval authority. Explicit
`active`, `expected_revision`, version and batch size are required in review JSON;
duplicate fields, null scalars, unknown fields and trailing JSON are refused.

Public DTOs omit account attribution, original base settings, artifact bodies,
private binding backups and leases. The installed projection's contributing
exception deadline is visible separately from server-timed history status.
Revocation takes precedence over expiry; an active historical approval need not
contribute to the current adoption. Reads never manufacture an observed revision.

The Go leaf module, generated Node/Python clients and CLI share these routes.
The published Go SDK wire vocabulary has a contract check against the daemon;
the Node package exports its generated organization service. Both generators
produced identical trees across two runs. Portable checks cover non-activation,
current role/scope/MFA boundaries, foreign/deleted applications, desired versus
persisted/observed progress, exception history and deadline visibility. These
checks do not establish native execution or real consumer acknowledgements.
Public approval, assignment/local/exception/operator mutations, complete
multi-service onboarding and recovery, and dedicated Linux amd64 root/KVM
scanner/boot/restore/promotion/leakcheck acceptance remain required.


## Private logging inventory consumer checkpoint

Gateway delivery receipts are separate from complete loaded configuration.
A single store snapshot returns enabled sender tuples and per-application
inventories, including legacy additions and empty inventories after removal.
Inventories bind tenant ownership, desired/persisted revision, effective hash
and sorted complete sender fingerprints. The gateway acknowledges only after
all expected senders have unsealed credentials and started, and all obsolete
sender/stream loops have joined. Quiet services do not need synthetic log events.

Facts are node-scoped and tied to a daemon startup UUID and monotonically
increasing generation. Retained immutable session history prevents an old
registration retry from reactivating a superseded startup. Registration retries
for the current startup preserve its generation. A new startup invalidates the
previous generation's current facts. The gateway requires its actual configured
compute-node identity; no application placement or synthetic node supplies it.
Unknown or empty node identity remains pending. Temporary node unavailability
suppresses current facts without replacing the session.

PostgreSQL rechecks the complete inventory under the existing nonwaiting
exclusive application-controls fence, which covers phantom drain insertion and
removal, plus nonwaiting parent/session row locks. Trigger guards check exact
current tuples, retain session lineage and own observation timestamps. MemStore
implements the same current-fact rules. Current reads reject changed or expired
projections, unavailable nodes and facts older than the centralized 90-second
freshness bound. A periodic two-second bounded pass refreshes ready application
facts, rotating its cursor to avoid starvation. No request-path inheritance or
per-log-line inventory write is added.

A process OS lock on the spool root protects durable queue ownership until all
worker loops join. Database session fencing alone cannot protect a local queue
while an old process still writes it. Operators must stop pre-protocol gateways
before introducing this protocol; those processes cannot provide inventory
acknowledgments. The checkpoint still establishes no fleet membership roster,
whole-application observed revision, wave release, provider success for a quiet
service, or native runtime acceptance. Public activation remains disabled until
those broader capability and recovery gates pass.


## Release-gated local and exception HTTP contract (2026-10-04)

The existing private atomic intent and exception operations now have typed HTTP,
Go/Node/Python SDK and CLI entry points. PUT local intent replaces the complete
settings object and additional destination set. POST approval records an immutable
one-field exception against an adopted version, reason and bounded expiry. POST
revocation preserves approval history and queues ordinary inheritance repair.
All require the current desired revision and preserve installed/observed facts.

Local choices use a dedicated organization action for owners, admins and
developers; approvals and revocations use the owner/admin approval action. Scope,
MFA, current role and live application ownership are checked before idempotency
lookup, with store authority checked again during the atomic mutation. Strict
request DTOs reject missing required values, nulls, duplicate or unknown keys,
unsupported fields and invalid value types. Public errors redact private details.

`FAAS_APPLICATION_STANDARD_MUTATIONS_ENABLED=1` is an explicit boot-time release
opt-in, following the existing execution/app-task gates. The default is false;
all other literals keep it disabled. Deployment must leave it unset until the
already-required consumer convergence, controlled rollout/rollback/recovery and
dedicated Linux amd64 root/KVM acceptance are complete. The gate wraps idempotency
replay as well as execution. No assignment approval/operator route is added by
this checkpoint, and no deployment flag is activated.

Portable HTTP/store lifecycle, role, revision contention, gate/replay, strict
request and SDK/CLI checks establish this contract only. Saving local choices,
approving or revoking an exception, or installing its projection does not create
consumer acknowledgments, advance observed revision or release a rollout wave.
All-consumer convergence, multiservice onboarding/rollback and daemon recovery,
source composition/scanner/native cold boot/restore/promotion, live egress restart
and leakcheck acceptance remain incomplete. Public activation stays disabled.


### Release-gated public reviewed approval and operator controls

The API and all three SDKs plus CLI expose the existing atomic saved-review
approval and operation pause/resume/abort stores behind the same default-off
`FAAS_APPLICATION_STANDARD_MUTATIONS_ENABLED` gate. Approval names the exact
saved SHA-256 plan; controls compare the exact current `updated_at` token with
microsecond precision. Current owner/admin action, scoped resource and gate checks
precede idempotency replay; the stores recheck authority under mutation fences.

Abort retains installed facts and skips outstanding targets. Rollback and
assignment deactivation use new ordinary assignment previews and approvals,
including current restrictions and an expected assignment revision. Historical
operations are retained. The two-application API workflow demonstrates partial
installation and reviewed rollback without fabricating consumer observations.
This contract does not enable the gate or satisfy consumer, wave/recovery or
native acceptance. Assignment inventory and full operational acceptance remain
part of the unfinished product scope.


### Retained assignment inventory and revision reads

Add organization-scoped GET/list views of assignment metadata, including inactive
rows, to the API, Go/Node/Python SDKs and CLI. Existing active-only admission and
resolver reads retain their meaning. Inventory includes the current admission
version and assignment revision, original creator and timestamps; updates and
rollback must use that revision in an ordinary reviewed preview/approval.

Both stores validate nonzero UUIDs, canonicalize legacy UUID spellings and return
ascending UUID pages with a bounded sentinel for continuation. Foreign records
are not disclosed. Current membership, read scope and session MFA protect the
HTTP views. The default-off mutation gate does not disable these reads. This
requires SQLC reads of existing columns and no migration or schema rewrite.

Portable lifecycle and paging tests exercise retained deactivation, stale
revisions, new-service admission and stable existing adoption pins. Inventory
reads do not acknowledge consumers, release waves or satisfy fleet/native
acceptance. Public activation remains disabled pending the full acceptance gates.


### Native startup history and delayed-registration fencing

Retain each registered vmmd incarnation and its admission protocol for the
lifetime of its compute-node row. An exact current identity retry is idempotent;
a new unseen incarnation replaces the current identity. Once superseded, an
incarnation cannot be registered again, and its protocol cannot change while it
remains current. A protocol change requires a new native process identity.

An additive migration backfills currently recorded identities and captures new
registrations atomically with the node update. History is immutable, including
against direct SQL, and is erased only with the owning node. Node row locking
serializes concurrent registrations; memory storage applies equivalent checks
under its existing lock. Existing native stale-error mapping and SQLC registration
remain in place. Frozen migration bytes are preserved.

This prevents a delayed old registration from restoring authority to an old
boot grant or already-published runtime receipt. Portable memory/PostgreSQL
fixtures check those refusals, current retries, concurrent replacement, raw SQL
protection, backfill and node erasure. They simulate receipt storage and do not
prove native execution, fleet membership, runtime convergence or wave completion.
Release activation and the full recovery/native acceptance gates remain pending.


### Current-process live egress observations

Add a distinct private `UpdateAdmittedAppEgressPolicy` RPC for schedd's live
repair path. It names the expected native startup identity and carries the entire
CIDR/port projection and durable app egress revision. The native manager checks
its immutable identity before mutation, orders physical updates through the
existing per-app gate, and returns its own receipt only after both controls
succeed. Missing capability refuses; an echoed legacy revision supplies no
standard observation. The current native artifact protocol is 2; this additive
RPC does not reinterpret older protocol identities or change frozen boot hashes.

A receipt binds the node, incarnation, protocol, application, egress revision and
canonical projection SHA-256. CIDRs hash masked address bytes with their prefix
length; ports hash the complete sorted unique requested set, including declared
base ports. This avoids IPv6 rendering differences between Go and PostgreSQL and
refuses malformed/forbidden ports before narrowing a wire integer.

Retain private egress observations for each application/serving-node pair in both
stores. Capture the current desired standard revision/effective hash, policy and
native identity together. Nonwaiting SQL guards compare that entire captured
tuple with current intent and eligible live ownership, check the backend receipt,
and stamp the storage clock. Restart, pending/changed enrollment, expired
exceptions, inactive nodes and absent live instances invalidate reads and late
writes. Node/application erasure removes their facts. Repair runs at subscriber
startup, on policy notifications and each existing reconciliation tick. Freshness
and batch limits live in `pkg/api/limits.go`.

These are per-node consumer facts. They neither establish fleet membership nor
advance enrollment/operation observations, release subsequent waves, or prove
firewall behavior on native hardware. Logging, image-security/artifact consumers,
whole-application convergence, controlled waves and dedicated Linux amd64
root/KVM restart/tightening/reload/leakcheck acceptance remain release gates.
Public activation remains disabled. The initial applied egress migration is
preserved; an additive repair aligns its guard with native protocol 2.


### Historical reference number

This decision was originally numbered ADR-435 on the application-standards
branch. It is renumbered because the integrated preview-route-report decision
also uses 435. Earlier application-standards code comments and preserved migration
headers using ADR-435 refer to this decision; frozen history is not rewritten.

## Current-process logging health evidence

The first successful standard-bound delivery remains a historical private fact.
It cannot certify current provider health: the same sender can subsequently
retry, exhaust its queue, or observe missing source records. The legacy health
row is also insufficient because it is shared across nodes and is not bound to
the standard projection or gateway startup session.

Add a distinct private per-application, drain and gateway-node health fact. Its
binding includes desired revision, effective hash, organization-owned resource
identity/configuration digest and the exact physical sender digest. The existing
gateway startup UUID and monotonically fenced generation identify the reporter.
Sender callbacks report unknown/idle before delivery, healthy/delivered only
after real HTTP success and durable queue acknowledgment, and bounded degraded
reason codes for retries, failed delivery, queue faults, durable losses, source
gaps and unavailable source streams. Reports contain neither log content nor
provider errors, URLs or credentials. A successful retry can repair a transient
failure; a later delivery cannot repair records already lost by the same worker.
Durable dead-letter counts reestablish loss when a worker opens its queue.
A durable enqueue refusal retains the source cursor for retry and reports a
transient queue fault; the legacy dropped counter alone does not prove lost logs.

A worker retains one current event, rather than a report per log line. Events
increase when the outcome changes. Storage rejects an older event or a different
payload using the same event number within the same binding and startup session.
A new current binding or startup can begin its event sequence again. The bounded
signed event counter fails closed with a reporter-exhausted outcome. PostgreSQL
owns both clocks: periodic refresh advances reporter freshness while preserving
the last outcome-change time. A heartbeat is not another successful delivery.

The gateway refresh uses the existing two-second budget and a sorted rotating
worker cursor. The spool lease, current startup session and live worker are
required. Superseded gateways stop reporting. Memory and PostgreSQL recheck
current parents, application/project ownership, unexpired exceptions, enabled
sender configuration, resource binding, reporter session and node eligibility;
healthy events also require an existing source instance belonging to the app.
PostgreSQL guards raw writes with the nonwaiting controls fence and parent,
resource, reporter, fact and source locks. Private facts disappear from current
reads after 90 seconds without refresh, on source erasure for a healthy event,
or immediately when their binding/session becomes stale. Application, drain and
node erasure removes the retained facts.

These reports describe the last outcome observed by a current process. They do
not probe a quiet provider, assert its future availability, establish a fleet
roster, cover every instance or advance whole-application observation. Quiet
services retain unknown provider health and separate loaded-inventory evidence;
no synthetic customer log is emitted. Cross-application endpoint health, full
consumer convergence, rollout wave release, native KVM enforcement/recovery and
release activation remain separate acceptance requirements. The mutation gate
remains disabled.


## Authoritative consumer membership and joined logging shutdown

A list of successful private reports cannot establish the required consumer set.
Read an application roster from compute-node membership and all live/in-flight
instance placements in one PostgreSQL statement snapshot or one memory-store
critical section. Include every enrolled compute node, including inactive and retired nodes
without live placements or gateway addresses, before any logging process reports.
A placement, lifecycle or role label cannot establish logging quiescence. Keep
registered logging startups as obligations even after a node becomes control-plane;
a joined closure remains explicit evidence rather than deleting membership. Include native obligations for every live placement, even on inactive,
retired, missing or unexpectedly control-plane nodes. Placement states are the
scheduler's resident/in-flight set: waking, cold booting, running, snapshotting,
migrating, warm and draining. A drain flag does not prove physical quiescence.

The private roster reports current enrollment eligibility, the desired and
persisted revisions, effective digest, membership/lifecycle/role, gateway
configuration presence, heartbeat eligibility, logging startup session/closure,
native startup incarnation/protocol and instance/deployment placement identity.
It contains no endpoint, ciphertext, physical network identity or log content.
Missing processes and capabilities remain visible; pending or expired enrollment
remains readable but ineligible. Sorted membership/placement and process identity
can be fingerprinted independently of the read clock. Consumers of the roster
must re-read it before advancing state; its digest is not a lease or an observed
revision, and neither an empty roster nor a fresh heartbeat is convergence.

A logging startup can explicitly close after its source and sender workers have
both joined. The gateway prevents new worker starts once shutdown begins and
serializes joined shutdown, retaining its operating-system spool lease until the
bounded storage acknowledgment finishes. Storage owns the first shutdown clock;
exact-current retries preserve it, and current closure remains readable on an
inactive node. Closed startups cannot register again, write raw or typed loaded
inventory/health reports, or qualify retained reports as current evidence. A new
startup UUID advances the fenced generation and starts without its predecessor's
closure. Late shutdown from an old startup cannot close a replacement. Node
removal cascades private consumer history and closure.

Closure describes the joined logging process; it never removes a node or native
placement obligation. Unavailable storage cannot fabricate a shutdown. A crash
without joined acknowledgment remains unproven and earlier reports expire under
the existing freshness rule. Whole-application observation, physical recovery,
artifact/security coverage, rollout-wave release, native KVM acceptance and public
release activation remain separate requirements. The mutation gate stays off.

### Current native-instance qualification

Add a private scoped diagnostic that distinguishes a retained native boot receipt
from qualification against current application inputs. The caller supplies the
owning organization, application and live instance. Cross-scope, absent and
terminal instances return not found. In-flight boots, warm instances and draining
instances remain pending; this reader does not invent a receipt for a lifecycle
path that the native consumption protocol cannot acknowledge.

Qualification requires current persisted enrollment, an active compute node
with fresh heartbeat and registered artifact-consumption process identity, a matching
revision and effective hash, and the complete current runtime/control/producer
input projection. The receipt must acknowledge the captured artifact source set
and match current instance resources and process incarnation. Fresh selected
publisher approval and a composed scan are checked independently of immutable
receipt history, including enforcement findings and bounded exception expiry.
Replacing a selected scan with failure, expiring approval, changing inputs,
losing native capability or restarting the process prevents qualification.
Renewing approval of the same immutable producers need not replace the receipt.

MemStore evaluates the diagnostic under its mutex. PgStore uses one repeatable
read transaction, the existing nonwaiting parent/control/artifact fences, and
SQLC reads. The output contains opaque identity, revision, roster fingerprint,
status, stable pending reason and approval deadline; it contains no grant token,
artifact storage key, jail identity, physical network coordinates or credentials.
The snapshot is a diagnostic, not a lease: a future application observer must
revalidate all required consumers, retained artifacts, provider health, claims
and scope in its own fenced write transaction. This reader does not advance
observed_revision, release waves, issue native authority or prove native hardware
acceptance. Public activation remains disabled.

### Composition with current runtime configuration and native recovery

Native standards publication and runtime configuration input evidence share one
readiness commit. The scheduler preserves restore input provenance and rejects a
different wake or scope. PostgreSQL rolls back the native receipt and runtime
tuple when configuration publication fails; MemStore completes all refusal
checks before changing either receipt or the instance. Ordinary transitions
retain qualification reservation, standards, capacity and exclusive-owner guards.

Journal-backed native recovery precedes admission registration and allocation.
Legacy jail, clone and standards source sweeps remain disabled in journal mode:
durable instance state cannot retire a quarantined process's resources. A failed
process retirement keeps its sealed artifact sources and measured descriptors.
Native snapshot publication remains unavailable until its original producer and
publication session can be attested. These integration checks do not substitute
for dedicated native hardware and crash/recovery acceptance.

### Native admission after the last adopted standard is removed

An explicitly installed standard projection retains native admission after a
reviewed removal restores the application's baseline settings. A positive
persisted revision is durable installation history; empty adoption and managed
field sets do not turn this application into a never-enrolled legacy workload.
The scheduler still needs a fresh current input capture, current artifact
approval, measured native consumption and matching runtime publication. An old
grant cannot publish the replacement settings, and plan compatibility cannot
reinterpret a retained native runtime as legacy residency.

Scope changes retain the last persisted revision and projection while queuing
repair. The pending state prevents either the retained projection or a stale
runtime from admitting a boot. PostgreSQL forbids decreasing the persisted
revision, including a raw update that would erase installation history. Existing
captures, hashes and frozen migration bytes remain unchanged. A new additive
migration applies equivalent predicates to boot, publication, residency and
runtime comparison guards; its down path retains those fail-closed protections.
The retained-removal path additionally requires protocol 2 at grant issuance,
receipt publication and promotion through the shared native artifact guard.
The older registry-image protocol cannot certify restored settings. This
requirement is a second additive migration because the installation-history
migration had already been applied to a private database during verification.
Rollback must use a binary that understands retained native admission or keep
affected services pending until that capability is restored.

Portable store fixtures exercise reviewed removal, stale and unmeasured receipt
refusal, fresh measured publication, current native qualification and scope
repair. These simulated receipts establish storage behavior only. They do not
advance application observation, release a wave, prove physical recovery or
satisfy native KVM acceptance. Public mutation activation remains disabled.


### Reviewed observation and wave revalidation

The apid worker now qualifies each installed target before marking its enrollment
and operation target observed. A worker claim never counts as consumer evidence.
Qualification and checkpoint share one MemStore mutex or PostgreSQL transaction.
PostgreSQL locks authoritative node membership, process identities, application
parents, controls, report rows, placements, retained deployments and snapshot
catalog rows. Additive child guards cover inserts and moves; a shared membership
advisory fence covers new nodes and role changes. Nonwaiting acquisition returns
busy rather than certifying an incomplete read. A stale SQL artifact guard rolls
back a private savepoint while the outer application and membership fences remain.

Every open required logging session must load the exact current drain inventory.
Each current company destination needs current healthy delivery evidence from each
required logging consumer. Both report refresh time and actual delivery-event time
must be fresh; idle, degraded and periodically refreshed old success remain pending.
Closed joined logging sessions discharge only their logging process obligation.
Live placements still require current measured native protocol-2 consumption and
current revision-bound outbound-policy receipts. Every retained deployment needs
current producer/publisher approval and a composed scan under the current policy.
Usable snapshot cache needs complete measured capture history, matching current
inputs and an available original producer process. Legacy or stale cache cannot be
silently upgraded into observation. An empty roster or artifact inventory is pending.

The observer reports bounded reasons on the existing enrollment and operation
fields. Positive observation is capped by the earliest heartbeat, consumer report,
provider event, scan or exception deadline. PostgreSQL shortens the owning worker
lease to that deadline so a late wave checkpoint rolls back. Materializing a later
wave independently requalifies all previously observed targets in its own write
transaction; a persisted observed bit cannot release a new wave. Pause, abort,
expired or replaced worker generations prevent late observation writes.

This checkpoint implements reviewed-operation observation. Automatic onboarding
still materializes its enrollment; independent automatic observation reconciliation,
positive multi-application wave acceptance, scheduler-driven replacement and
snapshot re-prime acceptance, fleet recovery, named production
scope and native Linux amd64 KVM acceptance remain release work. Provider evidence
is deliberately conservative for idle services until a real delivery is available.
The public mutation gate remains disabled. Portable tests use explicitly simulated
native and delivery receipts and do not establish physical host enforcement.


The regression suite caught a reverse-parent locking change in legacy deployment
metadata and instance cleanup. A third additive migration replaces the observation
child guard's application-row lock with a dedicated shared advisory fence. Only an
observer acquires its exclusive counterpart; ordinary parent-row transactions
retain their prior cleanup behavior. The earlier applied migration files are
preserved. The existing artifact writer regression gate covers compatibility.

### Automatic observation reconciliation

Apid also checks installed enrollments after reviewed work and automatic
materialization. It reuses the generation-fenced enrollment lease, qualifies the
same current roster, logging, native and retained artifact evidence, and commits
only observation state and a bounded pending reason. It never changes standards,
adoptions, controls or desired revisions. Healthy refreshes retain the enrollment
update fingerprint. Private scheduling metadata rotates oldest checks first and
gives failed reads a cooldown; newly installed revisions bypass the cooldown.
Active reviewed targets, including persisted targets in paused operations, remain
owned by that operation. The observer locks the organization approval fence and
rechecks ownership before checkpointing, preventing approvals from racing it.

Current positive evidence advances automatic onboarding to observed. Gateway
restarts, added nodes, expired reports or native and artifact changes return it
to persisted with the current reason; new valid evidence restores observation.
This is periodic reconciliation rather than synchronous invalidation at every
evidence expiry. PostgreSQL checks the storage lease and positive evidence again
after all checkpoint triggers, so a slow write cannot renew authority. Apid
restart discovers durable enrollments and scheduling metadata without re-enrollment.
The migration adds reversible private scheduling columns; older applied migration
bytes remain frozen. Portable receipts do not replace native KVM acceptance,
service replacement, snapshot re-prime, named production scope or release gates.


### Durable runtime convergence after installation

Each installed enrollment now invalidates the application's snapshot cache
eligibility and enqueues a private revision/hash-bound `runtime_config_restart`
handoff in the same PostgreSQL transaction. A failed outbox insert rolls back
the controls, enrollment, cache invalidation and rollout checkpoint together.
The existing snapshot-stale trigger removes replica eligibility; source artifacts
and the old snapshot bytes remain available to their existing retention rules.
A lost LISTEN notification is recovered through the durable notification outbox.
This handoff adds no migration and changes no environment or secret fingerprint.
The private LISTEN grace is centralized in `pkg/api/limits.go`.

Schedd reuses the runtime configuration rolling replacement path. Its private
standard context adds captured revision/hash freshness and checks the current
installed enrollment before admission, withdrawal, destruction and acknowledgement.
Resident live deployments receive fresh capacity before stale serving instances
are drained and destroyed. Stale process memory is never snapshotted. A wholly
idle application stays cold; its next ordinary wake uses current inputs. Existing
environment receipt checks remain in force without inventing an environment
change timestamp for a standards-only update. Snapshotting and migrating
placements defer replacement until their lifecycle owner finishes.

Superseded handoffs are harmless. A request coalesced behind an older restart
rechecks captured inputs before acknowledgement. The scheduler resolves the
store's physical application identity before instance selection, including legacy
MemStore UUID spellings. An operator pause stops further replacement actions;
already admitted work can finish. Resume reuses ready replacement capacity.
Paused and busy lifecycle requests return a deferred outbox result, retaining
the attempt budget and a storage-clock retry boundary. Actual delivery failures
still use the existing bounded retry/dead-letter policy and require operator
recovery after exhaustion. Abort retains already installed intent, consistent
with the operation control contract; restoring an earlier projection requires
a separately reviewed rollback.

A scheduler acknowledgement proves this private replacement handoff completed,
not whole-application observation. Logging, provider delivery, egress, native
process identity, composed scans and retained artifact evidence still qualify
independently before subsequent rollout waves. Portable scheduler tests use an
explicit legacy native consumer simulation; the composed portable scenario below
also exercises protocol-2 contracts with simulated measured receipts. Neither
establishes physical host enforcement. Native composed multi-app wave acceptance,
snapshot re-prime acceptance, fleet crash/recovery/rollback, named production
environments, full migration recovery and dedicated native
Linux amd64 KVM/test-metal/leakcheck acceptance remain open release gates.
Public standards activation remains disabled.

### Composed portable wave acceptance

The scheduler suite now exercises three services automatically enrolled under
one organization standard with mandatory company logging, an approved publisher,
signed images, enforced scan findings and egress restrictions. The fixture uses
real ECDSA signature verification and consumption of decoded OCI layer streams,
one shared base producer and one stable protocol-2 native process registration.
Scanner tree facts, native drive consumption and provider delivery reports are
explicit simulations; these tests do not mount ext4 artifacts, run a scanner,
send logs to a provider or enforce a physical firewall.

Both MemStore and PostgreSQL run the actual scheduler admission and replacement
paths. PostgreSQL additionally consumes the installed revision's real durable
outbox row through the scheduler handler and strict lease settlement. A replay
does not admit a second replacement or spend another attempt. The fixture changes
only queue replay eligibility to avoid waiting for the LISTEN grace interval;
native authority leases, evidence timestamps and qualification clocks remain
under their existing contracts.

A reviewed destination and egress update installs one service per wave. Persisting
settings or acknowledging replacement cannot advance application observation.
Current inventory, delivered provider health, egress, native receipts and composed
scan approval qualify each installed service before the next wave. Restarting the
logging consumer after the first observed checkpoint blocks the second wave. A
newer failed composed scan after the second checkpoint blocks the third wave.
Fresh evidence must pass the owning observer before either wave resumes; a saved
observed bit or a successful historical scan supplies no permission.

A separately reviewed rollback selects version 1 at a new desired revision and
repeats replacement, consumer qualification and wave advancement. Assertions inspect
the actual controls, captured inputs, stored native receipts, adoption version and
observed revision of every service after the update and rollback. This closes the
positive composed portable wave scenario while preserving the independent snapshot
re-prime, physical native/fleet recovery, named production scope, onboarding coverage,
migration recovery and public release gates.

### Composed portable cache rebuilding and delayed publication

The portable snapshot scenario joins the actual scheduler park path, durable
capture grant and acknowledgment catalog, reference-only notification, imaged
publication owner, and current-input publication fence in both stores. PostgreSQL
also settles snapshot publication and runtime handoff through the real durable
notification queue; fixture SQL advances only queue replay eligibility.

Three inherited services first park and publish measured cache references. A
reviewed one-service wave invalidates only its service's old cache. Replaying the
old publication must not make it eligible again: publication compares the
catalog's immutable inputs with current standard inputs under the same app/source
locks used by the runtime-config fence. Historical catalog records remain intact.
An idle runtime handoff starts no VM. The next ordinary wake boots with the current
standard, and its next park captures a new token that imaged publishes. A restarted
scheduler can select this current cache, and current consumer facts qualify the
first wave while later services remain queued.

This scenario also pins an outstanding capability gap. The protocol-2 native
source consumer currently forces verified cold boot when a snapshot request
includes approved artifact sources, and rejects paused restore. The composed fake
matches that behavior: it does not claim that snapshot RAM ran or that warm
promotion succeeded. Portable capture, cache publication, selection and cold
fallback are verified; verified snapshot restore and promotion still require a
complete native implementation and physical acceptance. Native receipt facts,
capture bytes, scanner results and provider delivery remain explicit simulations.

The current capture store also excludes promoted warm parents. Once measured
paused restore is supported, capture must use the actual serving promotion receipt
and preserve its parent linkage before that lifecycle can qualify. Neither guard
is removed by this cache-publication repair. Named production scope, remaining
onboarding adapters, native/fleet recovery and operational release gates stay open.
### Verified snapshot input preparation

Measured restore now has a native preparation component, without enabling the
restore capability. `SnapshotCapture.CheckRestoreInputs` requires a fresh
protocol-2 binding with a new token and instance, exact account/application/
deployment, desired standard revision/effective hash, egress revision and
approved source set. It also requires the selected capture keys and requested
memory size. A historical parent's expired grant remains historical evidence;
only the fresh binding is validated against the current clock. A target node's
wrapped input hash may differ from the source node's hash. The durable admission
transaction must separately compare the catalog's stable captured inputs with
current application intent before issuing that binding.

`JailerVMM.PrepareSnapshotRestoreInputs` validates the capture and exact drive
layout/membership before touching storage. It seals full memory, VM-state and
captured private-drive streams, alongside the currently approved producer
streams, in the existing native source cache. Re-reading the sealed files checks
their measured identities. The original main producer remains a separately
measured fact; the captured writable drive never replaces its approval. The
drive handoff pins the captured main bytes before injection and retains the
approved producer identity when measuring the final injected bytes. Shared
immutable base/snapshot inputs remain shared; each staged main drive remains a
separate writable inode. Teardown cancels and joins a blocked preparation before
releasing its protected files. Expiry during download discards the result.

Portable tests exercise complete-stream corruption, truncation/overflow,
read/close failure at each selected input, stale policy/scope/source/layout,
owner reuse, cancellation, expiry, sidecar membership, independent writable
copies and producer/capture/injected distinctions. Their historical boot parent
is explicitly simulated. Files and stream verification are real portable I/O;
they do not certify native process consumption or restored RAM.

Native snapshot loading from these protected inputs, snapshot-consumption receipts,
paused restoration, and capture after serving promotion remain implementation
work. Existing measured restore/promotion refusals and verified cold fallback
remain active. Kernel/base backing verification, Firecracker compatibility,
post-restore entropy/clock hooks, unique native leases, dedicated Linux amd64
root/KVM test-metal/leakcheck, fleet recovery and release acceptance remain
required. Public standard activation remains disabled.

### Fresh catalog authority and hashed restore envelope

A private versioned restore envelope now carries the immutable catalog capture,
capture token and Firecracker version. Its deterministic evidence hash is a
coupled pair with the capture token in the fresh boot binding. The complete
envelope also participates in the existing boot payload hash. Unknown nested
wire fields, missing or mismatched evidence, a cold request carrying restore
evidence, changed artifact sources, RAM, keys or Firecracker version are refused.
The native identity has a separate restore capability version; zero preserves
the existing unavailable capability rather than implying support from protocol 2.

Both stores check the published catalog when issuing fresh boot authority and
again at final readiness publication. The catalog must have an acknowledged,
non-stale cache row outside GC. Its stable captured application inputs must match
the current locked target inputs, including reservation RAM and mode. The target
has its own instance, token, node and process identity. Memory byte counts must
match that target's RAM; caller-supplied evidence hashes cannot substitute for
the stored capture. Historical source cleanup and a new target node do not
rewrite or renew the parent grant. PostgreSQL locks the capture and cache rows
with shared NOWAIT locks inside the existing native-input transaction; contention
cannot hold parent locks indefinitely. No schema or frozen migration changes are
part of this checkpoint.

Portable authority tests use real MemStore and PostgreSQL state transitions and
explicitly simulated captured bytes and cold fallback receipts. The RPC and
direct native boundaries continue to refuse restore-bound grants before any
allocation. Native loading, measured snapshot consumption, paused restore and
promotion are still implementation work. Additive raw-database enforcement of
the new restore binding, named environment scopes, remaining onboarding paths,
native/fleet recovery and the operational release gates remain required before
public activation. This checkpoint does not certify restored RAM or KVM execution.

### Protected private native snapshot loading

`JailerVMM.RestoreSnapshotVerified` now connects catalog preparation to the actual
restore staging and `/snapshot/load` code. It requires the fresh binding's exact
capture token and evidence hash before reading storage. The opaque preparation
belongs to one exact native lease and one cancellable load flight. Its spec is
derived from cloned runtime inputs; changing the lease, selected keys, injection
inputs, readiness settings or pause flag cannot reuse that preparation. A second
restore/load attempt is refused. Noncanonical custom drive IDs remain unavailable
until captured backing names can be represented and checked explicitly.

Memory, VM-state, captured private drive, base and sidecars use the retained
verified files. Only the platform kernel follows the existing resolver and
kernel/backing qualification remains the caller's responsibility. Protected
sidecar names reproduce `provisionForOwner`'s cold-boot names, including its
current indexing. The main drive is copied separately for each lease. Its
approved producer and captured bytes remain distinct from the freshly injected
bytes. Every drive is pinned before injection; final drive identity is measured
before load. Memory and VM-state descriptors retain their measured inodes and
digests and are checked around the exact load API command. The command hash
represents that delivered command, not an undelivered cold-boot configuration.

An API acknowledgment records only private loader acceptance. It cannot certify
consumed RAM. The existing native drive-handle observer runs after protected
restore readiness (or after a deliberately paused load); it does not invent a
memory mapping or persistent VM-state descriptor. Kill first cancels and joins
the protected flight. Failed restore cleanup runs after that flight finishes,
avoiding self-joining teardown. Protected descriptors and files stay owned until
retirement. Existing entropy/clock hooks, native journal ownership, cgroups,
network isolation and kernel checks remain required.

Portable tests exercise actual provisioning, inode sharing/isolation, immutable
input replacement/corruption, private injection, command hashes, stale authority,
owner and command changes, replay, API refusal and cancellation during loading.
Their historical receipt and HTTP acknowledgment are simulated; they have no
Firecracker process or consumed RAM. Measured snapshot consumption receipts,
scheduler/Manager/RPC forwarding, advertised restore capability, paused promotion
and capture after promotion remain implementation work. Dedicated Linux amd64
root/KVM test-metal/leakcheck, named environment
scopes, all onboarding paths, fleet recovery and complete release gates remain
required. Public standard activation and restore capability remain disabled.

### Raw database restore authority

Additive database guards now require the same immutable catalog authority as the
Go stores at boot grant issuance, receipt acknowledgment and first runtime
publication. The capture token and evidence hash must be a coupled protocol-2
pair. PostgreSQL selects an acknowledged capture in the exact account,
application and deployment scope and a usable, nonstale cache that is not queued
for collection. Both rows are held with shared NOWAIT locks through publication;
competing writers receive the existing bounded busy refusal.

The evidence hash retains its existing deterministic protobuf contract and
domain prefix. A private fixed-message encoder in PostgreSQL reconstructs that
wire representation from immutable catalog JSON; it does not introduce a new
JSON hash or trust the caller's reconstructed capture. Generated protobuf
descriptors and actual Go encoding are test oracles for field numbers, types,
default omission, UTF-8, varint lengths, nested restore fields and drive order.
Drive count and captured RAM boundaries remain tied to the central limits.

Catalog admission checks the historical parent, distinct target instance and
token, exact revisions, effective hash, approved source identity, stable current
inputs, RAM, artifact keys, Firecracker version and current grant expiry. It does
not renew the historical parent's authority. First publication includes a
network tuple written while an instance is still waking, as well as readiness.
A receipt acknowledged before cache invalidation cannot later publish either.
Resident bookkeeping and terminal cleanup do not require the old cache to stay
available, and catalog history survives source and target instance cleanup.

The original additive migration was applied locally before a catalog lookup
ambiguity was discovered. Its bytes remain frozen; a generated follow-up fixes
the lookup and extends the publication fence to waking network tuples. Both
migrations use replay-safe replacement and retain fail-closed guards on binary
rollback. No previously issued migration or hash protocol changes.

Portable PostgreSQL tests use real storage transactions, raw SQL writes and
catalog contention, with simulated captured bytes and verified cold-fallback
receipts. They verify valid current admission, refusal at grant, acknowledgment
and publication, delayed readiness after cache invalidation, retry after lock
release and resident cleanup. They do not certify loaded RAM. Measured restore
receipts, Manager/RPC/scheduler forwarding, paused promotion and capture after
promotion remain implementation work. Native root/KVM acceptance remains pending
because the configured project is suspended and no alternative acceptance host
is available. Native restore advertisement and public activation stay disabled.

## GitHub first-build admission checkpoint (2026-10-05)

New GitHub applications and source-backed preview sets already capture their
admission versions during creation. The apid GitHub build bridge now installs
those captured controls before consuming account deploy rate or publishing the
first deployment and build. This closes the gap between preview reservation and
the periodic standards repair pass. apid retains customer-intent ownership;
githubd only supplies source and invokes its existing bridge RPC.

Project apply and GitHub enqueue share the bounded immediate materialization
path. Its exact app, organization and desired revision claim preserves worker
lease fencing and reviewed-target precedence. Installation rechecks current
entitlements and ownership; it cannot adopt a newer candidate or project scope
silently. Pending or interrupted installation is retryable (`Unavailable`),
while blocked intent or a scope change requires intervention
(`FailedPrecondition`). Refusal leaves deployment rows, build notifications and
deploy rate untouched. An interrupted request releases only its own claim.

The generated gRPC client and real receiver exercise memory and PostgreSQL
stores, source archives, exact-head preview reservation, six installed controls
and durable enqueue. Recovery runs without waiting for lease expiry; a request
under another worker's lease cannot invalidate that worker's claim. Real project
detachment reenrolls the service and refuses the old scope's source build.
Account mismatch is rejected before installation. Accepted builds retain
observed revision zero: these portable checks do not establish image, log or
native consumer convergence. The full native, fleet recovery, named-environment
and operational acceptance gates remain open, with public activation disabled.

## Private snapshot memory-mapping observation checkpoint (2026-10-05)

Protected native restore now observes the expected memory file in the same live
process whose drive handles were verified. Linux procfs supplies device/inode,
private writable mapping permissions, file offsets and virtual ranges. The
observer requires complete, page-aligned coverage of the pinned memory file
exactly once; partial mappings, file or address overlap, aliases, shared mappings
and a different file identity are refused. A process's open memory-file
descriptor alone is insufficient. PID/start time and jail UID are checked around
observation; immutable pinned memory and VM-state bytes are remeasured, and the
mapping is rechecked before retaining the private observation.

The file backend is supported by this check because Firecracker uses a private
copy-on-write mapping of snapshot memory. Guest writes need not change the
immutable captured backing. VM state is released after loading, so no persistent
VM-state descriptor is invented. See the
[pinned Firecracker snapshot contract](https://github.com/firecracker-microvm/firecracker/blob/v1.12.1/docs/snapshotting/snapshot-support.md)
and [Linux maps contract](https://man7.org/linux/man-pages/man5/proc_pid_maps.5.html).

The observation retains the exact capture/evidence, load-command hash,
PID/start, verified blob identities and mapped ranges. Its accessor refreshes
native observations and returns an owned copy. It does not attest guest CPU
state, page residency or readiness, and is not yet a durable restore receipt.
The existing native/Manager boundary still refuses restore-bound grants and
advertises restore version zero. Receipt protocol/storage and Manager/RPC/
scheduler forwarding, paused promotion, capture after promotion, dedicated KVM
test-metal/leakcheck and the full release checklist remain open.

Portable regressions cover complete and split file ranges, memory holes,
permissions, aliases, overflow and omissions, plus refusal to manufacture a
mapping from a simulated load acknowledgment. Linux tests use an actual private
mmap, close the mapping descriptor while retaining an observer pin, verify
copy-on-write backing preservation, and refuse wrong UID, cancellation, unmap
and a shared mapping. These process tests require no KVM and do not certify an
actual Firecracker guest.

## Durable serving restore receipt contract (2026-10-05)

The internal `RuntimeBootReceipt` now carries optional versioned snapshot
consumption. Its memory/VM-state/captured private-drive identities, capture
token, evidence hash and complete mapped-memory byte count are attached to the
process identity and command hash in the receipt's drive-consumption facts.
Native witness construction couples both observations to the same still-owned
process. The hash must represent the exact protected file-backend load command;
an undelivered cold configuration or paused command cannot stand in for a
serving restore. The witness still does not attest physical page residency,
guest CPU state or guest readiness.

Serving restore receipts require this proof. Verified cold fallback omits it,
retains its cold method and preserves existing admission checks. The Go receipt
model and protobuf adapter retain the complete proof, reject unknown nested
wire fields, and preserve old cold JSON/protobuf encodings when the proof is
absent. Additive SQL migrations extend deterministic wire descriptors without
changing issued migrations or historical evidence hashes.

Both stores compare the proof with the immutable selected capture before
publication. PostgreSQL also checks exact backing, mapped bytes and command
identity for raw receipt writes. It retains the existing current-source hash,
catalog/scope/RAM/expiry locks, acknowledgment and first-publication fences.
Receipt history remains immutable. Existing resident bookkeeping does not
acquire fresh cache authority merely by retaining this historical proof.
The follow-up retains the current source and retained-artifact capability
guards and requires canonical integer method scalars for raw receipts.

Paused restore and capture from restored parents remain explicitly unavailable;
their promotion/serving lineage is separate pending implementation. Manager,
RPC and scheduler forwarding, restore capability advertisement, named production
scope, all onboarding adapters, real scanner/provider/native multi-service
updates and rollback, native/fleet recovery, dedicated KVM test-metal/leakcheck
and the full release checklist remain open. Public activation stays disabled.
Durable-store fixtures simulate native consumption; they do not certify a
Firecracker restore.

## Serving restore forwarding checkpoint (2026-10-06)

The ordinary scheduler wake now retains the storage-owned capture token from
its selected snapshot. A node that explicitly advertises the serving restore
version receives the scoped catalog envelope inside the complete boot payload
hash and fresh binding. Durable issuance still fences current application inputs,
source identity, RAM, catalog/cache usability and expiry. Unsupported nodes keep
the existing verified cold path without receiving a restore-bound grant. Local
cache selection uses the catalog's canonical VM-state storage locator rather
than an old host pathname. Companion RAM contributes to the same physical-memory
check as native allocation.

The generated client and RPC receiver preserve owned envelope copies and compare
returned consumption with that exact selected capture. Manager forwards the
current runtime specification and approved sources through protected native
loading, retains the existing kernel/base backing check and cold fallback, then
requests one coupled drive/memory witness for the target lease. Its serving
receipt carries those facts through storage acknowledgment and publication.
The replacement cold process's attempt is retained in the published lease and
failed-boot cleanup; a retired restore attempt cannot identify its drive owner.
The ordinary, unadmitted Wake entry point cannot accept catalog authority.
Cancellation, expiry, replay or invalid consumption refuses the receipt and
retires the runtime through the existing owner cleanup.

A failed load still boots verified current sources. Its cold receipt contains no
snapshot consumption, while retaining the fresh binding's catalog checks until
publication. Scheduler cache retirement runs after that publication attempt;
invalidating its own selected cache earlier would incorrectly reject a valid
cold runtime. An externally invalidated capture still refuses acknowledgment or
first publication. Cache retirement retains its existing best-effort policy after that attempt.

Portable tests join real scheduler selection, both stores' durable grants,
receipts and publication with explicit scanner/provider/native simulations.
They cover serving restore, unsupported nodes, verified cold fallback and
substituted proofs. Manager tests cover main/companion input forwarding, receipt
ownership, malformed authority, failed load, altered witnesses, cancellation and
retirement. A generated gRPC client talks to the actual receiver over a Unix
socket; separate client tests refuse structurally valid substituted backing and
clean up the target runtime. These tests do not certify a Firecracker guest.

`JailerVMM.RuntimeSnapshotRestoreVersion` still returns zero. There is no runtime
switch to advertise an unaccepted loader. Paused restore/promotion and capture
from the actual serving promotion/restored parent remain implementation work.
Named production environment scopes, remaining onboarding adapters, real native
multi-service update/rollback, physical entropy/clock and resource isolation,
fleet/migration recovery, dedicated Linux amd64 root/KVM test-metal/leakcheck and
the full release checklist remain open. Public standard activation stays disabled.

## Capture from the actual serving parent (2026-10-06)

A fresh capture may now retain a complete measured serving restore receipt as
its historical parent. Parent validation uses the saved completion clock, so an
expired boot grant does not itself invalidate healthy residency. This does not
renew that grant or authorize a new load. The fresh capture grant still reviews
current ownership, application inputs, producer approval, source start time and
expiry; native capture still owns the live process and pinned drives while
freezing and measuring the new output.

Both stores select the current serving receipt, including an acknowledged
promotion when the instance names one, rather than falling back to its original
boot receipt. Missing or mismatched serving history refuses capture. PostgreSQL
retains nonwaiting instance, boot and promotion locks and applies the existing
native receipt/source proof checks to that selected parent. The new migration
is additive; historical grants, acknowledgments and issued migration bytes are
unchanged.

Every new output uses a fresh coupled namespace. The restored input's capture
token cannot identify the new memory, VM-state or private-drive output, including
warm and compact deployment-key aliases. New acknowledgments retain the exact
serving parent's complete proof. Collection of an input cache does not erase
resident identity or its immutable lineage; every subsequent capture still needs
fresh policy and producer approval. A substituted parent or a changed current
policy refuses acknowledgment without partially publishing a capture.

Portable protocol and Manager tests cover restored serving history, owned wire
copies, warm/park capture and refusal to reuse the input namespace. The same
ordinary scheduler wake and park now retain the serving restored receipt in the
new catalog and a second wake consumes that catalog with its complete parent
proof, including deterministic SQL/protobuf evidence hashes. Both durable stores
exercise these paths with explicit native and cache-publication simulations.
They do not certify a Firecracker snapshot.

Protocol 2 paused restore and measured promotion remain unavailable. A future
promotion must retain the actual paused load command and independently prove
resume and its guest hook; changing a boolean or replacing its command hash with
a command that was never delivered cannot establish serving lineage. Production
restore advertisement and public activation stay disabled. Dedicated KVM
test-metal/leakcheck, physical entropy/clock/resource isolation, native/fleet
recovery, remaining onboarding/environment scope and the full release checklist
remain open.

## Acknowledged resume transport for measured promotion (2026-10-06)

The private native resume transport now retains versioned acknowledgments of
the actual PATCH command and the complete framed guest-hook payload that
received ACK=0. The hook acknowledgment includes its supplied host clock and
completion clock, and hashes the exact frame with a distinct versioned domain.
Entropy remains inside the transport; neither the observation nor logs retain
the payload. A lost ACK retries with fresh entropy and keeps only the successful
attempt's hash. Errors, cancellation, NACKs and incomplete writes return no
acknowledgment.

A strict resume observation does not accept a Firecracker conflict as evidence
of a state transition. The existing legacy resume retry still tolerates that
conflict, without producing a strict acknowledgment. Unix-socket tests inspect
the delivered bytes, clock, retry entropy and failure boundaries using simulated
Firecracker and guest peers.

These are transport facts, not native promotion receipts. The measured promotion
path must still couple them to the same owned PID/start and pinned paused load,
retain the original load-command hash, and bind fresh durable promotion authority
through Manager, RPC, protobuf and both stores. Protocol 2 paused restore remains
unavailable, production restore advertisement remains zero and the complete
native acceptance and release checklist above is unchanged.

## Owned paused-load resume observation (2026-10-06)

The private native resume operation now couples a fresh promotion request to
the retained, acknowledged paused load. It checks the historical grant at its
saved completion clock while validating the new grant against the current
clock and deadline. The process, UID, descriptor access modes, immutable
backing and complete private memory mappings must match the original parent
before any resume command and again after the acknowledged guest hook.

The same owned load can attempt resume only once. Source preparation, resume
and snapshot capture cannot share a native flight; retirement cancels and
joins that flight before closing its pins. Refusal before an attempt preserves
the owner, while an attempted resume that fails or loses current identity
returns no partial observation and retires the lease. The original paused
load-command hash is retained alongside separate hashes and clocks for the
actual acknowledged resume command and hook frame.

Portable tests refuse API-only load acknowledgments, substituted owners,
replay, cancellation and concurrent capture/preparation. A Linux unit fixture
uses actual child-process descriptors and private memory mappings with simulated
Firecracker and guest peers to exercise the complete observation and its failure
boundaries. It does not certify Firecracker CPU state, guest readiness or KVM
acceptance. Its process fixture requires an unprivileged positive UID; root
acceptance remains in the dedicated native lane.

This operation returns native facts, not a publishable receipt. Manager, RPC,
protobuf, durable promotion authority and both stores still need the full
measured promotion contract. Protocol 2 paused publication remains refused,
production restore advertisement stays zero, public activation remains disabled
and every outstanding release item remains open.

## Checked resume evidence contract (2026-10-06)

The owned native observation now returns a versioned resume evidence object.
Its complete fresh binding and domain-separated hash of the exact historical
paused receipt retain the initial load command, process start, drives and private
mapping. A shared validator compares both observed consumption records with that
same parent, requires the exact acknowledged resume command, and checks the hook
hash and ordered command, host, hook and completion clocks against the fresh
grant. Changing the grant's nonce, policy, payload or expiry cannot reuse an
observation. Replacing the original paused command with a serving-load hash is
refused. Returned observations own their request and drive copies.

The standalone protobuf message preserves this complete contract and refuses
unknown fields, unsupported versions and missing bindings. It is deliberately
separate from boot receipts and promotion responses: no historical receipt wire
encoding or SQL migration changes. Native observation validates the complete
object before returning it; an attempted resume that fails validation follows
the same joined retirement path as a failed command or hook.

Portable tests check substituted history, grant, process, mapping, command and
clock facts, owned copies and protobuf/JSON round trips. The Linux process
fixture checks the same contract against its actual descriptors and mappings
with simulated Firecracker and guest peers. These observations do not certify
guest readiness, physical entropy reseeding or KVM acceptance. Durable receipt
publication through Manager, RPC and both stores remains implementation work.
Production restore advertisement and public activation stay disabled, and all
release checklist items remain open. The user confirmed that no alternative
dedicated native acceptance host is currently available.

Ordinary snapshot observation continues to validate against the current clock.
Only the bound resume path rechecks historical load identity at its saved clock;
it separately requires fresh promotion authority. Portable refusal tests and
the Linux process fixture ensure that an expired original grant cannot produce
an ordinary consumption observation or replace actual process ownership.


## Durable measured promotion receipts (2026-10-06)

The receipt now carries the checked resume evidence together with the original
paused-load consumption. Historical parent binding and completion fields allow
reconstruction of the entire original receipt without a recursive receipt tree.
Its deterministic protobuf hash and the fresh promotion payload must both match.
The serving receipt retains the exact paused load command, PID/start, drives and
private mapping; separate command/hook hashes and ordered clocks prove resume.
A boolean change, invented serving load command or substituted parent is refused.
Receipt field 11 and resume fields 10–11 are additive. An absent resume proof
keeps historical boot/capture protobuf and JSON encodings unchanged.

Manager forwards a requested paused load only to a measured catalog backend
with resume support, then calls its owned measured operation for protocol 2.
It checks the returned proof before serving monitors or receipt publication and
joins retirement on failure, cancellation or loss of current ownership. Legacy
promotion remains a separate protocol-1 path. RPC validates the complete receipt
and refuses unknown nested lineage; initial boot publication cannot carry resume
proof. Scheduler probes the required capability, saves a fresh promotion grant
before resume, and checks the returned receipt before atomic publication.

MemStore and PgStore retain the immutable original boot receipt and separate
issued promotion. First publication rechecks current policy, node incarnation,
producer approval, catalog and expiry. A committed identical retry does not
renew authority. The additive resume-receipt migration mirrors the complete
proof and deterministic wire hashes for raw SQL, rejects proof submitted to a
boot row, and fences the final warm-to-running transition against current catalog
state. Previously applied migrations remain unchanged.

A new capture selects the actual promoted serving receipt in both stores. The
native owner retains its accepted resume evidence and reobserves the original
process, drives and mapping before accepting it as a capture parent. An expired
original boot grant remains historical identity; ordinary observation still
requires current authority. Linux and non-Linux refusal tests assert their
respective native ownership errors rather than conflating those platforms.

Portable Manager, RPC and scheduler tests simulate native acknowledgments.
Real PostgreSQL and MemStore tests cover complete promotion, altered proof,
raw-writer refusal, SQL/protobuf parity, retry and capture of the promoted parent.
Historical cross-tenant event/replay fixtures explicitly verify the creating
account guard, seed pre-ADR-595 state only inside an isolated test schema or
private test database, then restore the guard transactionally. Current writers
continue to be refused.

These software contracts supersede the implementation gaps recorded in the
preceding resume checkpoints. Jailer restore advertisement remains zero and
public activation remains disabled. Dedicated native test-metal/leakcheck,
physical guest entropy/clock/isolation, real fleet recovery, remaining onboarding
and environment scope, and all eleven release checklist items remain pending.
The user confirmed no dedicated native acceptance host is available.
