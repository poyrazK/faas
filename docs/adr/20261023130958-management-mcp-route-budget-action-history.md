# ADR-20261023130958: Management MCP route-budget action history

## Status

Accepted — 2026-10-23. Extends ADR-20261022161757 and ADR-20261022192200.

## Context

Reviewed route-budget overrides and inheritance resets already use durable,
revision-guarded native requests. After a lost reply or bridge restart, agents
need to discover the original plan without duplicating a possibly committed
policy change. Policy persistence does not establish improved performance or
completed observation windows.

## Decision

Extend existing `gregale_list_actions` and `gregale_get_action` with
`route_budgets` records for the exact account, app and registered environment.
Expose only the change kind (`set_override` or `inherit_app_policy`) and reviewed
environment/app-policy revisions. A validated receipt records `committed`, the
resulting budget revision, parent app-policy revision (including zero) and policy
source. Preserve `not_attempted` and `outcome_unknown`; a conflict or unbound
native response must never fabricate a committed revision. Restored cached
receipts revalidate the reviewed revisions and policy before replay; corrupted
receipts fail bridge construction. Exclude routes,
thresholds, enabled/configuration payloads, reasons and native request keys.

Reuse the existing guarded apply tool and original native request ID/body.
An ambiguous live plan can suggest that same retry tool, subject to current
write enablement, consent, immutable identity and expiry. Archived metadata never
reconstructs private replay material or extends authority. Reads require current
app/environment consent and repeat identity/consent checks before releasing a
result; route-budget history does not require request-diagnostic consent.

Supply exact app/project/environment arguments for `gregale_get_route_budgets`.
This explicit observation reads current policy, which may have advanced since
the recorded commit. History itself performs no budget follow-up read and always
reports `native_completion: not_observed`. A policy commit is neither a traffic
change nor evidence of better latency/error rates or fresh completed windows.

Store version-1 projections in additive `budget-actions/<grant>` records so
older bridges retain their existing history formats. Load all history namespaces
before reconciling live plans, and commit all replay/history namespaces in one
encrypted, fsynced snapshot before mutation and after the native receipt. Failure
prevents mutation or withholds uncommitted history. OAuth refresh preserves the
grant; revocation removes its budget history too. Upgrade can reconcile a live
legacy plan, but cannot reconstruct missing expired history.

Share the existing 256-record connection bound across all action families,
prioritizing live plans. Retention remains 24 hours after original plan expiry;
page size remains 1–50 (default 20), with context-bound cursors of at most 1024
bytes. Limits remain in `pkg/api/limits.go`. No new native endpoint, migration,
mutation permission or tool is introduced.

## Consequences

Catalog remains 53 read/planning tools and 62 with existing writes enabled.
Protocol tests cover override/inheritance receipts, zero parent revisions,
redaction and exact observation arguments. Recovery tests cover encrypted restart,
same-request replay, expiry, consent/identity changes, environment/app revision
conflicts, namespace compatibility, shared capacity and OAuth cleanup. Pinned
real Codex acceptance checks the same budget plan across restart and refresh
without model requests or native writes. Portable fixtures do not qualify native
or provider deployments.
