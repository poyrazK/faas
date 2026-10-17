# ADR-1472: Explicit native Linear lifecycle sync

Date: 2026-10-14

Status: Accepted

## Context

Native Linear creation uses a verified owner-scoped personal API key. Explicit
lifecycle synchronization still required a local key and lacked server-side
connection and group guards across preview and apply.

## Decision

Expose GET/POST `native-linear-status` on a stored Linear tracker reference. Keep
the existing native GitHub API and DTOs unchanged. Native Linear operations require
app ownership, Issues entitlement and deploy eligibility. Reads require app read;
pushes additionally require deploy-write and session MFA. Reuse the sealed-key
accessor and current workspace/viewer/team verification from native creation.

Return a credential-free preview containing the exact provider status, connection
revision and group fingerprint. Push requires that observed scope and a desired
open/resolved lifecycle matching Gregale. Refuse changed revisions or group
membership/rules, including changes preserving the canonical root UUID. Recheck
scope after obtaining credentials and after the provider response.

Require an explicit workflow-state UUID for a changing Linear lifecycle. The
provider client verifies the state is active, belongs to the linked ticket's team
and maps to the desired lifecycle, then verifies the mutation response. Preserve
the provider's exact snapshot check and archived/canceled/unfamiliar-state
protections. Aligned lifecycles remain unchanged without selecting a workflow.

Extend `issues tickets sync --native` to stored Linear references. Preview remains
the default; push uses `--linear-state`, and pull resolution still requires an
explicit fixing deployment. The CLI retains connection/group preview scope across
the second native read before a pull; changed scope aborts the local action. Native
mode rejects local credential flags. No automatic Linear reconciliation is enabled.

## Limits

Provider and Gregale do not share an atomic transaction. A competing edit can occur
after the final observation, and a dispatched mutation cannot be recalled. A scope
change detected after a provider write returns conflict; preview again to observe
the outcome. Pull uses the existing Gregale action API and does not atomically lock
the provider together with that action. OAuth and automatic inbound/outbound Linear
reconciliation remain separate increments.

## Validation

Domain race tests cover explicit workflow selection, stale provider snapshots,
aligned no-ops, revision/group/state changes, credential/read/write races and
sanitized unsupported/provider errors. PostgreSQL API tests exercise sealed keys,
reporter denial, preview fences, workflow requirements, stale updates, changed
connections, foreign teams, unsupported states and disconnect. CLI contracts cover
preview, explicit push, fixing-deployment pull, changed-group refusal and rejection
of local credentials. Go/Node/Python contracts preserve preview guards and selection.

## Automatic reconciliation prerequisite

The provider exposes read-only verification of a selected workflow state, reused
by explicit pushes. The Linear domain service verifies distinct open and resolved
workflow UUIDs against the connected team before returning a native snapshot. It
rechecks the connection and issue group between checks and after the final issue
read. Invalid, missing, archived, foreign-team and unfamiliar workflow states are
rejected without issuing a mutation; provider diagnostics remain sanitized.

This is an internal prerequisite, with no new public route or automatic policy.
Automatic reconciliation still needs durable workflow mappings, connection-revision
pinning, exact pending workflow-state recovery, policy configuration and worker
integration. GitHub's existing automatic policy remains unchanged.
