# ADR-20261022155043 · Rehearsal evidence follows the run

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-202610171904](202610171904-organization-resource-lifecycle.md), which deferred the review of rehearsal records
- **Decision:** Rehearsal policies, journals and evidence no longer block account erasure. They are erased with the identity, like the run and preview app they belong to. A fixture that still holds a database or a bucket blocks erasure until its cleanup finishes.
- **Why:** Any rehearsal row in a company organization blocked its creator's erasure. That included completed evidence, which nothing ever deleted on its own. One rehearsal run was enough to stop a former member deleting their account: `409 Company app ownership migration required`, with no way to clear it but support.

## Context

ADR-202610171904 left rehearsal policies and fixture and runtime journals unreviewed. It listed their account references in `org_app_identity_erasure_blocked`, so any of ten `scenario_*` tables blocked erasure. That covered completed fixture evidence too.

The review shows the evidence already has a bounded lifetime:

- Only a session preview can enroll in a rehearsal (open, unexpired, not a PR preview).
- Every `scenario_*` row cascades from its app, its account or its run's action policy.
- An expired preview goes to the janitor, then the seven-day grace period, then permanent app deletion, which removes the evidence.

None of this evidence is company configuration that must outlive its creator. A run reports to the people who ran it, and its receipt is already returned to the caller. Holding the identity for it protects nothing.

Live fixtures are different. A fixture in `provisioning`, `ready`, `sealing` or (PostgreSQL only) `failed` still owns a database and login role, or a bucket namespace, on a backend. The rehearsal reapers drop those, then mark the journal `complete` or `deleted`. Erasing the identity first would cascade the journal away and orphan the backend resource.

## Decision

`org_app_identity_erasure_blocked` drops these tables from its account inventory:

- `scenario_action_snapshots`
- `scenario_managed_work`
- `scenario_object_fixtures`
- `scenario_postgres_faults`
- `scenario_postgres_fixtures`
- `scenario_suspend_resumes`
- `scenario_test_action_policies`
- `scenario_test_chaos_matches`
- `scenario_test_members`
- `scenario_worker_faults`

It adds one check in their place: a `scenario_postgres_fixtures` or `scenario_object_fixtures` row for the identity whose status is not `complete` or `deleted` blocks erasure.

- The check runs where the inventory ran: after the early return for identities with nothing in a company organization. Personal-only identities were never blocked and still are not.
- The in-memory store applies the same rule.
- The fixture delete guards (`scenario_postgres_fixture_delete_guard`, `scenario_object_fixture_immutable`) are unchanged. They already let terminal journals cascade and refuse live ones, so a raw account delete with a live fixture still fails.

The migration is expand-only: it replaces the function body, and its down step restores the previous one.

## Consequences

- A former member who ran rehearsals can be erased once their fixtures are cleaned up. The reapers bound that wait: fixtures expire after 14 minutes and cleanup runs even with admission disabled.
- Completed evidence goes with the identity. A team that wants to keep a receipt keeps the JSON report `gregale test rehearse` already returns.
- A fixture whose backend drop keeps failing still blocks erasure, as it should: the backend resource still exists. Abandoning such a fixture by operator decision is out of scope. Today the drop must succeed.
- Age-based retention of evidence is not added. Evidence lives exactly as long as its preview app.
