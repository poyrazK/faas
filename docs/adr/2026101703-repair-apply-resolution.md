# ADR-2026101703: Authoritative repair apply resolution

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Decision: Resolve an exact apply identity at its original source database. Return its atomic commit receipt or permanently close the plan under the apply lock and retain a durable `not_committed` result.
- Scope: Existing declared PostgreSQL field updates, hosted Operations, executor API, CLI and review screens. No native-host or VM lifecycle changes.

## Source proof and irreversible closure

A row conflict, failed live check, failed approval forwarding, or lost response
can leave the hosted outcome unknown. Neither an error response nor absence of
a visible receipt proves that a prior request rolled back or cannot still run.
Read-only reconciliation remains an observation and cannot grant replacement
authority on that basis.

An explicit `resolve` mutation binds the source scope, immutable plan ID/digest
and original repair operation ID. Its owner-installed SQL function takes the
same scope lock and plan row lock as apply. A busy row returns unresolved using
`NOWAIT`; it does not cancel, replace, or declare the in-flight transaction dead.
An existing applied plan returns its original matching receipt, including after
expiry or payload compaction. A receipt for another repair identity conflicts.

With the plan locked and no committed receipt, the function atomically records
`apply_resolution` outside the frozen document. It contains `not_committed`,
the plan digest, original repair identity, resolution time and authenticated
actor. This closure commits before success is returned. Apply, approval,
rehearsal publication and failed-check publication reject a closed plan. Thus
a delayed old request cannot commit after the absence of an earlier commit has
been established. Closure performs no business-row reads or writes, checks,
capture, approval reset, expiry refresh or rollback of external effects.

Closure is irreversible. Same-identity replay returns the original metadata,
including its original actor and timestamp, even after replacement. A new
repair ID conflicts. Committed receipts and closures are mutually exclusive.
A lost closing COMMIT response is unresolved until a repeated call or source
read recovers the durable result. Constructors never install or upgrade SQL;
owners explicitly apply `data-repair schema --upgrade`.

## Hosted authority and recovery

The management action requires `postgres:manage` or admin, normal MFA and
same-origin checks, the exact plan digest and initial sequence. Its closure actor
comes from the authenticated requester; its repair identity comes from the saved
Operation. Neither comes from caller-authored outcome fields. A committed
receipt must still match the original approval actor. Executors without this
additive contract cannot supply proof.

A CAS transition from `applying` or `outcome_unknown` to `resolving` happens
before contacting the exact saved executor binding. Apply retries and
replacement cannot proceed while resolving. Unavailable/busy sources and lost
responses leave the reservation in place. There is no elapsed-time release.
Repeating the action for the same Operation/digest may use its original
sequence. Receipt identity, scope, digest and original approval actor must
match before projecting success; a source closure must bind the exact plan and
repair identity before projecting `closed` with `apply_closed_at` and failure
code `repair_not_committed`. A source read can finish an already reserved
resolution, but a negative lookup cannot finish it.

The reservation and its events survive ordinary retention and appear in repair
health with the existing execution timeout. On settlement the usual bounded
result/event retention starts again, preserving access to a late recovered
outcome without extending source plan expiry. Account/app deletion remains its
separate owner action. A repeated settled resolution adds no events. Durable
closure replay remains available during and after linked replacement.

A closed parent can enter the existing linked replacement workflow. Original
history remains intact; the new candidate set can only narrow and requires its
own rehearsal and approval. Successful applies cannot be replaced. Source
payload retention keeps its hold until a replacement is saved and meets the
separate [reviewed compaction contract](2026101704-repair-superseded-retention.md);
closure alone does not authorize compaction or change saved retention selections.

## Operator controls and verification

`data-repair resolve` requires the exact digest and `--yes`. Standalone use
requires the original saved `--repair-operation`; hosted use derives that UUID
from the Operation. Both review screens explain that an uncommitted plan will
be permanently closed. The standalone screen saves the apply identity in the
page URL before submission and reloads it without persisting credentials or
business data. Existing strict guest execution headers omit resolution metadata.

Portable checks exercise row conflicts and live-check rollback, source
commit/rollback races, in-flight refusal, delayed old applies, lost source
closure responses, same-identity replay, committed receipt recovery after
payload compaction, scoped APIs, role enforcement, hosted reservations,
retention and health. The CLI journey closes the failed 460-candidate attempt,
then rehearses and approves a new 459-candidate plan and preserves 41 edits.
These checks establish the resolution contract without changing native runner
qualification or production activation requirements.
