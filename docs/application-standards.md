# Application standards

Application standards describe organization-owned requirements for services.
Published versions are immutable, carry a canonical definition hash, and record
their publishing identity. Publishing creates a candidate; it does not activate
the version or change applications.

The implementation is in progress. Candidate version and resource management,
plus automatic enrollment repair and private reviewed control installation,
are implemented. Public
assignment activation, runtime enforcement and controlled rollout
must pass the acceptance checklist in [ADR-435](adr/435-inherited-application-standards.md)
before this feature is declared available.

## Enrollment boundary

Every organization-owned application insert records its original control values
and the explicit admission versions of matching active organization, project and
application assignments. This includes project plan/reconcile and PR preview
inserts. Publishing a candidate does not move an admission pointer or an existing
application's adoption. Restoring an application or changing its organization or
project rechecks inheritance and preserves its original values and local intent.

Enrollment keeps desired, persisted and observed revisions separate. Pending,
applying or blocked enrollment rejects deployment creation with HTTP 409 and
`application_standards_pending`. Persisting configuration will not count as
runtime observation. apid's repair worker discovers durable pending enrollment,
including after restart, and installs the captured versions into the existing
control tables. Public assignment activation remains under development.

Legacy projects have account ownership rather than a dedicated organization
column. Assigning a project verifies the creator's organization membership and
every live member application's persisted organization. Shared project locks
on membership changes serialize that check against activation, including
cross-organization insert/restore races. An assignment retains its identity;
updates advance its revision, and direct deletion is reserved for organization
erasure.

## Reviewed changes under development

The private review store records organization, project or application assignment
changes, affected services, current controls, proposed controls, field provenance
and blockers. Reviews expire after 30 minutes. Their approval digest binds the
server-issued review identity, creator, assignment revision, admission version,
batch size, target membership, adoption pins, local intent, immutable resource
hashes, account entitlements and current artifact metadata. An unrelated candidate
publication or unused resource does not invalidate the reviewed version.

Freshness checks reread storage and reject changed inputs, expired reviews and
blockers. They also check inheritance for future services, including empty
projects. Existing services resolve their saved adoptions separately from the
versions offered to new services. An assignment disabled for new admissions can
remain adopted while its controlled removal proceeds.

An initial default preserves explicit existing settings. A logging requirement
that permits extra destinations preserves those extras separately, so replacing
the company's required destination does not turn the old destination into a
permanent extra. Quota review counts the entire proposed batch and existing
account-wide drains, including disabled destinations. Security changes to
existing artifacts require verification evidence; setting an enforcement flag
alone does not satisfy that gate.

Review records contain no destination URLs or credentials. Current destination
and credential inputs are represented by hashes. The schema also retains
immutable approved operation intent and target identities through service
deletion, with private fenced lease fields for subsequent worker integration.
Company attribution survives account erasure; owning organization erasure
removes its review and operation history.

The private approval path now locks and rereads the complete input set, checks
the exact digest and current approving authority, and commits the admission
pointer, frozen rollout targets and audit event in one transaction. A stale,
expired or blocked review writes none of them. Repeating the same approval
returns the original operation; another unfinished operation on the same
assignment blocks an overlapping change. Input locks use bounded retries so
concurrent legacy control writers cannot deadlock an approval's parent locks.
Restoring or reenrolling a service revokes its previous worker lease authority.

Approval leaves existing adoption pins and actual settings unchanged until the
service's rollout batch. New services capture the new admission version and
remain pending until materialization. Disabling admission likewise retains
existing pins until their approved removal. Saving an operation does not mark
any service persisted or observed.

These methods are internal storage interfaces. Public review/approval endpoints,
consumer verification, exceptions, and rollback are still
being implemented. A successful read-only freshness check does not authorize an
unlocked mutation; writes must use the atomic approval path.

Native source staging uses a private `.vmmd-runtime-sources` directory in the
node-local storage cache, or local storage root when no cache is configured. It
records each instance before exposing sealed image bytes and keeps an OS lock
while a cache is active. Startup and periodic cleanup reclaim aged unlocked
roots only after every recorded instance is durably gone. Live instances,
database failures and unknown records retain their shared files. Ownership
records authorize cleanup; they do not count as runtime observation. Full
snapshot lineage, consumer acknowledgments and native acceptance remain open.

Governed cold boots also measure the staged producer drives, the final drives
after runtime injection, and the exact Firecracker configuration. A Linux
observer checks that the live native process holds every measured drive inode
with the expected read-only or writable access. Teardown removes these facts.
They are private in-memory verification; protocol-1 receipts and observed
standards adoption do not gain content authority from them.

On the dedicated Linux amd64/KVM acceptance host, with the checkout's staged
kernel, base and main-layer fixtures configured, run:

```bash
RUN_REGEX='^TestMetalRuntimeDriveHandoff' make test-metal PKGS=./pkg/fcvm
make leakcheck
```

The native cases require all three fixtures, verify two live VMs and a sidecar,
and reject a changed source without retaining a runtime. Linux process tests
also exercise actual procfs access modes without KVM. These checks do not replace
the full scanner, snapshot, rollout, recovery and product acceptance checklist.

## Publish and inspect candidates

Create a definition file:

```json
{
  "require_signed": {"mode": "mandatory", "value": true},
  "security_policy": {"mode": "mandatory", "value": "enforce", "override": "narrow"},
  "egress_cidrs": {"mode": "restricted", "value": ["203.0.113.0/24"]},
  "egress_extra_ports": {"mode": "restricted", "value": [5432]}
}
```

The example network is a documentation range; select actual approved
destinations before applying a standard. Plan and platform network restrictions
still apply.

```bash
gregale orgs standards publish --org acme --standard production-baseline \
  --file standard.json --expected-version 0 --description "Production baseline"
gregale orgs standards list --org acme
gregale orgs standards show --org acme --standard production-baseline --version 1
```

For an update, supply the latest version as `--expected-version`. A concurrent
publication returns `application_standard_version_stale`; refresh and review
before retrying. Read an older version with `--version`, or omit it for the latest
candidate. List responses include `next_page_after`; pass it as `--after` to
retrieve the next page. Output is structured JSON.

Owners and admins publish versions. Active organization members can inspect
candidate definitions. Requests require the existing authentication, MFA and API
key scope checks in addition to the organization action.

## Organization-owned resources

Create logging destinations and approved publishers once per organization.
Use the returned resource UUIDs in `log_destinations` and `trusted_publishers`.
Publication rejects missing resources and resources owned by another organization.

For a logging destination, save a private JSON file:

```json
{
  "name": "Central production logs",
  "kind": "http_json",
  "target_url": "https://logs.example.com/ingest",
  "auth_header": "Authorization: Bearer REPLACE_WITH_CREDENTIAL"
}
```

The endpoint must use HTTPS and cannot contain userinfo, query strings or
fragments. Credentials belong in the optional header and are sealed server-side.
Read/list responses, standard definitions and audit events omit credential material.

```bash
gregale orgs standards destinations create --org acme --file destination.json
gregale orgs standards destinations list --org acme
gregale orgs standards destinations show --org acme --id DESTINATION_UUID
```

A publisher file contains `name` and `public_key_der`, the base64-encoded ECDSA
P-256 SubjectPublicKeyInfo DER supported by Gregale's image verifier. Private
keys, malformed keys and unsupported curves are rejected. Publisher responses
include the public key and its SHA-256 fingerprint.

```bash
gregale orgs standards publishers create --org acme --file publisher.json
gregale orgs standards publishers list --org acme
gregale orgs standards publishers show --org acme --id PUBLISHER_UUID
```

Resources are immutable, including destination credentials. Rotation creates a
new resource and a new standard version referencing it; existing services change
through the controlled adoption process. Creating a resource alone changes no
service. Emergency revocation and adoption still require the remaining acceptance
work; these endpoints are candidate management during implementation.

## Requirement semantics

| Mode | Meaning |
|---|---|
| `default` | Supply an inherited value when local intent does not replace it. |
| `mandatory` | Require the value; changes obey the declared override rule. |
| `restricted` | Bound the permitted value and allow narrowing only. |

An omitted override is `none` for a mandatory requirement and `narrow` for a
restricted requirement. `extend` is valid only for mandatory log destinations:
all required destinations stay present while additional destinations are
permitted. `narrow` permits stronger signature/posture requirements, a smaller
publisher or extra-port set, and CIDRs contained by the approved ranges.

Supported fields are `log_destinations`, `require_signed`, `security_policy`,
`trusted_publishers`, `egress_cidrs`, and `egress_extra_ports`. Destination and
publisher sets contain organization-owned resource UUID references. Credential
values do not belong in the definition. Unsupported fields, duplicate JSON keys,
unknown rule properties, and incompatible override modes are rejected.

Clearing `egress_cidrs` is unrestricted access in Gregale's network contract; it
does not narrow a nonempty approved range. Disjoint inherited CIDR bounds are
reported as a conflict instead of becoming an unrestricted empty set. Extra
ports retain the existing platform contract, including the base ports and
forbidden-port restrictions.

More-specific scopes cannot weaken mandatory ancestor requirements. Effective
settings retain every contributing standard, version, scope and applicable
exception identifier. Approved exceptions affect only the named field and
immutable version; at their exact expiry boundary the ordinary requirement
applies again. Independent requirements remain enforceable.


## Private rollout materialization

Implementation now includes a private control-plane materializer for approved
operations. It installs actual log destinations, publisher keys, image settings
and outbound settings transactionally with a persisted target checkpoint.
Company resources retain logical identities through private physical bindings.
Removed legacy drains and signer keys have private backups, including sealed
credentials, so a reviewed removal can restore their original configuration.
Backup bodies are excluded from effective views, reviews, audit and operations.

Worker claims expire and carry a generation that rejects an old process after
replacement. Each target must still match its approved inputs and current plan
limits. A changed target is blocked without overwriting its settings. Managed
controls reject legacy patches that change the resolved projection. The shared
intent path for permitted overrides is still pending.

Saved settings produce a `persisted` target, with no observed revision. The next
wave waits for actual consumer verification. Public activation,
runtime proofs, exceptions and complete rollback
operations remain acceptance work; these private paths are not a released
application-standards feature.

The private native boot protocol now binds a complete prepared request to one
compute node and vmmd process, a captured input digest, a standard revision and
an exact egress revision. Unsupported nodes refuse before boot; expired,
replayed and mismatched grants refuse admission. The backend returns a receipt
for the actual runtime identity. Cancellation joins the in-flight boot and
cleans up a late success. Managed cold boots, snapshot restores and initial paused
warm restores save grants before native invocation and atomically publish the
matching receipt and runtime. Warm promotion saves fresh authority tied to the
same paused lease; an initial receipt cannot authorize resume. Exact committed
promotion publication can be retried after a lost acknowledgment without another
native resume. Current input and native process fences still apply. This supplies no image-content or delivered-log
proof, and does not mark the standard observed or enable public activation.

Native publication still obeys the fleet recovery-capacity and exclusive-operation
lifecycle checks. Capacity refusal commits neither a receipt nor a runtime
transition. An eligible survivor can publish a warm promotion into its declared
service slot while the fleet is degraded. These storage guarantees remain
separate from native consumer verification.

An app with no adopted standard or retained managed fields can keep its existing
resident guest after an account plan change. Its original admission capture is
preserved; ownership, controls, artifacts, current account eligibility and
capacity checks remain in force. A new boot still rejects stale plan inputs.
Managed services retain the strict plan fence for boot and promotion.

Fresh installation and ordinary database upgrade pass. An explicit local
PostgreSQL 16 [reviewed ledger recovery](runbooks/application-standard-ledger-recovery.md)
verifies the complete expanded schema, immutable migration bytes and backfill
coverage before appending current recovery events and an immutable receipt.
It preserves application configuration, rollout and native admission history.
Normal daemon startup does not invoke this operation. The unmodified
full-feature replay gate still fails at the frozen initial standards migration
when its tables already exist; explicit recovery does not resolve that gate.
Existing migration files remain immutable. Public activation still requires
complete recovery acceptance and native consumer acceptance.

## Automatic onboarding and repair

apid runs bounded repair passes every five seconds. Each pass visits reviewed
operations first, then pending enrollment. A queued reviewed target takes
precedence even if an automatic worker claimed the service before approval.
Claims carry a generation, desired revision and a storage-owned expiry; restoring
or reenrolling a service revokes earlier authority. A transaction that outlives
its lease rolls back its intent and controls together.

New services receive actual log drains, publisher keys, signature posture and
outbound settings without manual enrollment. Automatic repair uses their captured
adoption versions; a later publication or admission update cannot move them
silently. Current creating-account entitlements and aggregate drain quotas still
apply. A blocked service keeps its original controls and a stable error code;
durable retry reevaluates it after thirty seconds.

Enrollment retains which fields were last installed separately from desired
ownership. Leaving a project restores original values and private legacy control
backups even when no assignment remains. An inherited default does not become a
local override during restore. Previously managed controls remain protected
while that repair is pending.

Installation advances the persisted revision, leaving observation at zero.
These checks do not prove image verification, delivered logs or live network
convergence. Restore/wake admission, runtime acknowledgments and native acceptance
remain necessary before public activation is enabled.

Source-build conversion now verifies the local OCI manifest, config and layer
bytes against their declared digests and sizes, including each uncompressed
layer's DiffID. Duplicate entries and corrupt gzip streams refuse conversion;
valid source builds keep using the existing container and function paths. This
content check does not prove an approved company publisher. Durable proofs bound
to the rootfs and sidecars that actually run, current publisher keys and scan
expiry remain part of the image-policy acceptance work.


Direct registry image and sidecar preparation retain private immutable publisher
verification records. Storage authenticates the exact signed payload against the
current trusted key, binds the persisted customer reference and selected child,
and assigns a 24-hour expiry. Key rotation/deletion, scope/reference changes and
substituted signed bytes refuse publication. Exact record retries retain their
original expiry; these records cannot be updated in place. Registry credentials
are absent from the evidence. The full-rootfs fallback and sidecars use the
resolved immutable child for conversion.

These are registry-source records. Historical reads can return revoked or expired
evidence and therefore do not authorize runtime admission. Converted rootfs bytes,
source-build approval, per-workload scan evidence, live immutable-subject refresh
and native proof consumption remain acceptance work before standard activation.

Private managed boot requests now carry captured producer digest and complete
byte count for the base, application layer and sidecars. vmmd verifies each
complete storage stream into a protected source before staging the VM, while
sharing the read-only base and preserving a private writable application drive.
Mutable local storage paths cannot replace a verified source afterward.

This source check currently requires verified cold boot; paused snapshot restores
remain unavailable until snapshot lineage is bound. Source staging does not
approve the final guest overlay, acknowledge physical consumption, or advance an
application's observed standard version. Snapshot restoration and promotion,
retained-source restart cleanup, and native acceptance remain necessary before
public activation.

The default scanner now receives a private bounded copy with guest-root symlink
resolution and complete before/after tree verification. Scanner success cannot
accept a changed tree, and cancellation/failure removes its staging copy. This
handoff is preparation for whole-runtime scanning; separate component scans do
not prove the composed guest filesystem. Native composition, fresh approval and
the dedicated Linux amd64 scanner/KVM acceptance remain required.
