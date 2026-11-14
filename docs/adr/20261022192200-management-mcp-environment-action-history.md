# ADR-20261022192200: Management MCP environment promotion and rollback history

## Status

Accepted — 2026-10-22. Extends ADR-20261022161757 and ADR-20261022183756.

## Context

Environment promotion/rollback plans already retain private approval and retry
material, but agents cannot discover their original handles through action
history. These operations can affect every workload in a project. App-scoped
history cannot represent their source/target consent boundary. Human approval
and native promotion execution are separate steps, and a lost response does not
establish failure or authorize a replacement plan.

## Decision

Add read-only `gregale_list_environment_actions` and
`gregale_get_environment_action` with an exact project and target environment.
Bind records to account/API, project ID, source and target environment IDs, and
the original plan ID. Both environments and every current project workload need
read consent. Missing source consent omits the row from lists and withholds its
detail. Revalidate immutable identities and whole-project consent before releasing
results, including sources used to establish pagination continuation. Context-bound
cursors are distinct from app-history cursors and other grants/targets.

Expose fixed `not_attempted`, `approval_pending`, `outcome_unknown` and `accepted`
phases. Approval pending describes the last recorded bridge snapshot. Reads do
not poll browser approval, approve anything, or apply a release. Expose only the
recorded approval requirement/expiry and a checked promotion ID; omit reasons,
preview hashes/tokens, approval credentials, review URLs, tickets, exchange codes,
workload changes and native error/status payloads. Native acceptance never proves
completion or healthy traffic. Exact known promotion references select the
existing `gregale_get_environment_promotion` observation tool explicitly.

An available unexpired plan can point to its original guarded apply tool under
current authorization. Pending human review resumes only through the existing
approval flow. Expired approval or plan metadata never extends review or write
authority. Accepted and uncertain outcomes remain distinct even when the native
rollback response contains a completed status. A rollback retains its exact
original promotion reference before any attempt; a lost new promotion response
does not invent one.

Use additive version-1 `environment-actions/<grant>` records. Load all history
namespaces before reconciling or committing legacy plans, preserving archived
environment records when app history changes. Persist app/environment replay
payloads and all three history namespaces in one encrypted, fsynced snapshot on
every plan/approval/attempt/receipt update. Storage failure prevents native
mutation and withholds uncommitted history. OAuth revocation removes the new
namespace too; refresh preserves it within the same grant.

The existing 256-record connection bound is shared across app, workload and
environment histories, prioritizing every live supported plan over archives.
Retention remains 24 hours after original plan expiry; pages default to 20,
maximum 50, with cursor input at most 1024 bytes, all using `pkg/api/limits.go`.
Merge bounded N-1 histories then prune to the shared bound before serving.
N-1 ignores the additive namespace and can still read its known formats.
Upgrade imports only still-live legacy plans, recording first import time rather
than inventing historical creation time. Older bridges can leave stale snapshots
or gaps once their plans expire; a missing/old record is never proof of no write.
There is no native API change or migration.

## Consequences

Catalog becomes 53 read/planning tools and 62 with existing writes enabled.
Route-budget history remains outside the current profile. Tests cover both MCP
protocols, human approval redaction/passive inspection, exact same-key recovery,
restart, expiry, grant and immutable-identity isolation, membership/consent changes,
paging, shared capacity, old-journal import and failed atomic persistence. Pinned
real Codex acceptance inspects the same project/environment plan after restart
and refresh, without model requests or native deployment writes. Portable fixture
evidence does not qualify native/provider deployments.
