# ADR-20261017190810: Reviewed GitOps promotion pull and merge requests

- **Status:** accepted (implementation preview)
- **Date:** 2026-10-17
- **Related:** ADR-20261017190803, ADR-20261017190804, ADR-20261017190808, ADR-20261017190809

## Context

Promotion plans already prove a currently served origin and propose only target
workload source changes. Exporting and manually committing those changes leaves
operators to repeat repository selection, stale-base checks and retry handling.

## Decision

1. Add `gregale projects environments gitops promotion-pr`. Require the origin
   environment, immutable target commit and explicitly reviewed plan hash.
   Fetch a fresh promotion plan and active target source. Derive the repository,
   provider, manifest path and target branch from that binding; no independent
   repository or branch override is accepted. Tag bindings are unsupported.
2. Keep platform API access read-scoped. Use the operator's authenticated `gh`
   or `glab` JSON API command for GitHub.com or GitLab.com. Explicit hostnames
   and encoded endpoints avoid ambient repository/host selection. Send manifest
   bytes through stdin. Suppress provider debug/stderr and omit response bodies
   from errors. No provider token is requested, persisted or printed by Gregale.
3. Default to a read-only preview. `--yes` authorizes creation of a draft PR/MR
   and its proposal branch. Do not merge, approve, enable auto-merge, alter
   protection, push to the target branch, or touch the local checkout/index.
4. Require the provider numeric repository identity to match the verified
   binding. Require the target branch head to equal the reviewed target commit
   before preparation and before every visible write. Recheck both platform
   promotion observation and binding before committing and before opening review.
   Provider calls cannot transact with platform observations or the target ref;
   recheck after review creation and report a moved base as failure. An existing
   draft/branch can remain after a race or uncertain response; never delete it or
   retry an uncertain write automatically. A fresh plan is required for a new base.
5. Read the manifest at the immutable target commit. Verify the Git blob identity
   and ordinary non-executable file mode. Validate the complete base definition
   digest using ADR-20261017190803's exporter. Preserve target YAML nodes/comments outside
   changed source contracts; JSON remains canonical JSON. Parse and verify the
   resulting complete proposed digest. Configuration and secret-reference values
   stay out of review descriptions and CLI receipts.
6. Name the proposal branch from the complete domain-separated plan hash.
   GitHub creates one tree/commit with that exact parent and exclusively creates
   the ref. GitLab creates one commit with `start_sha`, `force=false` and the
   original file's `last_commit_id`; GitLab rejects an existing target branch for
   this form. Never update an existing proposal branch. Verify one parent, one
   modified file, unchanged file mode and exact manifest bytes before review.
7. On retry inspect all review states for that exact proposal branch. Reject
   closed/merged, ambiguous, retargeted, foreign-fork or edited requests and
   changed branch contents. A matching open request returns its existing URL.
   A valid orphan branch can be reused to finish review creation after a lost
   response. Existing request descriptions must match the redacted provenance
   generated from the reviewed plan (ignoring only final line endings).
8. Review descriptions identify origin release, source manifest commit, selected
   image/build provenance, target base, workload changes and plan hash. They
   require normal Git review/approval and fresh target qualification, activation
   and serving evidence. Requests do not pin artifacts or promise indefinite
   retention. Current publisher trust and ordinary GC fences remain mandatory.
9. Scope this slice to promotion. Rollback can later reuse the provider mechanism
   with its own reviewed rollback contract. Do not change native acceptance,
   production qualification/enforcement gates, release, tag or deploy behavior.

## Validation

Cover GitHub/GitLab commit payloads, exact tree modes/content and ancestry,
configuration/comment preservation, no-write previews, stale observations/base
heads, mutated branches, closed/foreign/edited reviews, lost-response recovery,
bounded/redacted provider I/O, JSON/text dispatcher paths and CLI references.
Provider tests use isolated scripted responses, never customer repositories.
Native VM/leak acceptance remains pending on an exclusive physical x86_64 host.

## Provider contracts

- [GitHub Git trees](https://docs.github.com/en/rest/git/trees) and
  [pull requests](https://docs.github.com/en/rest/pulls/pulls).
- [GitLab commits](https://docs.gitlab.com/api/commits/) and
  [existing-branch validation with start_sha and force=false](https://gitlab.com/gitlab-org/gitlab/-/blob/master/app/services/commits/create_service.rb).
- [gh api](https://cli.github.com/manual/gh_api) and
  [glab api](https://docs.gitlab.com/cli/api/).
