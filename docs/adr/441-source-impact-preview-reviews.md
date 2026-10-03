# ADR-441: Source impact joins and priorities in preview reviews

- Status: accepted
- Date: 2026-10-01
- Related: ADR-435 (preview reports), ADR-436 (requirements), ADR-440 (function impact)
- Envelope versions superseded by [ADR-442](442-request-input-compatibility.md).

## Context

Function impact detects behavior changes that do not change OpenAPI. Customers
otherwise correlate source, captured contracts, candidate checks, current
policy requirements, and production traffic manually. A useful review queue
must explain its evidence and avoid joining findings from different revisions.

## Decision

Add an optional `--source-impact` artifact to `gregale preview report`. Strictly
parse bounded version 2 FastAPI JSON before authentication or network reads.
Reject unknown fields, duplicate keys, inconsistent summaries, malformed
locations and provenance, and collection, evidence, byte, and nesting limits.
Local file reads use the existing vetted regular-file helper. Errors contain
no decoder excerpts. Centralize new bounds in `pkg/api/limits.go`.

The analyzer optionally records a canonical GitHub identity from local origin
metadata. Strip credentials and support only recognized GitHub reference
forms. Compare source identity, full commit, and exact source/build root with
both selected deployments' declarations, checking app ownership. Working-tree
candidates, missing annotations, unsupported providers, inconsistent embedded
commit references, and mismatches remain unbound. Agreement is a
`declared_match`, not archive authenticity: Python fingerprints and deployment
archive digests cover different bytes. Never compare these hashes as identity
proof. Retain a digest of the supplied artifact for audit.

Match source routes only against captured deployment contracts. Ignore names
of whole-segment identifier parameters when the method/template match is
unique on both sides. Preserve separate source and contract classifications.
Ambiguous templates, unsupported converters or embedded parameters, absent
captured routes, and conflicting presence remain unmatched. An unbound or
unmatched source finding receives no deployment test or traffic evidence.

Add explicit priorities: blocker for known contract breaks/removals, failed
candidate checks, or exact declared requirement violations; needs_evidence for
unresolved source, mapping, requirements, or missing passing candidate samples;
review for affected routes with samples that still need behavior review.
Use only exact concrete-path requirement matches; never promote one request's
policy result to an entire template. Source-matched separate-deployment checks
remain supplemental. Baseline revision-attributed request count orders items
within a category, with missing traffic last; it is not a risk score.

Source-enabled reports use envelope version 2, with structured binding,
unmatched findings, handler locations, static chains, priorities, and next
actions. Reports without the option retain version 1. Shareable output excludes
source excerpts, arbitrary source issue messages/scope, raw repository URLs,
policy actions, and receipt errors. Existing outcome and CI gates preserve
known contract/test/policy findings and include unresolved source or review
needs. The command performs existing account-scoped GETs only.

## Consequences

Customers can review shared-function changes alongside available deployment
evidence without manually merging reports. The queue remains useful with
incomplete evidence because it explains which binding, capture, or assertion
work remains. Static references and passing request samples cannot guarantee
business behavior or universal route coverage. No server attestation or new
framework adapter is introduced. Other repository providers need explicit
canonicalization and deployment annotation support before joining.

## Validation

Tests cover real Git artifact round trips, credential stripping, strict JSON
and bounds, both-side provenance mismatches, working trees, source-root and
reference conflicts, app ownership, unique parameter-name matching,
ambiguities, unsupported templates, contract/source separation, candidate and
supplemental receipts, concrete requirements, priority ordering, missing
traffic, incomplete assembly, redacted/escaped CLI output, pre-network input
rejection, version compatibility, and output-before-exit CI behavior.
