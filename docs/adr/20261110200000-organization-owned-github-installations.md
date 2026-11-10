# ADR-20261110200000: Company projects link GitHub through an organization-owned installation

- **Status:** proposed
- **Date:** 2026-11-10
- **Related:** ADR-012 (githubd), ADR-1345 (account-owned browser installations),
  ADR-20261101160300 (GitHub connector app bindings),
  ADR-202610171905 (organization project ownership)

## Context

A company project (an adopted organization's project, `projects.account_id`
= the organization's resource account) cannot link a repository. The shared
project update refuses `repo_full_name` with 409 "Repository ownership
migration required" (`handlers_projects.go`), because every GitHub
installation row is owned by the member who connected it
(`github_installations.account_id`). Resolving a company project's repository
through a member's personal installation would make the company's deploys
depend on that member: their departure, erasure or disconnect would silently
stop the company's pushes, and the member's personal installation would reach
the company's resource namespace. Apps/projects review 2026-11-10, P4.

The owner chose an organization-owned installation, ADR first.

## Decision

1. **Ownership.** An installation can be owned by an organization's resource
   account: a `github_installations` row whose `account_id` is that account
   and whose `org_id` is the organization. No new table: the row shape,
   sealing and reconciliation are unchanged. Only adopted organizations (with
   a resource account) can own one; others keep the 409 with a docs link.

2. **Connecting.** Connect GitHub accepts an optional `organization` (slug).
   apid checks, at start and again at the OAuth callback, that the signed-in
   member is an owner or admin of an active, adopted organization. The sealed
   OAuth state gains the target organization ID (a new `omitempty` field), so
   a callback cannot switch targets; the callback passes the organization's
   resource account to githubd in place of the member's account. Everything
   else in the handshake (state cookie, CSRF action, GitHub user-to-server
   verification, installation chooser) is unchanged. The installation's
   audit login remains the authorizing GitHub user.

3. **Binding.** A company project resolves `repo_full_name` against the
   organization's installations only (`resolveRepositoryInstallation` with the
   project's resource account), and `UpdateProjectBinding` writes with that
   account. Linking, relinking or unlinking a repository needs the
   project-security action that guards `PATCH /v1/projects/{slug}`
   (`authz.OrgActionManageProjectSecurity`: owners and admins, as today). A member's
   personal installation is never consulted for a company project, and a
   company installation is never offered to personal projects.

4. **Lifecycle.** Disconnecting an organization's installation needs owner or
   admin. A member's departure or erasure does not touch it. Deleting the
   organization's resource account cascades the row as today. Webhooks route
   by installation ID and the bound project, so githubd needs no change beyond
   accepting the resource account on the exchange (it already takes an
   account ID).

5. **Surfaces.** `POST /v1/orgs/{organization}/github/connect` (browser form,
   session-authenticated) and `GET /v1/orgs/{organization}/github/installations`
   list the organization's installations; the CLI reaches the latter with the
   `--org` prefix. The dashboard is out of scope for this repository.

## Rollout

Slice 1: state envelope field, organization connect start/callback, listing,
binding through organization installations, tests. Slice 2: disconnect and
reconciliation surfaces, docs. Both release independently (the new state field
is ignored by an older apid only if the flow started on a newer one, which
then fails closed at the callback: the state does not verify).

## Validation

- A developer or viewer cannot start or complete an organization connect; an
  admin can. A member removed between start and callback is refused at the
  callback.
- A company project binds only through its organization's installation; a
  member's personal installation that can see the repository is ignored
  (403 `github_repo_not_accessible`).
- A personal project never resolves through an organization installation.
- A member's departure or erasure leaves the organization installation and
  the company project's binding intact.
- A tampered state (changed organization ID) fails verification.

## Rejected alternatives

- **Bind through the linking member's personal installation**: ties the
  company's deploys to one person and crosses the resource boundary.
- **A separate `org_github_installations` table**: duplicates sealing,
  reconciliation and webhook routing for no ownership benefit; the resource
  account already models company ownership.
- **Transfer an existing personal installation**: GitHub installations are
  per GitHub account, not per Gregale account; reconnecting through the
  organization flow is explicit and auditable.
