# ADR-1628: Request-linked regression evidence through management MCP

## Status

Accepted. Extends ADR-1624–1627 and the native request-evidence contract.

## Decision

Expose a bounded `regression` object in `gregale_get_request_evidence` from the
matching observation already returned by the native API. Reuse the regression
inbox's explicit metric, triage and suspected-dependency projection. No new
query, tool, prompt argument, endpoint, migration or diagnostic permission is
added. App-wide diagnostic consent, immutable app identity, request deployment
ownership/scope and final authorization continue to protect the response.

Require a retained request identity, positive represented count and attributed
deployment. Match the observation's exact original deployment and route to the
native request before redaction, query/fragment removal or truncation; labels
that merely become identical after projection do not establish a match.
Require native regression_detected status and reviewed kind/workflow values.
Validate decimal factor, counts, rates and timestamps using the inbox contract.
Preserve native triage visibility without recomputing it using the bridge clock;
expired dismissals can reactivate, and workflow state is not application health.

Report observed only for valid matched enrichment. Without a row, native
unobserved becomes no_matching_observation, regression_unavailable becomes
unavailable, and missing/unknown native metadata becomes unknown. A claimed
detected regression without a row, contradictory metadata, mismatched identity,
unsupported categories or invalid metrics becomes invalid_native_regression.
Journal-only or unattributed request evidence remains unavailable. Omit the
observation in all these states while preserving the other request evidence.

Observed context preserves baseline/current p95 and error rates, decimal factor,
affected/error counts, detection timestamps, triage state and scoped deployment.
Suspected dependencies remain hypotheses and exclude SQL/query text. Native
finding/recommendation prose and dependency-comparison payloads remain omitted.
Detection timestamps are not sampling-window boundaries. The API supplies no
baseline deployment identity, exact observation window or collection-completeness
proof. A matching route/deployment observation does not establish that this
individual request regressed, that the deployment caused it, or that a baseline
was healthy.

Accept native error_regression and performance_regression findings only with
the canonical regression reference, a valid exposed observation, the matching
signal kind and supporting increased metrics with positive counts. Do not
reimplement detector thresholds or manufacture absent native findings. Use fixed
descriptions and /regression/observation JSON Pointer citations; preserve native
rule confidence and mark these aggregate findings approximate even for a request
row with count one.

The compare_deployments recommendation requires a supported regression finding.
Its fixed description requires two explicit deployment IDs from the user and
does not infer a latest, previous or healthy baseline. Retrieving evidence or
prompts performs no comparison, triage action, replay, collection or rollback.
Extend request/performance guidance with these semantics.

## Consequences

Agents can connect individual request investigation with detector observations
without additional evidence queries. Missing, invalid and unavailable enrichment
remain distinct from health. There remain 38 read/planning tools, 46 with writes
enabled and six prompts. Protocol and pinned real Codex acceptance cover consent,
scope, redaction, resolved local citations and absent/unavailable enrichment,
with zero model requests and native writes. Provider/native deployment
qualification remains separate from portable fixtures.
