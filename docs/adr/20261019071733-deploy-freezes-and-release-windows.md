# ADR-20261019071733 · Deploy freezes and release windows

- **Status:** proposed
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
- Freezes are per account. Org-wide freezes across several accounts are out of scope.
- Project-level freezes, and freezes on non-production environments, are follow-ups. An app-level freeze on each app of a project covers the case meanwhile.
