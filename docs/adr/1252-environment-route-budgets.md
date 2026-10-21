# ADR-1252: Independent advisory environment route budgets

## Status

Accepted. Extends ADR-1248 and ADR-1251.

## Decision

Store optional advisory budgets for each app and immutable registered environment
(default is an explicit separate scope). Absent overrides inherit saved app budgets;
explicit inheritance retains a monotonic override revision and its observation reset
anchor. Environment overrides specify enabled state and exact normalized routes with
absolute error-rate and latency budgets. Customer grouping, pooled history, incidents,
automatic rollback and canary/release gate intent remain outside this API.

Only apid writes scoped monitoring intent. Native GET/PUT require one explicit scope
selector and normal ownership, MFA and read/deploy-write credential scopes. PUT checks
app, project/environment IDs and both override and parent app revisions under native
account/app/environment locks. Enabling requires request telemetry entitlement and
account admission. Durable account-bound request UUID receipts bind the exact body and
scope, returning the original result after lost responses or later edits. Recreated
app/environment identities cannot inherit old intent or receipts. Every accepted save
increments the override revision and requires fresh completed observation windows.

Scoped evaluation selects effective policy and its source in the same read-only
snapshot as the current scoped serving generation. App-level production worker
configuration and recovery behavior remain independent. Default-scope overrides also
apply only to explicit scoped evaluation. Monitoring configuration remains local to
the registered environment; release promotion does not copy it. Clone schema inventory
classifies policies as configuration and receipts as operational; classification alone
does not claim clone capture or promotion support.

Expose MCP get, plan and opt-in apply tools. Plans expire in 15 minutes and share the
existing bounded plan store. Pin account/profile/API, immutable app/environment IDs,
current effective budgets and both revisions. Persist exact proposal and attempt before
writes; encrypted bridge restart and native receipts recover original outcomes. Consent
is checked again even for cached results. Plans and reads never mutate customer intent.

## Consequences

Staging and production can use independent thresholds while inheritance remains the
default. Loosening these advisory thresholds cannot bypass release gates or trigger a
recovery action. Conservative parent revision guards also reject a plan for an override
when the app policy changes. Memory stores support intent and replay for portable tests;
production evaluation still requires attributed PostgreSQL telemetry.
