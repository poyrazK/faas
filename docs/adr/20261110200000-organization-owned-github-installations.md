# ADR-20261110200000: Company projects link GitHub through an organization-owned installation

- **Status:** accepted
- **Date:** 2026-11-10
- **Related:** ADR-012 (githubd), ADR-1345 (account-owned browser installations),
  ADR-20261101160300 (GitHub connector app bindings),
  ADR-202610171905 (organization project ownership)

## Context

A company project (an adopted organization's project, `projects.account_id`
= the organization's resource namespace) could not link a repository. The
shared project update refused `repo_full_name` with 409 "Repository ownership
migration required" (`handlers_projects.go`), because every GitHub
installation row was owned by the member who connected it
(`github_installations.account_id`, a foreign key to `accounts`). Resolving a
company project's repository through a member's personal installation would
make the company's deploys depend on that member: their departure, erasure or
disconnect would silently stop the company's pushes, and the member's personal
installation would reach the company's resource namespace. Apps/projects
review 2026-11-10, P4.

The owner chose an organization-owned installation, ADR first.

## Decision

1. **Ownership.** An adopted organization's resource namespace (the
   organization's ID) can own GitHub installations: a `github_installations`
   row whose `account_id` is that namespace. No new table; the row shape,
   sealing and reconciliation are unchanged. Migration
   `20261110164822932_github_installations_namespace_owner` moves the
   column's foreign key from `accounts` to `resource_namespaces`, as
   `projects.account_id` already is (every account is also a namespace). The
   drop of the old key makes it a contract migration. Only adopted, active,
   non-personal organizations can own one. An installation still belongs to
   one Gregale namespace (`github_installations_installation_id_uidx`): an
   installation already connected personally must be disconnected there
   before the organization connects it, else the connect answers
   `github_account_conflict`.

2. **Connecting.** The existing Connect GitHub form
   (`POST /dashboard/install/connect`) accepts an optional `organization`
   slug. apid checks, at the start and again at the OAuth callback, that the
   signed-in member is a current owner or admin of an active, adopted
   organization. The sealed OAuth state carries the organization ID (a new
   `omitempty` field `o`), so the callback cannot switch targets; the callback
   hands githubd the organization's namespace in place of the member's
   account. The installation chooser and the App-install round trip keep the
   organization. Everything else in the handshake (state cookie, CSRF action,
   GitHub user-to-server verification) is unchanged. The installation's audit
   login remains the authorizing GitHub user; apid also records
   `org.github.connected` with the connecting member.

3. **Binding.** A company project resolves `repo_full_name` against the
   organization's installations only, and the binding is written in the
   project's namespace. Linking needs the action that already guards
   `PATCH /v1/orgs/{organization}/projects/{slug}`
   (`authz.OrgActionManageProjectSecurity`: owners and admins). A member's
   personal installation is never consulted for a company project; without an
   organization installation the link answers 404
   `github_install_not_found`. Personal projects are unchanged. Source uploads
   to a company project that carry a personal installation binding stay
   refused; linking goes through the project update.

4. **Lifecycle.** A member's departure or erasure does not touch the
   organization's installation. Deleting the organization's namespace
   cascades the row. Webhooks route by installation ID and the bound project,
   so githubd needs no change: its exchange already takes the owning account.

5. **Surfaces.** `GET /v1/orgs/{organization}/github/installations` lists
   the organization's installations (ID and authorizing GitHub login) to its
   members; tokens never leave the server.

## Rollout

Slice 1: migration, state field, organization connect start and callback,
listing, binding through organization installations, tests.
Slice 2: the organization page shows the company's installations to members;
owners and admins get a Connect GitHub form that sends the `organization`
field and returns to the page, and a CSRF-protected Disconnect form
(`POST /dashboard/orgs/{slug}/github/{installationID}/disconnect`, role
rechecked). Disconnecting deletes the row, which unlinks projects and apps
stored in the company's namespace, and also unlinks company projects still
stored under a creator's namespace; it records `org.github.disconnected`.
Live re-verification of a company installation stays with githubd's
reconciliation, as for personal ones. During a mixed
apid rollout, a flow started on a new apid and completed on an old one
ignores the organization and connects the installation to the member's own
account, which that member could already do; nothing reaches the
organization.

## Validation

- A developer cannot complete an organization connect, an outsider gets 404,
  an admin connects for the organization's namespace; the role is rechecked
  at the callback.
- A state sealed for one member does not open for another and keeps the
  organization it was sealed for.
- A company project binds only through its organization's installation; an
  admin's personal installation that sees the repository is ignored
  (404 `github_install_not_found`); developers cannot link.
- Members list only the organization's installations.

Tests: `TestCompanyProjectLinksThroughTheOrganizationsInstallation`,
`TestOrganizationGitHubConnectNeedsAnOwnerOrAdmin` (pkg/apid/apidserver).

## Rejected alternatives

- **Bind through the linking member's personal installation**: ties the
  company's deploys to one person and crosses the resource boundary.
- **A separate `org_github_installations` table**: duplicates sealing,
  reconciliation and webhook routing for no ownership benefit; the resource
  namespace already models company ownership.
- **Transfer an existing personal installation**: reconnecting through the
  organization flow is explicit and auditable.
