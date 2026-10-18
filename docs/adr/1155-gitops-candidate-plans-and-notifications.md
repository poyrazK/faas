# ADR-1155: GitOps candidate plans and customer notifications

- **Status:** accepted
- **Date:** 2026-10-13
- **Related:** ADR-568, ADR-1000, ADR-224, ADR-982

## Context

Revision review returns Git bytes; adoption planning requires an approved
revision. PRs and MRs need ownership and field-change assessments before
approval. Reports also need customer notification, and GitLab sources need a
complete dashboard binding workflow.

## Decision

1. Add read-only `POST .../gitops/plan` for an immutable commit. Reuse provider
   identity, access and archive verification. Reread the binding and scoped
   intent under the source row lock; reject a changed generation or contract.
   Return commit, digest, generation, observation version/time and plan hash.
   Use the existing planner with no adoption and only the current pruning
   policy. Permit initial generation zero only for candidate previews. A
   separate candidate revision namespace prevents their hashes authorizing
   adoption or execution. Create no revision, claim, workload, run or alert.
   Omit all before/after values from public candidate results.
2. Expose JSON and escaped Markdown through CLI and SDKs. `--fail-on-blocked`
   writes the result before exit 2. Invalid/unredacted responses fail closed.
   CI jobs provide PR/MR feedback with caller credentials; Gregale needs no
   provider write permission or broader OAuth scope. A plan is an observation,
   not native qualification or deployment authority, and must be refreshed.
3. Add explicit account event `environment.gitops.changed` with independent
   drift and source-verification axes. Establish first healthy baselines
   silently; notify first failures and recoveries. Deduplicate unchanged
   status. A new approved generation with unresolved drift can notify again.
   Reports stale against the current intent version cannot change the alert
   baseline. Retain baselines independently of report history.
4. Commit each baseline and its opted-in receiver fan-out with the report or
   source poll. Reuse the durable delivery ledger, signatures, retries,
   receiver controls and account quota. Each transition has a UUID; receiver
   delivery IDs remain stable through retries. Account events without an app
   use account CloudEvents identity. Include approved revision/commit, run and
   dashboard links, field names/actions and stable error/blocker codes; omit
   values and credentials. Bound fields and blockers to 100 each and baseline
   JSON to 2 KiB. Report detail truncation explicitly. Require explicit filters.
5. Offer GitHub and GitLab.com binding forms, provider-specific source controls,
   and candidate plans separate from approval. GitLab keeps manual approval,
   report mode and disabled pruning.

## Validation and scope

Exercise unapproved previews, account isolation, binding races, redaction,
candidate receipt rejection, observation hashes, stale reports, deduplication,
recovery and source failures in memory and PostgreSQL. Check CLI output/exits,
dashboard forms, SDK round trips, migration replay, clone coverage and signed
account delivery without an app.

Production enforcement, workload provenance and native acceptance retain their
existing gates. This introduces no VM lifecycle or qualification changes.
