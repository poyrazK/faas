# ADR-1298 · Read-only live automation suite comparisons

- **Status:** accepted
- **Date:** 2026-10-15
- **Decision:** Compare two retained suites for the same app and automation in a
  single read-only snapshot. Expose the comparison through the API, dashboard and
  Go, Node and Python clients.
- **Why:** A suite can recover execution across page visits, but deployment
  review still requires comparing individual runs manually. The baseline normally
  targets an older deployment, so current-target freshness cannot be its verdict.
- **Consequences:**
  - GET `live-suites/{suite_id}/compare/{baseline_id}` treats `suite_id` as the
    candidate. Either side may be historical, and self-comparison is allowed.
    Explicit baseline and candidate identities avoid guessing from creation time.
  - Cases match by exact saved name. Their frozen verification `inputs_hash`
    binds the published definition and entire saved case, including input, mocks,
    simulated expectations and live assertions. Equal nonempty hashes are required
    for regression, recovery or unchanged. This is deliberately conservative:
    even an edit to simulation-only expectations makes evidence incompatible.
  - Pass-to-fail is a regression, fail-to-pass a recovery, and equal passing or
    failing results are unchanged. Both reports must still describe the original
    run's terminal state, resume count and updated time. Target drift alone does
    not invalidate historical evidence. Nonterminal, unchecked, expired or stale
    reports cannot establish pass/fail; missing hashes or evidence produce unknown.
    Unequal retained hashes produce incompatible even while results are pending.
  - Added and removed describe selection changes, with the existing side's result
    still visible. They do not assert a case was added to or deleted from the
    current automation. Candidate order is preserved, followed by removed cases
    in baseline order. Two suites of at most 32 cases produce at most 64 rows.
  - Counts are disjoint and sum to the selected union. There is no aggregate
    release approval: zero regressions can coexist with failures or missing data.
    An optional CI policy is a separate feature.
  - Memory reads hold one lock; PostgreSQL uses one read-only repeatable-read
    transaction for both suites and all linked runs. Reads never admit, check,
    resume, cancel or mutate workflows, reports or suite records. No schema change
    or copied verdict storage is needed.
  - Authorization uses existing app/account isolation, MFA and read scopes. Errors
    are sanitized and responses are not cached. Both sides must exist in the same
    automation; missing and foreign identities return the same not-found response.
    Only identities, target metadata, result categories and counts are exposed.
  - The dashboard uses a separate GET form containing only automation and suite
    selectors, outside the editor's real-action form. Comparison cannot enable
    automatic suite advancement. Historical targets, unknown results, original
    run links and direction swapping remain visible.
- **Rejected alternatives:** Comparing top-level `outdated` outcomes confuses
  deployments with failed assertions. Matching only case names can misclassify
  changed checks. Refresh-time assertion checks mutate evidence and hide gaps.
  Independently reading each suite allows mixed snapshots across concurrent
  workflow updates.
