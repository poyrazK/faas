# ADR-20261117135916: Hosted Task binding qualification

Status: accepted

## Context

Hosted Task source retention and owner review pin source, policy and app state.
They deliberately provide no credential or execution authority. Owners need a
bounded way to inspect the next setup boundary without giving agents plaintext
secrets or an apply path.

## Decision

An independent, default-off APID opt-in,
`FAAS_MCP_HOSTED_TASK_BINDING_VERIFICATION=1`, permits personal owners to select
four existing native managed PostgreSQL secret references for an approved hosted
source intent: web and worker runtime, worker migration and observer. Selection
also names the exact intent/review digests and queue namespace. The original
source deadline remains fixed at fifteen minutes. Selection is immutable per
source identity; changing it requires a new source review and owner decision.

APID derives owner-key, previous-key, payload-ring and namespace configuration
from the exact retained Account-encrypted source, without evaluating customer
code. Runtime secrets require account-bound runtime envelopes. Native catalog
records must identify one ready account-owned database, default app scopes,
read_write runtime bindings, a distinct migration binding and a read_only
observer binding. Credential generations, ciphertext hashes, native versions,
owner/requester authority and app baselines are pinned. PostgreSQL rechecks real
key expiry after lock waits and in the journal write predicate. Actual database
errors remain errors; known drift becomes terminal stale evidence.

A separate bounded journal row, at most one per existing retained identity,
preserves compatibility with older source-journal writers. It contains opaque
native pins and references, never source or credential values. It is operational
state excluded from project environment cloning. Account/source deletion
cascades to qualification metadata; source expiry still removes ciphertext.

A private UUID claim permits exactly one probe attempt. Concurrent retries and
process restarts cannot repeat or take over it. There are at most four shared
private workers, one SQL connection at a time, a fifteen-second total budget,
five-second statement/connect limits and one-second independent cleanup.
Production connections require public destinations, whole-answer DNS validation,
pinned IP dialing, verified TLS hostname identity and no host PGOPTIONS or client
certificate inheritance. The target schema is read with qualified builtins, then the transaction lookup path
is restricted to pg_catalog so tenant types/operators cannot shadow probe SQL.
Fixed SQLC catalog queries run in read-only transactions;
no customer function, migration, source, task operation or data mutation runs.

Checks cover namespace, configured owner/previous key agreement, configured
payload-ring agreement, database/schema identity and role ACL separation.
Observer checks reject write privileges, unsafe memberships, CREATE/TEMP and
executable customer SECURITY DEFINER functions. Passing is a historical catalog
observation: it does not authenticate retained payloads, prove migration/schema
compatibility, prove VM connectivity or authorize execution. Evidence expires
within one minute of completion, clamped to source expiry. GET never re-probes;
it may persist permanent stale/revoked/expired bookkeeping after fresh native
checks. Rotation, source drift, owner/requester revocation and app changes cannot
revive old evidence.

Owner API/CLI actions select, check and revoke. Revocation and metadata reads
remain available with new admission disabled. Agents receive only a consent-gated
metadata getter. Public receipts exclude source, credential values, references,
driver errors and database facts. Every receipt retains
`ready_to_apply=false`, `bindings_verified=false`,
`owner_authorization_required=true`. Execution requires a separate future owner
decision and protocol.

## Validation

Memory and PostgreSQL regressions cover immutable selection, one-time claims,
concurrent retries, restart fencing, authority, drift and metadata secrecy.
Isolated PostgreSQL role tests exercise actual fixed SQL and reject observer
writes, schema CREATE, migration membership and SECURITY DEFINER execution.
API/SDK mirrors, deterministic MCP schemas, CLI help and regeneration are checked.
Native KVM and live staging are outside this change.
