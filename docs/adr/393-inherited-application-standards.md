# ADR-393 · Versioned inherited application standards

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
failed component evidence when current inputs permit publication. Revoked,
expired or replaced producer inputs cannot publish or renew a scan; historical
reads remain available without asserting authority. Exact retries retain the
original storage clock and cannot reactivate an older selection. Complete
high/critical/unknown findings remain visible evidence and block enforce mode;
a sidecar scan failure or unsafe result also uses the live quarantine path.

Component scans have a storage-owned five-minute lease clamped to the producer
expiry. The scan pass has a five-minute deadline, and vulnerability databases
may be at most 30 days old and cannot be future-dated. Central bounds are 8 MiB
per retained report, 16 MiB scanner stdout, 64 KiB per diagnostic stream,
100,000 findings, 256-byte version metadata, 128 paths per finding and 4,096
bytes per path. Complete artifact reads use the existing 16 GiB cap. Subprocess
diagnostics are bounded and are not echoed into logs or reports.

These are component producer facts under the existing trusted imaged/database
writer boundary. Portable fixtures verify real byte copies, real layer
consumption, PostgreSQL atomicity and refusal behavior; injected mkfs and Grype
fixtures do not establish native scanner execution or consumer ACKs. Scans of
the shared base, complete two-drive approval, immutable-source refresh after
registry verification expiry, native capture/consumption and dedicated Linux
Grype/KVM/leakcheck acceptance remain pending. Ext4 extraction resource and
cleanup behavior also requires native acceptance. The existing six-hour legacy
re-scan schedule is not a renewal mechanism for the new five-minute component
lease. Public activation remains disabled.

This is a producer boundary under the existing imaged/database writer trust
model. Source-build publisher approval, complete runtime scans and native boot
consumption remain separate. Artifact keys remain mutable; later consumers must
freshly validate the selected expected digest and size. Existing admission
captures do not yet include these producer identities. Historical
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

Managed cold boots, initial snapshot prime and snapshot restores now save an
immutable private grant before invoking the native capability. Initial warm
restores use the same protocol with explicit paused intent. Storage owns the
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

A managed node requires vmmd's existing compute-node registration with database
configuration (including a named default-local node on a single box). A legacy
local process without registered native identity or an older native backend
refuses managed admission before allocation. Native process registration does
not query inherited customer intent.

This remains partial enforcement. The grant/receipt alone does not attest artifact
content, log delivery, established-flow tightening or all live-instance egress
convergence, and it never advances an observed standard revision. App tasks,
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
