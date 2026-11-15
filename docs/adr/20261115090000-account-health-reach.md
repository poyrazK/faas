# ADR-20261115090000: Account health reaches the overview, refusals and CI

Status: Accepted

Extends ADR-20261112210000 and ADR-20261114190002.

## Context

Account health (`/dashboard/account/health`, `GET /v1/account/health`,
`gregale account health get`) explains what may stop an account from working,
including the attention signals customers can see. Three paths still missed
it:

- The overview banner ("N things need your attention") counted restrictions
  only, so an account whose deploys keep failing showed no banner.
- A CLI user refused with a revoked key, a past-due account or a plan limit
  saw the error but nothing pointing at the page that explains it.
- CI pipelines and agents had no way to fail fast on an account that needs
  attention.

## Decision

1. **Overview banner.** `accountAttention` adds the account's open
   customer-visible signals to the restriction count, with one count query on
   the existing `customer_attention_signals(account_id, state)` index
   (`attention.Evaluator.CountOpenForCustomer`, same kinds and filters as
   `ForCustomer`). The page still never reads the deployment, domain or error
   tables. A failed count is logged and left out, so the banner degrades to
   restrictions only.
2. **CLI refusal hint.** Text-mode API errors whose code the health checks
   explain gain an `Account health:` line: past-due, unverified email and
   plan-limit codes point at `gregale account health get`. A revoked or expired
   key cannot read the health API, so those point at the dashboard page, which
   a browser session can open. `--json` output is unchanged.
3. **`--check`.** `gregale account health get --check` prints the report as
   usual, then exits 1 with an "Account health check failed" error when
   `attention` is non-zero. It follows the `--check` flags of `gregale job
   status` and friends.

## Consequences

- Customers see recurring problems from the overview, from the error that
  caused them, and in CI.
- No migration, no API change.
