# ADR-20261017190801: Provider-bound GitLab review and workload provenance

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-15
- **Related:** ADR-568, ADR-1000, ADR-1331

## Context

GitLab.com sources can read reviewed definitions, but report-only validation
prevents protected-branch approval and workload preparation. GitHub installation
IDs, review receipts and source URLs must never authorize GitLab projects.

## Decision

1. Keep GitLab OAuth scopes and sealing unchanged. Reverify current account
   access, numeric project ID and canonical path for each source/build read.
   Fetch build archives by the approved immutable commit only. Run the same
   bounded archive, definition-digest, build-layout and secret checks as GitHub.
2. Use distinct `gitlab_reviewed_branch/v1` and `gitlab_reviewed_merge/v1`
   evidence profiles, with installation ID zero. Approval validation and the
   PostgreSQL provenance trigger bind the provider to the source row. The
   provider cannot be changed in place; use the existing reviewed rebind flow.
3. The first automatic-approval profile requires a single project-wide regular
   or any-approver rule with a positive review count, no hidden groups, and no
   per-MR overrides. Reset all approvals on push; disallow author and committer
   approval. Every matching protected-branch rule, including inherited and
   wildcard rules, must prohibit direct push and force push. Missing fields,
   partial pagination, unavailable permission evidence and unsupported policy
   shapes fail closed. Merge permissions use standard Developer/Maintainer/Owner
   roles; custom user/group merge grants are outside this initial profile.
   GitLab Premium/Ultimate approval-policy API access is
   required for this profile; manual review remains available otherwise.
4. Resolve exactly one merged, non-imported same-project MR for the observed
   target-branch commit. Match the merge/squash commit or a fast-forward head.
   Approvals must be from distinct active human repository writers other than
   the author, belong to the satisfied project rule, follow the latest collected
   diff version and precede the merge. Match its exact head SHA. Recheck policy,
   merge identity, target ref and repository identity after collecting evidence.
   Compare the definition at the reviewed MR head to the merged definition.
   Current policy and permissions are observations, not historical guarantees.
5. Publish GitLab source builds through the existing tarball builder lane,
   rather than attributing them to a GitHub webhook. Persist credential-free
   `gitlab://<numeric-project-id>/<canonical-path>@<full-commit>` provenance;
   freeze that identity with the approved definition, archive digest and build.
   Uploader-supplied source URLs never establish provider trust. Unverified
   tarball baselines remain blocked, including URLs claiming the correct GitLab
   project and commit. Existing image baselines can be adopted and then rebuilt
   from reviewed GitLab source. Scoped GitOps intent retains its owned identity.
   Candidate SQL guards
   bind the same identity and build kind; retries reuse the published artifact.
6. Report remains the public default. The existing isolated acceptance executor
   can exercise adoption and source preparation for both providers. This does
   not enable production qualification polling, enforcement, native execution,
   a deployment, or any destructive pruning policy. Exclusive native KVM and
   leak acceptance remain required.

## Validation

Provider HTTP fixtures cover review ordering, policy changes, wildcard bypass,
identity transfer, permission loss, bots, missing API fields and pagination.
Memory/PostgreSQL tests cover provider-isolated atomic approval, immutable
provenance, stale adoption and candidate identity. Real apid HTTP tests cover
OAuth-scoped source binding, review/adoption and rejected archive substitution.
No provider write API or real customer repository is needed for these tests.

The existing native image API/worker resilience journey also has a GitLab
variant using the fixed HTTPS client through a private test CA and a loopback
proxy that only accepts the GitLab authority. Named scenarios and the private
versioned [qualification report](../environment-gitops.md#native-qualification-evidence)
require both providers, immutable run inputs and pre/post leak observations.
This local report cannot enable enforcement. The journey uses manual approval
and image workloads; it does not claim live GitLab access, protected-MR native
qualification or native source-build acceptance. Execution remains pending an
exclusive native host.

API contracts: [approvals](https://docs.gitlab.com/api/merge_request_approvals/),
[branch rules](https://docs.gitlab.com/user/project/repository/branches/protection_rules/),
[merge request diff versions](https://docs.gitlab.com/api/merge_requests/#retrieve-merge-request-diff-versions).
