# ADR-20261112230000: Company projects reach GitOps, environment copies, full clones and browser reviews

Status: Accepted (2026-11-12)

## Context

Company projects authorize every `/v1/projects/{slug}/...` route through the
reviewed table in `pkg/apid/projectaccess/routes.go`; an unlisted route is
refused to every member, owners included (bug hunt 37,
ADR-20261110200000). Nineteen routes stayed unlisted on purpose: environment
GitOps (15), full environment clones (2) and the browser review of a
protected promotion (2). `projectaccess.ValidateEnvironmentCreation` also
refused `from_environment` copies. So a company could not run GitOps, copy or
clone an environment, or let an agent ask a human to approve a protected
promotion, while a personal project could (projects review 2026-11-12,
problem 1).

Environment branches (ADR-20261031193635, organizations amendment) already
settled the analogous question for copying: a copy stays inside the project's
resource owner, a developer may copy an unprotected environment, and copying a
protected environment other than production needs the approve action.

## Decision

1. **GitOps.** Status, releases, adoption preview and the revision, plan,
   promotion-plan and rollback-plan previews need `read_project`. Creating,
   changing, rebinding or detaching the Git source and adopting it need
   `manage_project_security`, the action that links a company project's
   repository (`PATCH /v1/projects/{slug}`). Approving a revision needs
   `deploy_project`; overrides need `configure_project`. In a protected
   environment other than production, approving a revision and adding or
   removing an override also need `approve_project_environment`
   (`projectaccess.ProtectedEnvironmentChange`, which `BranchParent` now
   calls). Production is exempt as it is for branches and releases
   (ADR-20261019071733 amendment 9).
2. **GitLab.** A GitLab connection belongs to one member's account, and a
   company project never acts through a member's own credentials
   (ADR-20261110200000). Binding or rebinding a GitLab source for a company
   project answers 409 `gitlab_company_project_unavailable`. GitHub sources use
   the project's bound repository and installation, which for a company
   project is the organization's own.
3. **Copies and full clones.** `from_environment` copies and
   `POST .../environment-clones` need `configure_project`; a protected
   source other than production also needs `approve_project_environment`.
   Reading a clone operation needs `read_project`. The clone coordinator
   already resolves the project's resource owner for every copy it makes.
   Full clones stay behind their admission switches.
4. **Browser review of a protected promotion.** Requesting and exchanging
   need `deploy_project`. The sealed ticket of a company project carries the
   organization and the requesting member. The review page shows and accepts
   the review only for a signed-in current member of that active organization
   whose role has `approve_project_environment`. The exchange succeeds only
   for the member who asked, and only while the reviewer still has that role.
   The approval records the reviewer as `approved_by_account_id`, so release
   approval policies (self-approval, approver roles) judge the human who
   approved, not the agent. Personal reviews are unchanged.

## Consequences

- `TestEveryProjectRouteIsReviewed` no longer exempts any route: every
  project route must have an organization action.
- Async-route manifest bindings remain outside company projects.
- GitOps sources on a legacy (not billing-adopted) company project are stored
  under the creator's namespace and keep blocking the creator's erasure, as
  GitOps references already do.
