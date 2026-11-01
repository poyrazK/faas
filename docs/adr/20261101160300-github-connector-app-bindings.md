# ADR-20261101160300: GitHub connector — app bindings deploy, connect works for every sign-in

- **Status:** accepted
- **Date:** 2026-11-01
- **Amends:** ADR-1345 (account-owned GitHub installations: the remaining
  GitHub sign-in gates on `/oauth/callback` and the dashboard wizard go),
  ADR-050 (push dispatch: a repository bound only to apps deploys them).
- **Checked against:** ADR-012 (githubd owns GitHub), spec §11 (ownership is
  proved by GitHub, never by a request field), `docs/release-compatibility.md`
  (release N beside N-1; expand-only migrations), `CLAUDE.md` (apid writes
  customer intent; githubd only reads the trigger it adds).

## Context

`github-deploys` is GA in the capability catalog, but an investigation on
2026-11-01 found that its main promise did not hold, and that its acceptance
test exercised a stub RPC that returns an error in production:

1. **A push never deployed an app bound at the app level.** The wizard,
   `gregale github bind` and both bind APIs write the binding on the app row.
   githubd's push path then required a `projects` row bound to the same
   repository and deployed that project's apps. Nothing creates that row for
   an app, so the push was dropped as "no binding" while the connection read
   healthy (journey `TestGitHubDeploys/push_to_deploy`, failing since
   2026-10-27).
2. **The dashboard wizard required a GitHub sign-in.** Connect GitHub
   deliberately does not add a GitHub login to a Google or password session
   (ADR-1345), so those customers looped on "Connect GitHub". The install
   callback (`/oauth/callback`, GitHub's Setup URL) had the same gate and sent
   them to `?github=unauthenticated` after installing the App.
3. **A GitHub user who could see more than one installation could not
   connect.** Connect GitHub sent no installation, and githubd refused to
   guess; the customer saw 502 "Could not reach GitHub". The same 502 hid the
   organization-admin, owner and pending-permission refusals.
4. **Defaults picked `main`.** A connection stored `main` as its default
   branch and a bind without a branch used it, so a repository that deploys
   from `master` or `trunk` bound a branch that never receives pushes.
5. **An app without a project could not hand production to GitHub Actions.**
   `gregale github setup` always set the project policy's production trigger
   and failed with `project_required` after it had already bound the app.
6. **Renames detached bindings.** The production App was not subscribed to
   `repository` events, an organization rename (`installation_target`) was
   ignored, and the installation sync matched bindings by name, so a rename
   detached the binding even though its immutable repository ID was stored.
   Repository renames also left `projects.repo_full_name` stale.
7. **Accounts with several installations flipped between them.** "The
   account's installation" was the row sealed most recently, and token
   rotation re-seals hourly.
8. The durable webhook inbox's event check refused `issues`, which githubd
   enqueues for issue-tracker callbacks (ADR-1476).

## Decision

1. **A push deploys the apps bound to its repository when no project is.**
   githubd keeps the project path unchanged. When `ProjectByRepo` finds no
   project, it builds every live app of the account bound to the pushed
   repository, branch and installation, with no reconcile. A project-backed
   app bound at the app level follows its project's policy and is skipped when
   that project is bound to another repository.
2. **An app without a project stores its production trigger on its
   binding** (`apps.github_production_trigger`, NULL = webhook). The deployment
   policy API reads and sets only that field for such an app; the other policy
   fields stay project settings and answer 409 `project_required`. Unbinding
   clears it; a re-bind keeps it.
3. **The account's durable connection authorizes an installation in every
   browser flow.** `/oauth/callback` persists nothing and redirects to GitHub
   authorization with the installation bound into the OAuth state, as the
   code callback already proves ownership (owner of a personal installation,
   active admin of an organization). The wizard checks the durable
   `(account, installation)` row instead of a GitHub login, and binds an
   existing app with `?app=<slug>`.
4. **Connect GitHub offers a choice.** With no installation in the state,
   githubd connects the only installation the GitHub user may connect, or
   returns the eligible ones (at most 20) as gRPC ErrorInfo metadata; apid
   renders a chooser whose options restart Connect GitHub with that
   installation. Refusals are typed problems (`github_app_not_installed`,
   `github_installation_owner_required`, `github_org_admin_required`,
   `github_permission_pending`, `github_installation_not_accessible`).
5. **No installation default branch.** A bind without a branch takes the
   repository's default branch, which githubd reads while it verifies access.
6. **Renames follow the repository ID.** githubd handles `installation_target`
   renames, the installation sync renames a binding whose repository ID GitHub
   still lists under a new name, and repository renames move projects too.
7. **Several installations are listed, never guessed.** Connection status
   lists them; `GET /v1/github/repos` and `gregale github repos|bind|setup`
   take an installation and answer 409 `github_installation_required` when
   one is needed.
8. **App settings are watched.** githubd reads `GET /app` with each delivery
   log poll and exports `githubd_github_app_config_missing{kind,name}`;
   `FaasGithubAppConfigDrift` names a missing event or permission.

## Compatibility

Both migrations are expand-only. An older githubd ignores the trigger column
and writes a subset of the widened event check. A new githubd keeps the phrase
an older apid matches for "App not installed"; an older apid shows the new
refusals as before (502). A new apid accepts an older githubd's untyped errors
and its `main` default branch. The ExchangeOAuthCode proto is unchanged.

## Rejected alternatives

- **Create a single-app project at bind time.** A project reconciles the
  repository's workloads on every push and can add or retire apps; binding a
  repository to one app must not change what the account runs.
- **Connect every installation the user may connect.** It would claim
  organization installations the customer did not choose for this account.
- **Keep the GitHub sign-in gates and ask customers to sign in with GitHub.**
  A GitHub sign-in can switch Gregale accounts (ADR-1345).

## Validation

Unit tests cover the app-bound push (one and several apps, Actions trigger,
deleted app, project bound elsewhere), the installation choice and refusals,
the wizard for a password session and an existing app, the default branch,
the installation listing, renames by ID and `installation_target`, and the App
settings check. The acceptance test for `github-deploys` is the app-bound push
test. The journey's `push_to_deploy` known failure is removed once the suite
passes against a release with this change.
