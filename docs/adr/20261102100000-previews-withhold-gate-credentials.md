# ADR-20261102100000: Previews withhold platform gate credentials; losing the repository retires whole PR environments

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-079 (per-app public auth), ADR-20261031180000 (preview gates), ADR-095 (PR preview lifecycle)
- **Context:**
  - A PR preview keeps its production app's access gates (ADR-20261031180000): the basic-auth password, `require_authn` and bearer public auth. The gateway verified the credential and then forwarded the `Authorization` header to the app unchanged. For a preview the app is a pull request's unreviewed code, so it could read the production app's password when a reviewer opened the preview, or the Gregale API key a reviewer or CI job presented (any key of the owning account with `apps:read` or `admin`, with whatever other scopes it holds). Someone who can push a branch but has no production access could collect either.
  - Revoking the GitHub App installation, or removing a repository from it, retired only previews whose own production app carries the GitHub binding. In a project only the bound app carries it, so the previews of the project's other services in a PR environment kept serving PR code to the end of their lease (7 days by default, up to 30) and kept holding the preview allowance: no close event can reach them. The project also stayed on the gone installation, so after a reinstall, reconnecting the app did not bring the project back.
- **Decision:**
  1. On a preview, the gateway removes the `Authorization` header after a platform gate that reads it (`require_authn`, public auth `bearer` or `basic`) has passed, before anything reaches the app. The request stays marked as credentialed, so shared caches still bypass it as before. Production apps, and previews whose `Authorization` no platform gate reads (an app's own scheme), are unchanged.
  2. A revoke or a repository removal retires every preview whose PR environment previews an app bound through that installation (and repository), found through the PR's preview set as an unbind already does, and does not reset previews already being torn down.
  3. The same events detach the installation's projects (`install_id` cleared, repository and branch kept). Binding an app of the project to the same repository again restores the project's installation, so a reinstall needs no separate project rebind.
- **Consequences:**
  - Preview code no longer sees the platform credential; the gate has already authenticated the caller.
  - End-user tokens the app's own edge rules (`jwt`) verify still reach it: they are the customer's tokens, not Gregale's or the production app's.
  - No migration. A project detached by a revoke shows as not connected until an app of it is reconnected.
- **Rejected alternatives:**
  - **Strip the header for every app.** Production apps may read their own gate credential today; that is the account's own reviewed code and a separate decision.
  - **Give previews their own password.** It does not cover API keys, and reviewers would need a second secret per app.
