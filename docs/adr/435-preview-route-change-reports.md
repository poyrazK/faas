# ADR-435: Read-only preview route change reports

- **Status:** accepted for initial implementation
- **Date:** 2026-10-01
- **Decision:** Add `gregale preview report` as a composition of existing
  authenticated API reads. Compare deployment-captured OpenAPI documents for
  named parent/candidate revisions; never replace a missing capture with a
  current import or discovered route inventory. Publish text, Markdown, and a
  versioned JSON read model with availability and provenance for each evidence
  source. No new control-plane writes, storage, quotas, or entitlement bypasses.
- **Policy:** Current app policy summaries are explicitly separate from captured
  revision contracts. They do not establish effective authorization or historical
  policy equivalence. Existing concrete edge-rule tracing remains authoritative
  for static request-policy evaluation.
- **Traffic:** A route's aggregate latency is attributable to a revision only
  when its complete request count belongs to that revision. Different apps and
  traffic populations yield advisory differences, never a benchmark verdict.
- **Tests:** Preserve the server-returned source archive hash in real-VM scenario
  receipts. Exact app/deployment matches can provide route check evidence;
  matching source in another test environment is separately labeled supplemental.
  Failed run/cleanup, local/simulated results, and unmatched receipts cannot become
  passing candidate coverage. Customer-owned receipts are not signed attestations.
- **Disclosure:** Export identifiers, supported change kinds, policy kind names,
  timing aggregates, and test counts. Exclude rule actions, schema example/default
  values, query strings, request bodies, and raw test errors from shareable output.
- **CI:** Generation and release assessment have separate exit semantics.
  `--fail-on-breaking` gates known structural response/route breaks;
  `--fail-on-incomplete` also gates missing/review-required evidence. Neither
  option mutates rollout or traffic state.
- **Acceptance:** CLI HTTP tests cover read-only composition, selected revision
  provenance, optional backend failures, explicit baseline ownership, output
  redaction, gate exits, and conservative traffic/test attribution. No VM lifecycle
  behavior changes; native KVM acceptance is not claimed by this CLI report.

See [the customer guide](../route-change-report.md) for scope and follow-on work.
