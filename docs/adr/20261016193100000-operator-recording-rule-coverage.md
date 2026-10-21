# Operator recording-rule definitions and expected alert references

## Status

Accepted. Read-only backend/API/CLI extension; frontend and rollout are separate.

## Context

An alert definition can be loaded, match expectations and evaluate successfully
while an absent recording rule leaves its expression empty. Alert-rule coverage
deliberately excludes recording definitions. Expected recording dependencies can
also form chains; a direct-only reference list hides upstream definitions.

## Decision

Add GET `/v1/admin/obs/recording-coverage` and
`gregalectl obs recording-coverage --expected-set ... --json`, with existing
operator read scope, MFA, allowlist, rate limits and strict query handling.
Use the existing explicitly selected minimum file profiles: `gregale-core-v1`
selects `faas.rules.yml`; `gregale-full-v1` also selects `bridge.rules.yml`,
`pg_backup.rules.yml`, `data_placement.yaml` and `data_repair.rules.yml`. Without selection, advertise
available metadata, return unknown and make no Prometheus request. Catalog
presence is not installation intent. Additional valid source identities are
allowed. Existing alert-only coverage and its revision remain unchanged.

Generate a separate digest-only recording catalog using the same pinned official
Prometheus parser and normalization algorithm. Compare expressions and rule
labels, normalizing recording hold/keep components to zero. Recording definitions
with source timers are invalid. Match identity by source basename, group and
recording metric name; directory relocation is ignored. Duplicate candidates
or basename/group collisions are ambiguous. Source omissions behind invalid
identities stay unknown. Group labels, intervals, offsets and annotations are
outside v1 comparison; nonempty group labels make definitions unknown.

Use one strict bounded Prometheus `/api/v1/rules?type=record` read with the same
eight-MiB and 4096 group/rule/alert caps as alert reads. Reading and comparison
share three seconds. Preserve all existing alert-source semantics. Prometheus
2.54.1 recording labels use `omitempty`: omitted labels represent an empty set;
explicit JSON null remains unknown. Track this distinction without changing
the collector's label values or acceptance policy. Unsupported/warning/info/
missing/trailing envelopes and timeouts yield unknown with null totals, never
inferred missing or matching definitions. A complete empty source has known
missing definitions. The five expected categories partition full totals before
a 200-item ambiguity/change/absence/unknown/match preview sorted by identity hash.

Generate expected syntactic alert references from the selected source ASTs.
Exact metric names, including exact `__name__` equality selectors, can identify
recording producers. Follow direct references and transitive recording chains,
deduplicating each alert per recording. Do not choose between multiple expected
producers of the same metric name. Nonexact selectors, conflicting exact names,
ambiguous producers and reachable cycles leave mapping uncertainty, propagated
to consuming alerts. Retain known syntactic edges alongside uncertainty. Exact
names without a producer in the selected recording catalog are raw/external
inputs outside this mapping. This does not establish their presence or health.

Bind recording definitions, references, uncertainty and the exact expected alert
catalog revision into a separate SHA-256 recording revision. Validate all
references against that alert catalog on load and return detached data. Bound
combined generator nodes to 4096 and stored recording-to-alert relations to
16384 per set; reject an unrepresentable catalog rather than sample it. Both
catalogs regenerate/check through existing Make targets, with ordinary Go drift
tests. No raw queries or label values are embedded in either generated catalog.

Each comparison item includes the full known-reference count and at most five safe
expected alert references, sorted by identity hash, with truncation. A separately
scoped dependency object reports the expected alert revision/count and complete
or partial mapping state, with full uncertain-alert count and a five-alert preview.
Mapping is available once a catalog is selected, even if the independent live
source is unavailable. Definition coverage can be covered while mapping is
partial; these are independent evidence. Dependent-alert totals count known
references before preview truncation; unresolved links may add other dependencies. References describe selected expected
catalog syntax, not deployed/current alert definitions, label compatibility,
series presence, evaluation order, runtime impact, causation or alert activation.
They exclude arbitrary/extra deployed alerts and historical episode keys.

Exclude raw expressions, label values, errors, source URLs and absolute paths
from API/CLI evidence. Validate shared comparison partitions, identities,
ordering and fingerprints, plus recording-only changed fields, selected profile,
reference metadata, previews and closed scope/state/limitation codes before text
or JSON output. Never print failed upstream bodies. Synchronize private-route
maps, DTO coverage, canonical/embedded OpenAPI and generated Node types.
No state migration, historian write, evaluation change or new deployment config.

## Validation

Review missing input versus quiet output, optional companions, ambiguous producer
names, cycles and nonexact selectors as adversarial cases. Test direct/exact-name
and transitive mapping, known edges plus unknown upstream links, revision binding,
corrupt references, isolated loads, generator drift and graph bounds. Check
actual recording wire label omission/empty/null/reset behavior, expression/label
changes, missing/ambiguous/invalid sources, cancellation, redaction and full totals
before previews. Verify operator/MFA authorization, strict explicit selection,
no implicit source read and unchanged collection/episode history. Exercise CLI
text/JSON, malformed replies, existing alert diagnostics, generated contracts,
repository policies and lint. Verify wire behavior against local Prometheus 2.54.1.
