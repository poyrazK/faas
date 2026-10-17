# ADR-1551: Reviewed GitOps recovery pull and merge requests

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-17
- **Related:** ADR-1545, ADR-1546, ADR-1547, ADR-1548, ADR-1550

## Context

The recovery catalog and rollback planner already identify retained, previously
served releases and export source-only recovery proposals. Operators still need
to manually turn that export into a Git change during recovery. ADR-1550's
provider transports and branch/review guards can perform that step without
extending platform deployment authority.

## Decision

1. Add `gregale projects environments gitops rollback-pr`. Require an explicit
   canonical `--to-release` UUID, immutable current target commit and reviewed
   rollback plan hash. Do not infer the last release or select one automatically.
   Fetch the existing read-scoped rollback-plan endpoint and active Git source.
2. Validate the rollback response under its own hash domain and exporter.
   Require the selected release to match the response, historical graph and
   serving timestamp, source identity, provider/repository, environment and
   current binding generation/intent. Reject blockers, missing proof, detached
   or suspended sources, source errors and no-op recovery. Do not convert the
   rollback response into a promotion plan or reuse promotion approval hashes.
3. Extract ADR-1550's provider write/retry state machine behind a private prepared
   request. Only the separately validated promotion and rollback entry points
   can construct one. Reuse the same exact-file/tree/parent checks, provider
   numeric repository identity, exclusive branch creation, fixed hosts, bounded
   stdin transport, redacted errors and local-checkout independence.
4. Derive repository, manifest and target branch from the active binding. Require
   the target head to equal the reviewed commit before preparation, writes and
   returned receipts. Refresh the rollback plan and source before committing and
   before opening review. The observation binds both selected historical release
   and current release, so new serving history, revocation, retention/GC changes,
   target intent or binding changes require fresh review. Provider writes cannot
   transact with platform reads; races can leave a branch or draft, never serving
   authority. Check the target head again after creating review.
5. Default to a read-only preview; `--yes` creates a draft GitHub PR or GitLab MR.
   Use `gregale-rollback-<complete rollback plan hash>` as the exclusive branch.
   On explicit retry, verify exact manifest bytes, one parent/file and matching
   recovery description before returning an existing open request or completing
   a valid orphan branch. Reject changed, closed/merged, ambiguous, retargeted or
   foreign requests. Never overwrite a branch, reopen a review or automatically
   repeat an uncertain write. Promotion and recovery branch/description markers
   stay distinct.
6. Invoke the rollback exporter on the complete immutable target manifest. Share
   only YAML source-node replacement and final digest checking with promotion.
   Preserve current configuration, secret references, routes, policies, app/queue/
   service identities, schedules, command and smoke contracts. Retain YAML
   comments outside replaced source nodes. Check actual encoded manifest size
   against the existing definition bound.
7. The draft and receipt identify the selected release. The description includes
   historical graph, serving time and manifest commit, current release, target
   base, selected image/signed-build provenance and rollback hash. Historical
   serving proof selects code; it is never current qualification or readiness.
   Normal Git review/approval, current artifact trust and fresh target
   qualification, activation and serving remain mandatory. Creating the draft
   grants no artifact pin or retention extension.
8. Recovery changes code only: it does not restore data or undo database
   migrations. Completed Jobs are never replayed. Scheduled source/Dockerfile
   Jobs retain current execution configuration and require fresh qualification
   plus a successful post-activation occurrence for future runs. Existing
   unsupported workload and recipe restrictions remain in the planner.
9. Do not merge, deploy, release, tag, change VM lifecycle or enable production
   qualification/enforcement. Dedicated native acceptance remains a separate
   requirement on an exclusive physical x86_64 Linux KVM host.

## Validation

Run promotion and rollback regressions after sharing the state machine. Cover
both providers, preview/explicit creation, release selection, hash separation,
historical proof, current release/binding changes, stale target/proposal heads,
closed requests, uncertain-write recovery and target configuration/comment
preservation. Include retained signed builds for scheduled Jobs and dispatcher
text, explicit/global JSON, stale-plan HTTP failures, help and reference checks.
Provider operations use isolated fixtures; these checks claim no live provider
write or native VM/leak acceptance.
