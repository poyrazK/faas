# ADR-1625: Structured request explanations through management MCP

## Status

Accepted. Amends ADR-1618's complete exclusion of native explanations, while
retaining its exclusion of native prose and raw spans. Extends ADR-1619–1624.

## Decision

Add an explicit `explanation` projection to `gregale_get_request_evidence` using
the already fetched native evidence response. Add no tool, endpoint, schema,
migration, native query or new diagnostic permission. Preserve existing exact
request selection, app-wide consent, immutable app identity, deployment
ownership/scope checks and final authorization before serialization.

Native explanation prose can include SQL patterns, private dependency labels
and references to omitted spans or regression payloads. Do not forward native
headline, diagnosis, finding/recommendation text, reference registry labels or
values, or primary span. Accept only reviewed finding codes whose facts agree
with exported structured evidence. Render fixed descriptions from safe numeric
fields and known phase names, without application prose interpolation.

Supported codes are `request_failure`, `request_client_error`, `cold_boot`,
`evidence_gap` and `longest_stage`. Verify retained represented counts, HTTP
status ranges, cold-boot flag and observed/missing/partial stage status as
appropriate. Resolve the native `request` or known `correlation:<phase>` label
to a JSON Pointer in this response. Duplicate/missing phases, a truncated stage
list, unsupported or extra references suppress the finding. Longest-stage
claims require positive observed duration and no longer observed stage; ties
can share the maximum. Never infer a causal critical path from stage durations.
Hidden-span, guest-outcome, regression and deployment-comparison rules remain
omitted until their supporting evidence can be safely exposed.

Centralize budgets in `pkg/api/limits.go`: eight finding candidates, five
recommendation candidates and one reference per supported finding. Keep native
prefix ordering and expose native returned counts, output/reference truncation
and total omitted counts. Do not fill the prefix by scanning additional rows.
Retain non-nil empty arrays. API synthesis/capture completeness is unknown;
output availability does not establish complete evidence or healthy traffic.

Preserve closed native confidence labels with `confidence_basis:
native_rule_label`; unknown values become `unknown`. These labels are not
calibrated probabilities or causal proof. Preserve approximation for correlation
stages and request rows representing multiple requests. Keep known native
status/generator metadata and map unreviewed values to `unknown`. Native
`unobserved` concerns regression availability, not request health. Do not expose
an overall diagnosis whose support was omitted. Report absent legacy synthesis
as `unavailable`, no projectable findings as `no_supported_findings`, and
projection/evidence limitations as `partial`. Do not manufacture a fallback
diagnosis from an absent explanation.

Project only reviewed read-only investigation suggestions from native actions
and supported findings: `inspect_guest_failure`, `inspect_wake_timeline` and
`inspect_stage_timeline`. Use fixed text and the finding's exposed reference;
collapse duplicate actions. Omit collection/query-plan and unsupported actions.
Returning a suggestion performs no followup reads, collection, probe, replay or
write, and adds no environment/log permissions. Extend existing request and
performance prompts to explain these boundaries without fetching evidence.

## Consequences

Agents can explain visible request outcomes and evidence gaps using bounded,
resolvable citations while retaining the native rule's confidence label.
Findings that depend on data outside the MCP evidence contract are deliberately
omitted and counted. Native free text remains excluded by construction instead
of relying on credential redaction to identify arbitrary SQL or private data.
There remain 38 read/planning tools, 46 with writes enabled and six prompts.
Protocol fixtures and pinned real Codex acceptance cover consent, omissions,
malicious native prose/references, absent synthesis and zero native mutations
or model calls. Provider/native deployment qualification remains separate.
