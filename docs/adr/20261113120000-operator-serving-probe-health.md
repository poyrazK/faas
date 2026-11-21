# ADR-20261113120000 · Operator serving-probe coverage

- **Status:** accepted for implementation; production rollout pending.
- **Date:** 2026-11-13
- **Extends:** [independent serving-probe intent](20261112210000-serving-probe-coverage.md).

Add GET `/v1/admin/obs/serving-health` and `gregalectl obs serving-health` as
bounded operator reads. Apply existing operator read scope, MFA and allowlist
policy. Accept no query parameters and perform no writes, direct probes or
notification sends. Existing observability endpoints retain their contracts.

Validate raw deployment inventory independently from current results. Expose
every expected node/URL pair, including missing and unknown evaluations.
Unconfigured or unavailable intent cannot establish healthy empty coverage;
an explicitly configured empty inventory is not applicable. Preserve the
existing 1,000-pair bound and fail closed on inconsistent or excessive input.

Use the deployed coverage recording rules for serving classifications. Their
semantics are trusted; the new read does not implement a second probe engine.
Pin four strict queries to one time under a three-second budget. Require fresh
recordings, policy equality with current independent intent and matching
evaluation timestamps. Recheck the last-run clock against the current age
budget so a previously passing evaluation cannot hold green between runs.
Known faults take precedence over unknown peers.

Expose stable node names and SHA-256 fingerprints of exact configured URL
bytes. Exclude raw URLs, query credentials, response bodies, scrape addresses,
source labels and upstream errors. Nulls distinguish missing evidence from
false or zero. No cached health result can fill an observation gap.

Tests cover source and policy coherence, all coverage states, missing/stale
evidence, bounds, redaction and cancellation. Real Prometheus fixtures pin
query behavior against the deployed rules; API, CLI, OpenAPI and generated SDK
checks preserve authorization and null semantics. See the
[operator procedure](../ops/serving-health.md).
