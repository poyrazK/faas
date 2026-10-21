# ADR-1000: GitLab.com as a reviewed environment Git source

- **Status:** implementation in progress
- **Date:** 2026-10-13
- **Related:** [ADR-568](568-environment-gitops-contract.md)

## Context

ADR-568 first used the project's GitHub App installation for repository
identity, source archives, and protected-branch merge evidence. This made a
GitLab repository URL insufficient: a URL says neither which provider-owned
project was reviewed nor whether the authenticated account can still read it.

## Decision

1. A source has a durable provider discriminator, numeric provider project ID,
   canonical namespace path, ref, and manifest path. Existing rows are GitHub.
   GitLab.com paths may contain nested groups. GitLab source creation requires
   an account-scoped OAuth connection and verifies both the numeric ID and
   canonical path through GitLab's API. Rebind and every source read repeat
   this check, so a rename, transfer, access loss, or replacement fails closed.
2. The GitLab.com OAuth connection uses `read_api` and `read_repository`, PKCE,
   a short-lived account-bound CSRF state, a fixed HTTPS callback, and sealed
   access/refresh tokens. Refresh replacements are stored with a version fence;
   a failed durable rotation requires reconnect rather than using an
   unpersisted credential. Disconnect removes the account credential; source
   reads then fail closed while prior approved revisions remain durable.
3. Polling resolves a branch or tag to a full commit SHA, then requests the
   complete archive by that SHA and validates the bounded gzip/tar definition.
   When a previously verified binding resolves to the same SHA, polling may
   reuse its durable definition digest after rechecking account access and
   canonical project identity. This avoids [GitLab.com's archive rate limit](https://docs.gitlab.com/api/repositories/#retrieve-file-archive-from-a-repository)
   without treating a mutable ref as immutable.
   Manual review and approval read the same commit-pinned source. GitLab
   sources are initially **report mode, manual approval, prune disabled**.
   Existing GitHub protected-branch evidence is not reused for GitLab. The
   production workload adapter and GitLab protected-branch review evidence
   require separate qualification before enforcement can be enabled.

The protected-branch evidence and workload preparation extension is specified
in [ADR-20261017190801](20261017190801-gitlab-reviewed-gitops.md). Its provider-bound trust contract
supersedes the initial report/manual/prune restriction; production gates remain.

## Consequences

- A standalone Gregale project can bind a GitLab.com definition after an
  account connection; it need not have a GitHub installation.
- Provider identity and source outages remain distinct from the last approved
  revision. No GitLab token is returned in API responses or stored in Git.
- GitLab.com OAuth application credentials must be configured on `apid` as
  `FAAS_GITLAB_OAUTH_CLIENT_ID`, `FAAS_GITLAB_OAUTH_CLIENT_SECRET`, and
  `FAAS_GITLAB_OAUTH_REDIRECT_URI`; the registered redirect path is
  `/v1/gitlab/callback`. A missing configuration hides the dashboard connect
  control and rejects connection attempts.
- This decision does not qualify GitLab enforcement, protected-branch
  auto-approval, Job execution, pruning, or native KVM acceptance.

## Acceptance

Test OAuth state, token exchange and refresh, account isolation, project ID
and path verification, commit pinning, archive bounds, disconnect and access
loss, PostgreSQL migration/rotation races, SDK and CLI round trips, and
report-mode drift. Dedicated native acceptance remains the gate for production
workload execution.
