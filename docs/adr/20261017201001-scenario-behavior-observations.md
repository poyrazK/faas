# ADR-20261017201001 · Declared scenario behavior observations

- **Status:** accepted
- **Date:** 2026-10-16
- **Decision:** Extend Gregale Test with an opt-in, versioned application-observation contract and offline comparison. Reuse existing runners, run receipts, cleanup and report surfaces; add no daemon, provider lifecycle or production cloning path.
- **Why:** Existing report comparison identifies assertion and performance changes but cannot identify an extra successful external action when the declared API checks still pass.
- **Consequences:** Each run records required, logically keyed selected values alongside fixture/observer/operation-recipe fingerprints and explicit application-reported provenance. Comparison gates changed values and treats missing evidence, changed inputs or failed execution/cleanup as inconclusive. Legacy reports retain existing behavior.
- **Rejected alternatives:** Capturing all response/database/external payloads broadens retention without proving coverage. Automatically ignoring IDs, reordering arrays, or deduplicating deliveries can conceal behavior changes. Waiting for full environment cloning would couple a useful CLI capability to gated provider and native acceptance work.

## Collection boundary

The primary scenario declares observation keys/kinds, fixture files and observer
files. Scenario commands write one bounded JSON document to a fresh private
output path. All declared keys are required, with explicit JSON values; additional
keys, unknown fields, duplicate JSON object keys and unsupported versions fail.
The runner assigns provenance; applications cannot claim platform attestation.

Fingerprints cover declared file identities and contents, the selected schema,
and the executed operation recipe. Local case values also participate. Files are
checked again at collection. Application source and runtime configuration may
vary because they are the subject of the comparison. Transitive imports and
other inputs are covered only when declared; the fingerprints do not prove that
an application applied the fixture or observed every external effect.

Collection runs before runner-owned fixture cleanup and also preserves valid
observations from a failed assertion command. It never changes that failure to
success. Private output directories are removed on success and error. Existing
timeouts and interruption handling remain in force. The first version rejects
resilience expansion rather than silently dropping behavior nested in receipts.

## Comparison and compatibility

Matching retains scenario, engine, profile, case and attempt identity. JSON object
ordering and equivalent number spellings are normalized without floating-point
conversion; arrays retain order and multiplicity. Normalization of volatile
application fields belongs in the declared observer and changes its fingerprint.
Every selected observation is application-reported, including observations
labeled HTTP, delivery, object, database or application.

Behavior comparison is independent of performance budgets: changed selected
values fail, while incomplete or incomparable evidence is inconclusive, and both
return nonzero. Failed execution or cleanup cannot establish equivalence even
when selected values match. JSON and existing terminal, HTML, Markdown and GitHub
summary surfaces expose the result. Reports without behavior evidence retain
their previous informational semantics. The optional scenario field is omitted
from legacy replay hashing when absent.

## Acceptance and next steps

Portable acceptance uses the existing export API, worker and delivery sink with
fresh in-memory storage. Injecting an extra successful notification leaves API
and report observations unchanged and changes delivery count from one to two.
Both executions pass their ordinary checks, while comparison fails and writes
reviewable artifacts. Tests cover missing evidence, input drift, invalid output,
JSON precision/duplicates, cleanup, compatibility and rendering.

This establishes selected-outcome comparison. Two-revision orchestration,
platform-observed adapters, intentional-change approval, nested resilience
comparisons, complete data clones, migration contention and hosted/native
qualification remain separate work.
