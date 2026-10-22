# ADR-1262: Worker preparation of initial-failure reviews

Status: Accepted (2026-10-12)

## Context

The policy worker scans recovery jobs. Captured generation-zero failures without
parent jobs therefore require operators to prepare ADR-1261 reviews individually.

## Decision

An enabled preview policy with the explicit initial_failure trigger and explicit
workflow and failure-status filters also prepares initial reviews in the existing
scheduler policy poll. Each poll scans at most WorkflowRecoveryPolicyJobsPerPoll
matching, unreviewed retained failures. A separate durable UUID cursor advances
through full pages and wraps after a short page, so temporarily blocked failures
are revisited and cannot permanently hide later failures. Policy updates reset
both cursors. Existing reviews, including cancelled and expired reviews, are not
recreated for the same policy version.

Commit the policy claim and cursor before preparing reviews. Preparation uses
separate account-first transactions with a fresh expected-policy-version fence,
live diagnostics, shared preparation budgets, pending-job checks, and ready
notification capture. This preserves the existing account-before-policy lock
order and keeps slow preparation outside the policy claim. A crash between cursor
advance and preparation is recovered when the retained scan wraps. Missing
failure evidence and ordinary eligibility or budget blockers do not fail a poll;
infrastructure failures are returned to the worker. Review events identify the
system actor initial_recovery_policy.

The worker does not submit native jobs or resume runs. Automatic first execution
requires a subsequent explicit mode, separate execution budgets, unresolved-run
serialization and failure pause controls. This increment establishes the bounded
preparation path before introducing that authority.

## Consequences

Initial failures can notify operators without manual review creation. Preview
policies remain execution-free. Discovery is bounded and eventual rather than a
promise to process all retained failures in one poll. Blocked scan attempts do not
create durable reviews or consume preparation budget; fresh preview APIs expose
current blockers. Existing parent-job scans and automatic saved-failure retries
continue independently.
