# ADR 1459: Observed release comparison and atomic bulk issue triage

Status: Accepted

## Decision

Compare two distinct, owner-scoped deployments in a single repeatable-read snapshot over the fixed last 24 hours. Expand request telemetry row `count` weights for observed requests and HTTP 5xx requests. Return the observed 5xx fraction and candidate-minus-baseline delta; rates are null when there are no request observations. Coverage is `observed` or `unknown`, never a claim of complete traffic. Request observations span deployment traffic across environments. Issue occurrence and activity counts use an explicit environment, defaulting to `application`.

Issue signals include retained events, distinct current canonical groups, verified distinct customers, unattributed events, and distinct groups with recorded creation/regression activities attributed to each deployment within the window. Creation/regression activities can outlive occurrences. Merges and splits affect current group membership. Different deployment ages, traffic exposure, sampling, ingestion delay, and retention make this a descriptive comparison, not causal regression detection or crash-free session health.

Accept at most 50 explicitly selected distinct issue UUIDs for bulk assign, resolve, ignore, or reopen. Reuse single-issue lifecycle logic under the existing app lock and one transaction. Resolve merged aliases once and return one canonical group. All changes, activity, and transactional webhook work roll back when any selection or action fails. Preserve the normal mutation permission, session MFA, CSRF for dashboard forms, and API idempotency middleware. Merge/split actions remain individual operations.

## Consequences

Operators can compare available release evidence and triage selected groups through the dashboard, CLI, and SDKs. No new storage tables or telemetry promises are introduced. Saved views, richer team routing, CODEOWNERS ingestion, external integrations, and session instrumentation remain separate work.
