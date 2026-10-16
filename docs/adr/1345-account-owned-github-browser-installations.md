# ADR-1345: Account-owned GitHub installations for browser repository operations

- **Status:** accepted
- **Date:** 2026-10-16
- **Related:** ADR-012, ADR-116, spec §11
- **Supersedes:** ADR-116's session `GithubLogin` requirement for the browser
  repository picker and existing-app repository binding only.

## Context

Connect GitHub authorizes an installation using the signed-in Gregale account,
an action-bound CSRF token, a single-use OAuth state cookie, and GitHub's
user-to-server installation discovery and verification. It persists the
account/installation association without changing how the customer signed in.
A Google or password session therefore has no `GithubLogin` claim after a
successful connection. Requiring that separate login claim in the repository
picker and bind routes prevents the connected account from deploying.

## Decision

The browser picker and existing-app bind routes authorize the requested
installation through the authenticated account's durable installation row.
Look up the exact `(account_id, installation_id)` before calling githubd.
Missing and foreign installations return the same `403 github_install_not_owned`
problem, without exposing another account's identity or contacting GitHub.

Retain the live GitHub App installation verification. githubd's repository
listing and binding continue to resolve the token through the same account and
installation; binding also verifies current access to the requested repository.
An installation owner's login is audit metadata, not a browser identity claim.
Organization installations do not require the browser user to have the
organization's login.

The browser bind remains cookie-session authenticated and requires the named,
account-bound `bind_app_to_repo` CSRF proof. No bearer credential becomes a
browser session and no installation token is returned to the browser. Initial
connection still requires the existing OAuth proof; the legacy installation
callback and server-rendered creation path retain their existing login gates.

## Validation

Cover ordinary browser sessions with personal and organization connections,
foreign and unknown installation IDs even when a signed GitHub login matches
the foreign owner, live verification failure, and binding without CSRF. Reject
unowned installations before repository listing or persistence. Existing
githubd tests retain current repository visibility and account isolation checks.

## Rejected alternatives

- Requiring another GitHub sign-in can switch Gregale accounts and makes a
  successful account-preserving connection insufficient for deployment.
- Synthesizing `GithubLogin` from the installation owner misrepresents an
  organization as the authorizing user.
- Removing the login gate without checking the durable account/installation
  association would leave ownership enforcement dependent on downstream calls.
