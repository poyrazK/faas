# ADR-1667: Coordinated SQL refresh across credential rotation

Status: Accepted

## Context

ADR-1735 automates deadline-only grants but excludes coordinated SQL refresh.
Its legacy pause/fingerprint gate belongs to one login. Copying that gate during
credential preparation cannot preserve pause state while the predecessor still
serves: preparation commits before secret publication and runtime activation.
The existing binding rollout already retains the predecessor until the scheduler
confirms rolling refresh. New protection must span that overlap.

## Decision

Add explicit owner enrollment through `refresh-rotation.mjs` for managed Neon
`data_api` bindings using `api`. Require the stable managed schema owner, binding
UUID/resource marker, matching installed refresh hooks and optional deadline
contract. A binding must remove its previous deadline-only approval first.
Record a private binding gate with shared pause/token state and a source-login
pointer. Record each member's role name/OID, binding/resource scope, fingerprint,
catalog witness and approved RPC OIDs in a second owner-only table. Both tables
are permanent, without RLS/inheritance, and have fixed ownership, shape and keys.
This is owner-installed customer SQL under ADR-650, not a catalog migration.

Upgrade only the known legacy or matching current hooks. The two serving functions
remain protected `SECURITY DEFINER` functions with `search_path=pg_catalog` and
no public execution. They use a stable binding advisory lock and shared pause
state for enrolled members, returning/checking that login's own fingerprint.
Unenrolled legacy roles retain their per-login gate path. Public status fields and
claims are unchanged. Enrollment takes the existing schema-owner lock, exclusively
drains the legacy lock, installs the shared mapping and removes the legacy row
atomically. Already queued legacy hook calls therefore fail closed. The old
installer refuses to overwrite rotation hooks after this upgrade.

Install two additional owner-only, pinned invoker functions: a catalog witness
and candidate preparation. Hash ordered structural catalog metadata plus effective
schema/relation/column/type/function privileges and applicable RLS policies.
Exclude raw ACLs and role identities so granting an equivalent replacement and
ordinary DML/statistics do not change the witness. Include definitions, comments,
types, defaults, constraints, views and triggers needed for the normalized runtime
contract. This is deliberately conservative: unrelated DDL can require owner
re-verification. No catalog metadata or SQL bodies are returned publicly. The
original fingerprint comes from the existing normalized runtime inspector; the
provider compares witnesses instead of duplicating its JavaScript format.

The inspector now accepts an explicit privilege role consistently for columns,
relationships, functions, views and row metadata, as well as its existing type
checks. Owner verification does not need to assume a serving role or grant it
schema-owner memberships. Serving inspection retains current-user behavior.

Neon prepares an enrolled candidate inside its existing role transaction, before
returning credential material. Explicitly assume the stable schema owner before
private lookup. Validate and lock table metadata, then verify all four exact body
hashes, signatures, language, security/configuration and protected ACLs. Tests pin
the hashes to the embedded installer. Validate optional deadline hooks through
ADR-1735's fixed contract. Preparation exclusively locks the binding, verifies its
source role/OID and unchanged approved witness, grants only fixed refresh/optional
deadline hooks and owner-approved supported RPCs, and requires the candidate's
witness to match before registering its fingerprint and advancing the source
pointer. RPC OIDs must still identify managed-owner invoker functions in `api`
with the exact `@gregale:rpc` annotation.

A paused binding may accept a candidate whose approved witness still matches.
It shares the existing pause rather than copying it. DDL/permission drift prevents
publication until owner verification. Failure rolls back this attempt's candidate
creation, grants and membership changes; the binding service publishes no
replacement secret and does not revoke the predecessor. Retry uses the same
generation. Absent matching enrollment preserves existing manual behavior.

Route existing pause/resume/check commands to shared state for enrolled members.
Pause drains all generations. Resume requires the owner token and supplied
fingerprint for the requested login, then re-inspects every remaining member and
records its actual permission-filtered fingerprint, witness and approved RPC set
before unpausing. Members may have different verified fingerprints. A stale
runtime is refused by SQL fingerprint enforcement. Read-only setup/enrollment
checks do not install, adopt, repair, pause or resume state.

Use the existing scheduler rollout and retirement boundary. Revoke first disables
login and terminates sessions, then removes only that generation's verified mapping
under the shared lock. Keep the gate and its pause while another member remains;
remove binding state when the last member retires, including deletion. Preparation
and revocation remain retryable. No direct scheduler calls, new serving pool,
per-request catalog witness, new quota, SDK replay or hosted deployment is added.

## Consequences

Enrolled coordinated Data APIs retain pause protection through credential rotation,
including overlap with a migration. Owners continue using coordinated verification,
keep the recorded deadline mode consistent, update images/tools, and use new
enrollment for additional managed bindings after the database-wide hook upgrade.
Owner edits after validation remain an operational concern; this does not sandbox
owner SQL or prove universal cancellation. Other backends, exposed-schema layouts
and unenrolled bindings retain their setup. Local acceptance is separate from
hosted/provider rollout.

## Validation

Native PostgreSQL/PostgREST tests cover legacy admission drain and queued rejection,
two runtime generations, pause racing candidate preparation, both users' RLS,
protected state/helper access, approved RPC preservation and unapproved RPC denial.
Schema drift rolls back a candidate; owner verification permits the same login to
resume. Per-login fingerprints are independently checked after permissions diverge,
and stale runtimes are rejected. The actual owner command runs through an ordinary
migration login without serving memberships. Provider tests exercise both deadline
modes, preparation replay, security/table/identity/RPC/witness drift, owner repair,
old-session retirement, shared-pause preservation and last-member cleanup. Existing
binding-service publication/retirement, legacy coordinated refresh and SQL deadline
tests remain regression coverage. Null metadata is invalid, including missing
function search-path settings.
