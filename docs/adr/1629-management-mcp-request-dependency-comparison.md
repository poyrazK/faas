# ADR-1629: Request-linked dependency deployment comparisons through management MCP

## Status

Accepted. Extends ADR-1620 and ADR-1625–1628, using the native ADR-958 comparison.

## Decision

Expose `dependency_comparison` in `gregale_get_request_evidence` from the optional
native request-evidence enrichment. Reuse the explicit historical dependency
metric projection, excluding query patterns/SQL, native tags/commits and raw
spans. No new tool, prompt argument, endpoint, migration or evidence query is
added. Bound the projected dependency prefix to ten records in
`pkg/api/limits.go`, matching the current native per-request bound.

Require retained request identity, positive represented count and an attributed
deployment. Match the comparison's original current deployment and exact route
to the native request before text redaction or truncation; require a distinct,
bounded valid previous deployment identifier. Validate the exposed metric prefix
with the existing dependency contract. Invalid enrichment is omitted independently
of the remaining request evidence.

The existing request deployment ownership/scope lookup covers the current side.
For a valid comparison, additionally look up the native-reported previous
deployment, require its exact identity and app ownership, and pin its scope.
Recheck both deployments' ownership/scopes and immutable app identity before
serialization. Ownership/API failures remain tool errors; no comparison data is
released after a failed check. App-wide diagnostic consent authorizes this
request evidence across environments without granting other environment/log
tools or writes. Missing or invalid enrichment requires no previous-deployment
lookup.

Preserve the native-selected pair; MCP does not independently infer a previous,
latest or healthy baseline. Native selection uses retained observed deployments,
so previous need not be the immediately preceding release. Return observed,
partial, no_retained_dependencies, unavailable or invalid_native_comparison, with
explicit native/projection/text truncation and source_completeness unknown.
A nil native comparison cannot distinguish missing history from best-effort
enrichment failure; do not manufacture a more precise availability reason. The
API supplies no exact comparison window or per-side sample counts. False
truncation flags do not establish complete capture or an exhaustive native read.

Support native dependency_regression and dependency_failure_regression findings
only through a unique canonical native dependency label matched to an exposed
row, with the corresponding native flag, positive supporting counts and increased
metrics. Reproduce native label formatting only internally, before redaction;
never export these reference labels. Ambiguous identities, collection truncation,
absent native findings or contradictory support suppress the finding. Native
thresholds are not reimplemented and no diagnosis is manufactured. Use fixed
descriptions, uncalibrated native confidence, aggregate approximation and local
/dependency_comparison/dependencies/N JSON Pointers. SQL/query data never supports
a finding across this projection.

Support inspect_dependency_change only with one of these approved findings,
preferring failure evidence. Fixed guidance grants no additional reads or writes
and triggers no collection, comparison, replay, triage update or rollback.
Extend request/performance prompts to distinguish deployment comparisons from
time-split trends, individual request timing and causal conclusions.

## Consequences

Agents can inspect dependency changes alongside request and route-regression
evidence while preserving native selection, sampling and availability limits.
There remain 38 read/planning tools, 46 with writes enabled and six prompts.
Protocol and pinned real Codex coverage exercise consent, both ownership/scope
pins, redaction, local citations and missing enrichment with zero model requests
and native writes. Native/provider deployment qualification remains separate from
portable fixtures.
