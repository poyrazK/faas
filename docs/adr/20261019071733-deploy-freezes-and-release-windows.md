# ADR-20261019071733 · Deploy freezes and release windows

- **Status:** accepted (shipped in PR 5034, 2026-10-20); n2 metal drill passed 2026-10-26 (`cmd/e2e/release_guards_drill_metal_test.go`); amendment 1 (2026-10-26); amendments 2-3 (2026-10-28); amendment 4 (2026-10-29)
- **Date:** 2026-10-19
- **Follows:** [ADR-1446](1446-safe-releases-by-default.md) (production releases), [ADR-1445](1445-post-release-watch.md) and [ADR-625](625-first-wake-5xx-rollback-from-request-telemetry.md) (automatic rollbacks)
- **Decision:** An account can freeze production releases for all of its apps or for one app: for a fixed window, every week at set hours, or for a while after Gregale rolls a release back automatically. While a freeze is active, every path that creates a production deployment refuses with `409 release_frozen`, naming the freeze. A person can override one release with a reason, which is audited. Rollbacks are never frozen.
- **Why:** Teams stop releasing over holidays, on Friday evenings and while an incident is open. Today that is a convention each person has to remember. Nothing stops a CI job or a GitHub push from releasing into the window, and nothing records who released anyway.
- **Rejected alternatives:**
  - **Freezing in the CLI only.** GitHub deploys, CI calls through the API, project promotions and the SDKs would all bypass it. apid creates every deployment, so apid enforces the freeze.
  - **Holding rollouts already in progress.** A freeze that pauses a half-rolled-out canary leaves two revisions serving for the length of the freeze. In-progress rollouts finish. A freeze only stops new releases from starting.
  - **Freezing config changes.** Rotating a leaked secret during an incident must not need an override, and the config guard ([ADR-1451](1451-guarded-reversible-config-changes.md)) already reverses a bad config change. Config writes are not frozen.
  - **Freezing after every automatic rollback by default.** The next release after a rollback is usually the fix, so a default freeze would block it. The freeze after a rollback is a policy the team opts into.

## Decision

### Freezes

A freeze row (`release_freezes`) belongs to an account and covers either one app or, with no app, all of the account's apps. It has one of three kinds:

| Kind | Active when | Fields |
|---|---|---|
| `window` | `starts_at <= now` and (`ends_at` is null or `now < ends_at`) | `starts_at`, optional `ends_at` (null: until lifted) |
| `weekly` | `now`, in `time_zone`, falls inside a weekly occurrence: a set of weekdays, a start minute and a duration. An occurrence may run past midnight or into the next week. | `weekly_days` (bitmask, bit 0 = Sunday), `weekly_start_minute`, `weekly_duration_minutes`, `time_zone` (IANA) |
| `after_rollback` | never by itself. When Gregale rolls a covered app back automatically, apid creates a `window` freeze for that app, starting now and lasting `after_rollback_minutes`, with source `automatic_rollback`. | `after_rollback_minutes` |

Every freeze carries a `reason` (1–200 characters) and its creator. Lifting a freeze stamps `lifted_at` and `lifted_by`; the row is kept for the audit trail. An account may hold at most `api.ReleaseFreezesMax` unlifted freezes.

### What a freeze blocks

A freeze blocks creating a **production deployment**: deployment scope `default`, which is where a project's `production` environment deploys. It covers every creation path:
- the deployment API (image and JSON);
- multipart source deploys;
- upload sessions;
- git-ref deploys;
- GitHub push deploys;
- project image deploys;
- promotions into production.

It does not block:
- rollbacks, manual or automatic;
- rollouts already in progress;
- preview and other non-production scopes;
- config and secret writes.

A blocked request gets `409 release_frozen`. The problem lists the active freezes (`id`, `kind`, `reason`, `ends_at` for windows, creator) and the hint to override. A GitHub push deploy that hits a freeze is not created. The bridge reports the freeze as the failure reason for that commit.

### Override

A deploy request may carry `override_freeze` with a reason (1–200 characters). In the CLI this is `--override-freeze REASON`, and multipart deploys take the `override_freeze` form field. apid then creates the deployment and emits the audit event `release.freeze_overridden`, recording the freezes it overrode, the reason and the principal. Any principal allowed to deploy may override. The point of the override is that it is recorded, not that it is hard to do.

### Surfaces

- API:
  - `GET /v1/freezes[?app=SLUG]` lists unlifted freezes that are active, upcoming or policies.
  - `POST /v1/freezes` creates a freeze.
  - `DELETE /v1/freezes/{id}` lifts one.
  - `GET /v1/apps/{slug}/freeze-status` answers whether a production release would be refused now, and when the next window starts.
- CLI: `gregale freeze create|list|lift|status`, and `--override-freeze` on `gregale deploy`.
- Audit events: `release.freeze_created`, `release.freeze_lifted` and `release.freeze_overridden`. Automatic freezes are created with source `automatic_rollback` and the rolled-back deployment in their reason.

## Consequences

- A release refused by a freeze costs nothing: no build runs, no upload is kept and no deployment row exists.
- Weekly windows are evaluated in the freeze's time zone, so they follow daylight-saving changes.
- Freezes are per account. Org-wide freezes across several accounts are out of scope. (Superseded by amendment 4: a company organization owns its apps' freezes.)
- Project-level freezes, and freezes on non-production environments, are follow-ups. An app-level freeze on each app of a project covers the case meanwhile.

## Amendment 1 (2026-10-26): every path that releases into production

A release-tooling audit found paths that created or activated production releases without asking the freeze:
- deployment retries (`POST /v1/deployments/{id}/retry` and the dashboard retry form);
- GitOps graph activation in an environment;
- runtime upgrades.

All three now meet the freeze.

- **Production scopes.** A production release is one in the `default` scope or in a project's `production` environment, whose deploys and promotions use the scope `production` (`freeze.IsProductionScope`). The "What a freeze blocks" section said the production environment deploys to `default`; it deploys to `production`, and both are covered.
- **Retries.** A retry creates a new release from the failed one's inputs. A freeze refuses it with `409 release_frozen`. A retry carries no override: the refusal says to lift the freeze or deploy again with `--override-freeze REASON`, which records the override as before.
- **GitOps activation.** A graph prepared for the `production` environment is not activated while a freeze covers one of its member apps. Like the break-glass pause, the sync stays unconverged and activates on a later reconciliation once the freeze ends. An approved revision is not an override.
- **Runtime upgrades.** Staging an upgrade refuses an app a freeze covers (`runtimeupgrade.ErrReleaseFrozen`). It fails closed when freezes cannot be read, since a platform upgrade can wait. Upgrades have no customer route yet. Before customer admission ships, the cutover must also hold while a freeze is active, because a freeze can start between staging and cutover.
- **Metrics.** `faas_release_guard_decisions_total{guard="freeze",outcome="refused|overridden"}` counts refusals and overrides on every path (`pkg/releasemetrics`). The runbook is `docs/runbooks/FaasReleaseGuards.md`.
- **Dashboard.** `/dashboard/apps/{slug}/release-safety` shows the active and next freezes, alongside the app's gates, approvals and scheduled releases.

## Amendment 2 (2026-10-28): runtime upgrade cutover holds while frozen

Amendment 1 left one gap: a freeze that starts after staging, while the
upgrade's candidate builds, did not stop the cutover. Now the runtime upgrade
executor reads the app's freezes before every advance
(`runtimeupgrade.ReleaseFreezeHold`). While one is active, a ready upgrade
keeps waiting (phase `waiting`, no blocker) instead of switching traffic, and it
completes once the freeze lifts or ends. A freeze read that fails holds the
upgrade too: the iteration fails and its lease expires. The upgrade's deadline
still applies, so a long freeze ends it as `deadline_exceeded`. The claim
carries the account, app and serving deployment from the row it already
returns, so no query or schema changes.

## Amendment 3 (2026-10-28): releases parked before a freeze, and the override audit

- **A parked release waits for the freeze.** A freeze refuses creating
  production releases, but a release deployed without traffic before it began
  could ship during it through `traffic`, promote or a rollback to it, with no
  override and no audit. Giving traffic to a live release that is at 0% by
  choice, has never completed a rollout and was created before the active
  freeze's current start is refused with `409 release_frozen`. A release
  deployed during the freeze passed the freeze check, or its audited override,
  when it was created. A release that completed a rollout served before; going
  back to it is a rollback, which freezes never refuse.
- **The override is audited once the release exists.** A deploy emitted
  `release.freeze_overridden` at the freeze check, so a deploy that then failed
  validation left an override for a release that never existed. The deploy
  paths now emit it after creating the deployment, naming it.

## Amendment 4 (2026-10-29): a company organization owns its apps' freezes

A freeze belonged to an account, but a company app's rows stay under the
account of the member who created it
([ADR-202610171904](202610171904-organization-resource-lifecycle.md)). Only
that member could freeze the app: another admin's `POST /v1/freezes` naming it
got 404. No freeze could cover all of a company's apps. A creator's freeze on
all of their apps also froze the company apps they had created, and their
freezes on company apps went away with their account.

- **Owner.** A freeze belongs to an account or to a company organization
  (`release_freezes.org_id`; exactly one of `account_id` and `org_id`, checked
  by `release_freezes_one_owner_chk`). A company app is covered by its
  organization's freezes, on all of its apps or on that app. A personal app,
  including one in a personal organization, is covered by its account's
  freezes. An account freeze on all apps no longer reaches company apps.
  `freeze.Owner` resolves the owner from the app's persisted `org_id`, and
  every place that checks a freeze reads the app's owner through it: the
  deploy paths, GitHub pushes, GitOps activation, retries, parked releases,
  scheduled releases at scheduling and at start, runtime upgrade staging and
  cutover, and after-rollback policies. An automatic freeze belongs to the
  owner of the policy that created it. Each owner holds at most
  `api.ReleaseFreezesMax` unlifted freezes. A company's freezes are removed
  with the organization, not with any member's account.
- **Routes.**
  - `GET|POST /v1/apps/{slug}/freezes` and `DELETE /v1/apps/{slug}/freezes/{id}`
    list the freezes covering an app and create or lift a freeze on it, for a
    personal or a company app. They take the app's organization access check
    (`pkg/apid/appaccess`).
  - `GET|POST /v1/orgs/{slug}/freezes` and `DELETE /v1/orgs/{slug}/freezes/{id}`
    hold a company's freezes on all of its apps. A personal organization's
    route answers 409: its apps use `/v1/freezes`.
  - `/v1/freezes` holds the account's freezes. Naming a company app there is
    refused with 409 and the routes that hold its freezes, instead of creating
    a freeze that would cover nothing.
  - A freeze in a response carries `org`, the owning organization's slug.
- **Roles on a company.** Every member reads freezes (`org.read_app`). A
  member who deploys (`org.deploy_app`) may create a window, since stopping
  releases is the safe direction, and lift a window they created. Weekly and
  after-rollback freezes are standing policies, so only owners and admins
  (`org.manage_app_security`) create them, as with release gates and the
  approval policy; they also lift any freeze. Overriding a freeze for one
  release is unchanged: anyone who may deploy, with an audited reason.
- **Audit and activity.** `release.freeze_created`, `release.freeze_lifted`
  and `release.freeze_overridden` keep their names and add `org_id` for a
  company's freeze. They also appear in the organization's activity timeline,
  with Gregale as the actor of an automatic freeze.
- **CLI.** `gregale release freeze create|list|lift` take `--org ORG` for a
  company's freezes. `--app` uses the app routes, so it works on company apps.
  `--all-apps` freezes your personal apps.

Freezes existing before this amendment keep their account. On 2026-10-29
production held three freezes, all lifted, so none needed converting.
