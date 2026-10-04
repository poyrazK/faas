# Application standards

Application standards describe organization-owned requirements for services.
Published versions are immutable, carry a canonical definition hash, and record
their publishing identity. Publishing creates a candidate; it does not activate
the version or change applications.

The implementation is in progress. Immutable candidates, resource management,
automatic enrollment, assignment inventory, reviewed approval/operator controls,
local intent and bounded exception APIs are implemented. Mutation APIs share a
default-off release gate. Runtime consumer convergence and controlled rollout
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
control tables. Public assignment activation remains disabled pending acceptance.

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

Public non-activating previews, assignment inventory and saved review, operation
and exception-history reads are implemented. Approval, operator, local-intent and
exception mutations use the default-off release gate described below. Complete
consumer verification and end-to-end fleet rollback remain acceptance work. A
successful read-only freshness check does not authorize an unlocked mutation;
writes use the atomic approval path.

Logging delivery now has a private observation checkpoint. Each loaded standard
drain carries its application revision, effective hash, organization resource
hash and exact sender fingerprint, including sealed credential bytes. A receipt
follows a real successful HTTP response and durable queue acknowledgment.
Storage checks current ownership, source instance, installed revision and the
full sender tuple, and rejects expired exceptions or delayed old workers.
Gateway receipt writes use a bounded two-second periodic pass, retry storage
failures and stop after one accepted receipt per loaded projection. Pending
receipts contain source identity and sequence; they retain no log content.

The gateway also records a separate private loaded-inventory checkpoint. One
storage snapshot binds the application revision and effective hash to every
enabled sender, including permitted local additions, or to an empty set after
logging removal. The gateway requires every expected sender to have started
successfully and every obsolete worker to have exited. This covers services
without log events; it supplies no provider-delivery receipt for them.

Inventory facts identify the configured compute node and daemon startup session.
A new startup receives a higher generation, and retained session history refuses
a superseded startup's delayed registration retry. Current reads reject changed
configuration, expired exceptions, unavailable nodes, superseded sessions and
facts older than 90 seconds. The gateway refreshes facts in a bounded two-second
pass and holds an OS spool lock until its workers join. A missing or unknown
`FAAS_NODE_NAME` leaves node verification pending. A configured node becoming
unavailable suspends its facts without changing its session identity.

These facts do not advance the application's observed revision or release the
next rollout batch. Fleet consumer membership and capability qualification,
provider delivery health, daemon recovery acceptance, and the other runtime
consumers still require verification before controlled rollouts can finish.
Operators must stop older gateway processes before introducing the spool-lock
protocol. Public activation remains disabled.

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


## Inspect application enrollment

Read a live application's captured adoption and current progress:

```bash
gregale orgs standards application --org acme --app APPLICATION_UUID
```

The read-only API is
`GET /v1/orgs/{slug}/application-standard-enrollments/{app}`. The Go SDK exposes
`GetApplicationStandardEnrollment`. The caller needs a read-scoped credential
and active organization membership; session requests retain the MFA gate.
Applications in another organization and deleted applications return 404.

`local_settings` and `additional_log_destinations` describe saved local choices.
`installed_effective` and its field provenance describe the last installed
projection; they are omitted before installation. Pending local changes can
therefore have a newer `desired_revision` while `persisted_revision` and installed
settings remain unchanged. `observed_revision` records actual consumer evidence
and does not advance because a read or installation succeeded. Destination URLs,
credentials, original private control backups and worker leases are excluded.
Public assignment activation, override writes and exception management remain
under development.

## Preview and history

Owners and administrators can save a non-activating preview. It captures the
assignment revision, affected applications, proposed effective values, field
provenance and blockers. It never changes assignments, enrollments or app
controls. Read-scoped organization members can inspect saved previews and
operations; cookie sessions still require MFA.

```bash
gregale orgs standards reviews preview --org acme --file review.json
gregale orgs standards reviews show --org acme --id REVIEW_UUID
gregale orgs standards operation --org acme --id OPERATION_UUID
gregale orgs standards exceptions --org acme --app APPLICATION_UUID --limit 100
```

A new organization assignment preview uses this request. IDs are UUIDs read
from the organization and published standard. Every scalar shown is required,
including `active` and `expected_revision`; an update also supplies the existing
`assignment_id` and its current revision.

```json
{
  "scope": "organization",
  "scope_id": "ORGANIZATION_UUID",
  "standard_id": "STANDARD_UUID",
  "admission_version": 1,
  "expected_revision": 0,
  "active": true,
  "batch_size": 10
}
```

`POST /v1/orgs/{slug}/application-standard-reviews` saves the preview with an
approval hash and expiry. `GET .../application-standard-reviews/{review}` reads
it. Saved history can be inspected after expiry; historical inspection does
not prove that the review is still approvable. A later approval must revalidate
all authoritative inputs. Public approval remains gated on runtime acceptance.

`GET .../application-standard-operations/{operation}` returns saved target
progress and its approved application view. Queued targets have no installed
desired revision yet. A persisted target still needs actual consumer evidence;
reading progress never creates that evidence.

`GET .../application-standard-enrollments/{app}/exceptions` lists historical
approvals in ascending UUID order. The response includes the server's `as_of`
time and `active`, `expired` or `revoked` status; revocation takes precedence.
An active historical approval applies only when its standard version is still
adopted. `after` is exclusive and `limit` is 1–100. A full page returns
`next_page_after`; the final follow-up page can be empty. Records retain the
reason, approving identity, expiry and revocation identity/time.

Enrollment responses also expose `installed_exception_expires_at` when the last
persisted projection used an exception. The deadline can already be expired
while replacement is pending; it does not assert observation. These public
responses exclude original base settings, artifact proof bodies and worker
leases. The Go, Node and Python SDKs expose the same preview and inspection
routes. Assignment activation and exception mutation APIs remain unavailable.

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
controls reject legacy patches that change the resolved projection. The private
local-intent path accepts permitted overrides through that same resolver.

Private operator controls now support pause, resume and abort. Each command
requires a current active owner or admin and the exact operation `updated_at`
returned by storage. Concurrent worker progress or another operator's command
rejects a stale timestamp. State, lease revocation and the audit event commit
together. Repeating pause on a paused operation or abort on an aborted operation
with its current timestamp changes nothing and writes no duplicate audit event.

Pause preserves all target checkpoints and prevents both new claims and previous
worker leases from writing. Resume grants no worker authority itself; a fresh
claim must reacquire it. A resumed wave still waits for real consumer observation
before advancing. Abort records `state=failed` with `error_code=operator_aborted`
and marks untouched queued targets as skipped. It preserves persisted settings,
observed checkpoints, blocked-target evidence and the approved admission version
for new services. Stopping new admissions or reverting installed settings needs
a new affected-app preview and approval. That reviewed rollback uses current
inputs, entitlements and artifacts, including services created during the forward
operation; it never reuses the forward approval. An aborted operation stays in
history and cannot be resumed. These controls have no public endpoint yet.

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

## Permitted local settings

The private local-intent store accepts a complete `settings` object and a separate
`additional_log_destinations` set, together with the current `expected_revision`.
An empty object and empty set clear local choices back to inheritance. Unknown
fields, duplicate JSON keys, null values and out-of-bound changes are rejected.
Local settings must name fields governed by a captured standard; other controls
continue through their existing application interfaces.

Active owners, admins and developers can make permitted choices. The app owner's
current plan supplies entitlements. Mandatory logging permits additional
organization-owned destinations only when every contributing constraint allows
`extend`; required destinations cannot also become extras. Narrowing publisher
sets or strengthening security posture checks the current authenticated selected
artifacts and composed scan. A scan replaced before materialization is checked
again and can block installation.

Saving local intent increments the desired revision, revokes prior enrollment
worker leases and writes an audit event atomically. It leaves the previously
installed controls and their persisted and observed revisions intact while the
new revision is pending. Deployment admission remains blocked until repair
installs it. A stale revision or an active reviewed rollout on the app refuses
the mutation. An unchanged permitted request writes no new revision or audit.
Public override endpoints and consumer observation remain acceptance work.

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


### Private approved exceptions

The private control plane can approve a replacement for one inherited field in
one application's captured standard version. It records the reason, active owner
or admin who approved it, and an expiry of at most 30 days. The replacement must
still satisfy independent standards, platform restrictions, resource ownership,
plan quotas and current artifact security checks. Permitted local settings are
separate from an approved exception.

An approval is immutable. Revocation and expiry retain its history, queue repair
and preserve the distinction between desired, installed and observed revisions.
The installed exception deadline refuses new runtime admission even when repair
is delayed. If expiry makes saved local choices invalid, the application stays
blocked until a permitted correction restores compliance. This private lifecycle
has release-gated mutation routes described below; native enforcement and consumer
convergence acceptance remain required before enabling them.


## Release-gated local settings and exceptions

The API, Go/Node/Python SDKs and CLI now expose the existing atomic local-intent
and exception lifecycle. These mutations are **disabled by default**. The apid
boot-time opt-in is `FAAS_APPLICATION_STANDARD_MUTATIONS_ENABLED=1`; deployment
must keep it unset until ADR-435's consumer convergence, controlled rollout and
recovery, and dedicated Linux amd64 root/KVM acceptance gates pass. Adding the
contract does not satisfy those gates. The same disabled gate protects reviewed
assignment approval and the operator controls described below.

| Operation | Route suffix under `/v1/orgs/{slug}/application-standard-enrollments/{app}` | Authority |
| --- | --- | --- |
| Replace local choices | `PUT /local-intent` | Owner, admin or developer |
| Approve one-field exception | `POST /exceptions` | Owner or admin |
| Revoke and retain history | `POST /exceptions/{exception}/revoke` | Owner or admin |

All three require write scope and completed session MFA. Current organization
role, live application ownership and the release gate are checked before
idempotency replay. The stores recheck write authority and controls atomically.
A disabled gate returns `503 application_standards_pending` without persisting
intent, approval or revocation. Errors omit internal configuration and credentials.

Local intent requires `expected_revision`, a complete `settings` object and an
explicit `additional_log_destinations` array. `{}` and `[]` clear local choices.
An approval requires the current `expected_revision`, adopted `standard_id` and
`version`, one `field` with a non-null `value`, a nonempty `reason`, and a future
`expires_at` within 30 days of server time. Revocation requires the current
`expected_revision`; it retains the original approval and reason. Conflicting
local choices after revocation remain blocked until corrected.

```sh
gregale orgs standards local-intent --org acme --app APP_UUID --file local.json
gregale orgs standards exceptions approve --org acme --app APP_UUID --file exception.json
gregale orgs standards exceptions revoke --org acme --app APP_UUID --id EXCEPTION_UUID --file revoke.json
```

For example, `local.json` can contain:

```json
{"expected_revision": 1, "settings": {}, "additional_log_destinations": []}
```

`revoke.json` contains `{"expected_revision": 3}` using the revision from a
fresh enrollment read. SDK methods are `SetApplicationStandardLocalIntent`,
`ApproveApplicationStandardException` and `RevokeApplicationStandardException`
in Go, their lower-camel-case equivalents on Node's `OrgsService`, and the
corresponding snake-case endpoints in Python's `faas_sdk.api.orgs`.

Successful writes save intent or retained approval history. Changed intent
queues installation and retains the last installed projection; it does not
advance observed revision, release a rollout wave or prove native enforcement.


## Release-gated review approval and rollout controls

Owners and admins can approve a saved preview with its exact `approval_hash`.
Approval rechecks membership, scope, targets, immutable definitions, local intent,
exceptions and artifact inputs atomically. Changed or expired inputs require a
fresh preview; another active operation on the assignment refuses approval.
Publishing remains separate from selecting the admission version. These routes
share the disabled mutation gate above and require write scope and completed MFA.

| Action | Route under `/v1/orgs/{slug}` | Body |
| --- | --- | --- |
| Approve review | `POST /application-standard-reviews/{review}/approve` | `{"approval_hash":"<saved SHA-256>"}` |
| Pause | `POST /application-standard-operations/{operation}/pause` | `{"expected_updated_at":"<current updated_at>"}` |
| Resume | `POST /application-standard-operations/{operation}/resume` | Same exact current timestamp |
| Abort | `POST /application-standard-operations/{operation}/abort` | Same exact current timestamp |

Current role, scoped resource ownership and the release gate precede idempotency
replay. Operator timestamps retain microsecond precision; a stale timestamp
returns `409 application_standard_version_stale`. Read the current operation
before each control. Pausing and aborting fence old worker claims. Abort stops
outstanding targets, preserves installed targets and keeps the forward history.
The assignment retains its admission version until a fresh reviewed change
updates it; abort alone does not change the version inherited by new services.

```sh
gregale orgs standards reviews approve --org acme --id REVIEW_UUID --file approval.json
gregale orgs standards operation pause --org acme --id OPERATION_UUID --file control.json
gregale orgs standards operation resume --org acme --id OPERATION_UUID --file control.json
gregale orgs standards operation abort --org acme --id OPERATION_UUID --file control.json
```

Recreate `control.json` from each fresh operation response. Go clients expose
`ApproveApplicationStandardReview`, `PauseApplicationStandardOperation`,
`ResumeApplicationStandardOperation` and `AbortApplicationStandardOperation`;
Node and Python use their corresponding generated methods.

Rollback is a new assignment change. Stop an active forward rollout, inspect its
saved review request, select the earlier immutable `admission_version`, use the
assignment's current `expected_revision`, and save a new preview. A successful
approval increments the reviewed assignment revision by one. Preserve its
`assignment_id`, scope and standard identity. Review current blockers and approve
the new hash. Deactivating an assignment also uses a fresh preview with explicit
`active:false`; it never deletes retained history. Current platform restrictions,
other inherited standards and resource ownership still apply to rollback.

The public SDK/API fixture exercises two applications, partial installation,
pause/resume, abort, fresh reviewed rollback and retained history. Persistence
leaves observed revisions at zero and the rollback operation waiting. This proves
the control-plane workflow, while fleet consumer convergence, controlled wave
release, crash/restart recovery and dedicated Linux root/KVM acceptance remain open.


## Assignment inventory and fresh revision reads

The organization inventory includes active and deactivated assignments. It is a
read-only view of assignment identity, scope, standard, `admission_version`,
`revision`, `active`, original creator and creation/update timestamps. Reads need
read scope, completed session MFA and current organization membership; they remain
available when the mutation release gate is disabled.

| Action | Route under `/v1/orgs/{slug}` |
| --- | --- |
| List retained assignments | `GET /application-standard-assignments?after=UUID&limit=100` |
| Read current assignment | `GET /application-standard-assignments/{assignment}` |

Pages use ascending assignment UUIDs and a bounded limit of 1–100. Follow
`next_page_after` until absent. Empty pages return `assignments:[]`. Inactive rows
remain visible and keep their revision, identity and original creation metadata.

```sh
gregale orgs standards assignments list --org acme --limit 100
gregale orgs standards assignments show --org acme --id ASSIGNMENT_UUID
```

Go exposes `ListApplicationStandardAssignments` and
`GetApplicationStandardAssignment`; Node and Python expose corresponding generated
methods. Before an update, rollback or deactivation, read the current assignment,
copy its `revision` into the new preview's `expected_revision`, and preserve its
assignment, scope and standard IDs. Preview and approve a new reviewed change.
A stale revision is refused; deactivation increments the revision and retains the
row instead of deleting history.

`admission_version` selects inheritance for new services. Existing applications
keep their saved adopted version until a reviewed target is installed. Read their
enrollment and operation progress separately. Neither inventory reads nor target
persistence advance runtime observations. After a reviewed deactivation, new
services no longer enroll through that assignment, while retained historical
operations and assignments remain inspectable.
