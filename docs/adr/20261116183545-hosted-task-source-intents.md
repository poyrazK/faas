# ADR-20261116183545: Durable hosted Task source intents

Status: Accepted
Amends: ADR-20261116172002, ADR-20261115082509

## Decision

Add hosted `gregale_prepare_uploaded_task_tool_deploy` and
`gregale_get_uploaded_task_tool_deploy` under the existing explicit publishing
and write consent. Preparation requires a prior hosted source-review fingerprint
and a caller-chosen stable intent identity. The first creation rechecks exact
source bytes/modes, declared Task permissions/limits and current personal-account
app/configuration/serving baselines. Named environments and scoped/shared-org
grants remain excluded. The existing planner remains read-only and removes its
staged files; preparation is a separate durable write.

APID alone accepts PUT/GET `/v1/task-tool-source-intents/{id}`. New writes are
default-off behind `FAAS_MCP_HOSTED_TASK_SOURCE_RETENTION=1` and require the
independent Account-domain seal recipients. Source bundles may contain sensitive
authored data: encrypt their complete entry lists with age and bind the envelope
namespace to account, requesting key, intent identity and review fingerprint.
Never persist plaintext source in the bridge token journal, native metadata,
logs or public responses. A native PostgreSQL transaction saves the encrypted
source and immutable receipt together under the account lock, with fresh key
and app locks. This bounded preview uses the existing native journal rather
than introducing a distributed artifact-upload transaction.

Each role permits at most 2 MiB uncompressed bytes and 1024 entries, including
explicit directories. Requests and ciphertext are each bounded to 8 MiB. Permit
two active intents and 16 MiB total ciphertext per account, with at most 256
immutable identities over the account lifetime. These centralized limits are
deliberately smaller than the existing upload/review bounds. Source is retained
for fifteen minutes from the first commit; retries cannot extend that deadline.
The record binds canonical review metadata, source digests, account, requester
and stable identity. Exact repeat requests return the original receipt; changed
reviews conflict. Expired source cannot be reattached under the same identity.
Immutable metadata tombstones remain until account deletion, preventing reuse.

The native API validates normalized root/directory/file paths, modes and bundle
digests independently of the bridge. It rejects traversal, links, git/project
metadata, mixed build markers, missing Node start/Task adapter contracts and
source/config/review mismatches. API source entry content is base64 bytes, not
a host path or executable command. GET projects metadata only, and only to the
original fresh unscoped publisher or personal owner. Revoked keys cannot inspect
or prepare intents. No endpoint downloads source in this slice.

Hosted preparation first looks up the immutable intent. A retry with the same
identity and expected review recovers it after reconnect/restart or a lost reply,
without consuming the old archive upload again. Stack arguments and any supplied
source digest must match the retained review; inline retries also rehash their
bytes. An archive retry refers to the original retained snapshot, not the new
upload ID; use a new intent identity and fresh review for different source.
Temporary bridge source and reservations are removed on every outcome.

Account reads/writes retire expired ciphertext, and APID's service lifecycle
runs a bounded indexed sweep every minute, including when new retention is
disabled. Close cancels and joins this worker. Expiry is enforced in returned
metadata even if cleanup is delayed. Database downtime or backlog can delay
physical ciphertext removal; PostgreSQL MVCC, backups and storage retention
are not a cryptographic-erasure guarantee. Source bytes must never be used
after the fixed deadline. This preview offers no execution path.

Every receipt has `ready_to_apply=false`, `bindings_verified=false` and
`owner_authorization_required=true`. A historical app baseline is not fresh
readiness evidence. Owner decisions, credential binding, build/deploy, Task
migrations, fixture invocation and traffic changes require future separate
authorization flows. Existing local release/recovery authority is unchanged.

## Validation

Verify filesystem/bundle digest agreement, source bounds and unsafe paths,
source/review tampering, sealed namespace and metadata-only responses. Exercise
memory and PostgreSQL atomic preparation, concurrent semantic JSONB replay,
caller isolation, requester separation, quotas, expiry/tombstones and store
restart. Cover both MCP protocols, separate bridge sessions, lost replies,
changed inline retries and absence of CLI/customer-code execution. Preserve
existing planner tests. Live staging and native KVM acceptance remain outside
this source-retention slice.
