# ADR-602: Global app directory and verified regional bindings

Date: 2026-10-03
Status: Accepted; implementation and qualification in progress.

## Context

ADR-601 gives independently operated regional cells immutable database
identities. Accounts, API keys, app UUIDs, quotas and deployments remain local
to each cell. Forwarding a directory credential to another cell cannot prove
ownership there. Matching emails or slugs cannot establish account identity.

The multi-region product needs one durable app identity with multiple regional
targets. The user-selected default serving policy is **all selected regions**,
rather than an implicit primary region. The directory is the first part of
that model; it must not advertise an unverified target as a running deployment.

## Decision

`apid` owns a global app directory in a separate, unbound control-plane database.
The existing account authentication chain authenticates directory requests.
An explicit `global_app_directory_enabled` configuration gate defaults to false;
enabling it against a regional cell database fails startup. Regional databases
reject directory writes at the SQL boundary. Existing regional and unbound
deployments retain their current APIs.

Directory apps have a stable UUID, globally unique canonical slug and owning
directory account. Their initial routing policy is `all_selected_regions`.
Registering an identity creates no VM, deployment or resource entitlement.
Registration is bounded by the account plan's existing app-count limit; runtime
quotas remain enforced by each regional owner until global allocation exists.

Operators register immutable cell UUID/region pairs with a canonical HTTPS API
origin and an explicit enabled policy. Endpoint or policy changes increment a
generation. An app has at most one binding per selected region, and a regional
app UUID in a cell can belong to only one global app. The directory stores
regional account and app UUIDs, not guesses based on email or slug.

To attach an existing regional app, the caller authenticates to the directory
and supplies a regional account-admin API key for a one-request ownership
check. The directory contacts only the operator-registered HTTPS origin, never
follows redirects, and limits the response. The regional `apid` validates its
normal bearer authentication and admin scope, then returns the verified cell
identity and the requested live, personally owned app identity. Apps owned by the caller's active personal organization are accepted alongside
legacy personal rows. Shared organization apps and non-personal organization
keys need an explicit global-org authorization design before attachment.
The directory checks the returned cell UUID, region and slug before writing.
It establishes an immutable account mapping for that cell, with reverse
uniqueness so two global accounts cannot claim the same regional account.
The Go, Node and Python SDK attachment transports also refuse redirects when
sending the regional key to the directory. The supplied credential is not persisted, audited, returned or included in
diagnostics. Authentication replication is not introduced by this protocol.

The registry generation is rechecked atomically with attachment. Repeating the
same binding is safe and refreshes its verification; replacing its regional
identity conflicts. Endpoint changes invalidate previous verification for
resolution until the same identity is checked again. Disabling a cell prevents
attachment and resolution. Account-scoped reads hide other owners' identities.
Resolution returns an explicit selected region's API origin and regional app
identity; it neither guesses a region nor forwards a global credential.

## End-to-end work after the directory

The complete multi-region feature still requires regional provisioning and
multi-target deployment orchestration; shared account/org authorization;
generation-fenced global quota allocations; global ingress publication with
cached regional routing and health; explicit managed-data/secret replication
policies; and independent regional recovery qualification on native KVM.
All selected, healthy and eligible regions must serve the global app when
ingress is implemented. A regional outage must not require another region's
database for an already published route or wake. Directory availability may
gate new global changes, but must not become a per-request runtime dependency.

Existing-app attachment is an internal adoption path toward that final model,
not a replacement for global deployment orchestration. Bindings express
verified identity, not deployment readiness, capacity reservations, shared
billing, data residency, or automatic cross-region recovery.

Regional deletion or slug reuse is not observed by this directory slice. Future
regional mutation and publication protocols must fence the stored regional app
UUID atomically; a slug alone cannot authorize changes to a replacement app.
Global identity deletion needs durable withdrawal/tombstone semantics before
published ingress and global resource allocations can be released safely.

## Validation

Portable and private-PostgreSQL tests must cover immutable identity, quota and
ownership races, reverse uniqueness, both registry/attachment lock orders,
endpoint-generation invalidation, SQL bypass attempts, regional-cell refusal,
idempotent same-identity attachment and account-scoped lookup. HTTP tests must
exercise two independently authenticated cells, a separate directory, wrong
cell/region/slug responses, revoked/read-only keys, malformed/oversized
responses, redirects, disabled cells, and absence of credentials in stored or
returned data and diagnostics. The public Go SDK and operator/customer CLI
must exercise the real directory and regional proof endpoints. VM lifecycle
and global ingress acceptance remain separate required gates for completion.
