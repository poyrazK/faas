# ADR-2026101706: Source-backed repair history and read-only recovery

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [replacement](2026101702-repair-replacement.md), [resolution](2026101703-repair-apply-resolution.md), [retention](2026101704-repair-superseded-retention.md) and [verification](2026101705-repair-post-apply-verification.md)
- Scope: source metadata, standalone/hosted APIs, CLI and review screens. No control-plane schema, admission, approval or VM lifecycle change.

## Decision

Discover every retained plan for an authenticated source operation, including
unapplied, closed and replaced plans. Source discovery previously exposed only
committed-repair counts and the latest receipt. An operator with only the bad
import's UUID otherwise needed an out-of-band saved plan UUID to recover its
uncommitted or superseded attempts.

The source database owns the history. Return bounded metadata: original plan
ID/digest, creation/expiry, frozen candidate/conflict/unavailable counts,
rehearsal time, source approval actor/time, replacement parent/comparison and
forward link, explicit apply closure, committed repair UUID/time and payload
purge time. Do not return row identities/keys, before/after/observed values,
business SQL, check evidence or full plan documents in history pages.
Extract only allowlisted summary fields after a materialized bounded selection.
Do not hash every document or inspect business rows/journals during discovery.
History provides navigation and recorded lifecycle facts, never a substitute
for the existing digest/version checks at an action boundary.

Use the existing discovery budgets: default 25, maximum 100 plans, 512 KiB
response and 1 KiB cursor. Order by descending creation timestamp and UUID,
using keyset continuation. Bind the cursor to the complete authenticated scope,
source UUID and normalized page size. Hosted cursors additionally bind the
configured executor generation/origin. Cursors are navigation hints, not
credentials. Listing is live; facts can change between pages, and newer plans
above an existing boundary are found by starting another listing. A known
finalized source with no plans returns an empty page. A source outside the
authenticated scope returns not found, even after its payload has been purged.

Keep lifecycle facts separate. No committed receipt or closure means the apply
outcome is unconfirmed; neither plan expiry nor absence of a receipt proves
rollback. A replacement fence, source approval and payload marker are distinct
facts and do not create a new approval. History never creates a plan, records
evidence, refreshes expiry, runs checks, reconciles an Operation or retries an
apply.

## Hosted retention and read authority

Enrich source items with optional hosted execution context only when the
account/app/environment/customer, plan ID/digest, source UUID and binding match
the retained Operation. Return its actual phase/state separately from the
source receipt. A committed source receipt may legitimately coexist with an
`outcome_unknown` hosted phase. Missing or expired hosted rows, including the
interval before asynchronous pruning, are `not_retained`; a
mismatched identity/binding is `unavailable`. Neither exposes a phase or grants
authority. The source never supplies hosted execution metadata.
Capture discovery also treats an expired hosted source Operation as
`not_retained`, preserving metadata navigation without offering preparation.

Add source-scoped hosted plan reads and receipt verification which depend on
the current exact configured source binding and ordinary account authorization,
not on a retained source or repair Operation. They return the retained plan and
execution availability without reconstructing a writable hosted Operation.
Verification binds its report to that exact source plan and committed receipt,
using the existing read-only verifier. It cannot settle an uncertain hosted
outcome. These routes are read-only even when hosted execution context exists;
all existing action routes still require their retained Operation, binding,
sequence and approval protocol. No write, admission or approval fallback is
provided after hosted retention.

History and retained plan reads require the existing PostgreSQL read/admin
permission and MFA. Hosted verification additionally enforces same origin and
accepts only the digest in its body. Environment/customer select one configured
binding; arbitrary account/app/scope fields and ambiguous selectors are refused.
Trust boundaries validate scope/source identities, counts, receipts, lineage,
ordering, page/byte limits and execution availability. Redirects are not followed.

## Operator surfaces and installation

Expose `data-repair history SOURCE_UUID` in standalone and hosted modes. Hosted
`get` and `verify` accept `--source SOURCE_UUID` and optional configured
environment/customer selectors for read-only recovery after ledger expiry.
Standalone uses its existing exact plan reads and verification. Neither CLI
discovers a credential destination from a returned URL nor retries requests.

Both review screens list history independently of preparation eligibility and
payload availability. Operators can load a plan and its parent/replacement,
and explicitly open a retained hosted Operation. History inspection in both
screens disables every write action while permitting receipt verification.
Standalone inspection does not generate an apply UUID for an unresolved plan;
opening the normal plan workflow is an explicit action. Browser navigation keeps
earlier standalone apply URLs reachable and carries a known apply UUID when
inspecting that same plan. Links and
requests use saved IDs on the configured origin. Scope/token changes discard
history results and late responses. Render metadata as text; keep credentials
out of URLs/storage. Reloaded hosted historical links preserve source, plan and
configured environment/customer, and make only read requests.

Fresh schema and repeatable explicit owner upgrades install the same stable
read-only function and source/scope/creation index. Constructors and handlers
never install DDL. PUBLIC table/function access remains revoked. The function
uses a fixed catalog search path and the existing application-owner SQL
exception; it does not add control-plane SQL or maintenance authority. Reader
roles need EXECUTE on the new function and SELECT on retained operations/plans.

## Verification

Portable tests exercise 500 recorded updates, 40 human edits, an approved
460-candidate plan, another human edit, a failed apply, explicit closure and a
459/41 replacement with fresh approval and a committed receipt. Starting with
only the source UUID, recover both plans, their digests, closure, replacement
links and receipt after payload compaction and hosted result/event expiry.
Verify 458 unchanged records and one later edit without another apply or
recreating a hosted ledger. Journal purge remains explicitly unavailable.

Other coverage includes empty/unknown sources, all scope dimensions, tied
timestamps, interleaved new plans, page-size/source/scope/binding cursor
changes, explicit owner upgrades, read-only SQL execution, read-only principal
access, MFA/origin/body guards, oversized and malformed remote metadata,
unresolved hosted outcomes, CLI origin/identity checks, safe text rendering,
stale responses and historical browser reload/verification with writes disabled.
