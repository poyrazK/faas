# ADR-20261021152201 · Scheduled releases

- **Status:** accepted (shipped in PR 5126, 2026-10-22); n2 metal drill passed 2026-10-26 (start on time, blocked by a freeze)
- **Date:** 2026-10-21
- **Uses:** safe releases by default ([ADR-1446](1446-safe-releases-by-default.md)), deploy freezes ([ADR-20261019071733](20261019071733-deploy-freezes-and-release-windows.md)), release approvals ([ADR-20261020085706](20261020085706-release-approvals.md)), traffic splitting (issue #556)
- **Decision:** A production deploy may carry `release_at`. Gregale builds the release now and parks it: the release goes live at 0% traffic, ready to wake, while the current release keeps serving. At `release_at`, apid starts the rollout the deploy would have had: the same canary ladder, or a full cutover when the deploy had no canary. Before it starts, apid checks again that no newer release serves, that no deploy freeze is active, and that the app's approval policy can hold the release. If any check fails, the release stays parked and the schedule records why. `PUT /v1/deployments/{id}/schedule` schedules a release that is already parked. `gregale deploy --at`, `gregale traffic promote --at` and `gregale scheduled-releases` expose it.
- **Why:** Teams release at quiet hours, at a launch time, or at the start of a working day when people are around to watch. Today someone has to be awake to run the deploy, or a CI cron runs it without the freeze and approval checks a person would hit. Building at release time also wastes the window: the build can fail, or take minutes, at the moment nobody is watching.
- **Rejected alternatives:**
  - **Building at the scheduled time.** A failing build or a slow registry then fails the release at 2 a.m. Building early surfaces build problems while the team is still there, and the start is then only a traffic change.
  - **Holding the deployment before it goes live.** Keeping a built deployment in `snapshotting` for hours would trip the stuck-deploy checks and the deploy wait in the CLI, and imaged would have to own a timer. A parked release is an ordinary live row at 0% traffic, which the gateway, schedd and the rollback paths already handle.
  - **Starting the canary at 0% and holding its first step.** The canary worker, the stuck-rollout auto-abort and the post-release watch would all see a rollout in progress for hours. A parked release carries no canary ladder until it starts, so none of them act on it.
  - **Checking freezes only when scheduling.** A freeze created later for an incident, or the freeze after an automatic rollback, must stop a release that was scheduled before it. Freezes are checked when scheduling, as a preview of the release time, and again when the release starts.
  - **Waiting out a freeze, then starting.** A release meant for 2 a.m. that starts at 9 a.m. when a freeze ends surprises everyone. A blocked release stays parked until someone schedules or promotes it again.
  - **A server-side cron for recurring releases.** Releases are one-off decisions. Recurring cadence belongs to the CI system that produces the builds.

## Decision

### Scheduling

`release_at` is an RFC 3339 time no earlier than one minute in the past and at most `api.ScheduledReleaseMaxAhead` (7 days) ahead. Every deploy path that creates a production deployment accepts it:
- the image deploy JSON;
- multipart source deploys;
- source tarballs with a JSON sidecar;
- upload sessions;
- git-ref deploys.

GitHub push deploys and project promotions do not; they release when their source changes.

A scheduled deploy must target the production scope of a request-mode app, the scope the default canary covers. It may not set `traffic_percent`, because parking sets it. Service, worker and job apps roll out behind schedd's readiness gate and are refused.

When the deploy is accepted, apid:
1. evaluates deploy freezes **at `release_at`**, not now. A freeze active at that time refuses the deploy with `409 release_frozen`, unless the deploy carries `override_freeze`. The override then covers exactly those freezes; their IDs are recorded with the schedule. Building during a freeze is allowed, because a parked release takes no traffic.
2. applies the default release policy (ADR-1446) and the approval rule that a gated app needs a canary, exactly as for an immediate deploy;
3. records the resulting canary choice on the schedule and creates the deployment with `traffic_percent = 0`. The deployment builds, passes readiness, and goes live without traffic.

`release_notify` names up to `api.ScheduledReleaseNotifyChannelsMax` Slack or email channels. They hear when the release starts and when it cannot start.

An app has at most one pending schedule. Scheduling another release of the same app cancels the earlier schedule, whose release stays parked.

### Scheduling a parked release

`PUT /v1/deployments/{id}/schedule` takes `release_at`, optional `canary`, `release_notify` and `override_freeze`. It schedules, or reschedules, a release that is live at 0% traffic without a canary ladder. Such a release comes from an earlier schedule, from a schedule that was blocked, or from a deploy with `traffic_percent: 0`. Without `canary`, the release gets the canary the default release policy would give a deploy now. A `release_at` that has passed starts the release on the worker's next pass, so `--at now` releases a parked deployment through the same checks.

`DELETE /v1/deployments/{id}/schedule` cancels a pending schedule. The release stays parked. `GET /v1/deployments/{id}/schedule` returns the latest schedule of a release, and `GET /v1/scheduled-releases[?app=SLUG]` lists the account's schedules, pending ones first.

### Starting

An apid worker looks for due schedules every `api.ScheduledReleaseCheckInterval` (15 seconds). Concurrent apid processes are safe, because the start runs in one transaction that rechecks the schedule's state. For each due schedule it decides:

| Condition | Outcome |
|---|---|
| The build failed or was cancelled | `cancelled`, code `deployment_failed` |
| A newer release of the app serves traffic, or replaced the parked one | `superseded` |
| Someone gave the release traffic by hand before its time | `cancelled`, code `released_manually` |
| The build is still running, or an older rollout of the app is still in progress | Wait, and look again on the next pass |
| Still waiting `api.ScheduledReleaseStartGrace` (1 hour) after `release_at` | `expired`, code `not_ready` |
| A deploy freeze is active that the schedule did not override | `blocked`, code `release_frozen` |
| The app now has an approval policy and the release has no canary to hold in | `blocked`, code `release_approval_canary_required` |
| A traffic gate refuses the change, such as a binding release check | `blocked`, code `start_refused`, with the gate's reason |
| Otherwise | `started` |

Starting is one store transaction, `StartScheduledRelease`. It is the traffic change behind `PATCH /v1/deployments/{id}/traffic`, so the same gates apply: the lifecycle and route-removal guards, binding release checks, MCP compatibility, and the refusal to change traffic while another canary is in flight. In the same transaction it marks the schedule started and does one of two things:
- **With a canary ladder and a serving release:** it gives the release the first stage's traffic and the siblings the remainder, and stamps the ladder on the release. It sets `rollout_state = rolling_out`, `rollout_started_at = now` and the step start time to now. From there the canary worker treats it like any new canary, including approval holds and gates.
- **Without a ladder, or with nothing serving:** it gives the release 100% traffic and completes the rollout, as `gregale traffic promote` does. The previous release stays live without traffic until the next release replaces it.

apid then notifies the gateway, and emits the audit events `release.schedule_started` and, for an override that took effect, `release.freeze_overridden`.

`blocked` and `expired` emit `release.schedule_blocked` and notify the schedule's channels. A blocked release stays parked: scheduling it again, or promoting it by hand, releases it.

### Interaction with approvals and traffic changes

- A parked release has completed no rollout and takes no traffic, so approvals do not gate it while it waits. When it starts, its rollout begins at that moment. The approval rule "releases whose rollout started before the policy never wait" now looks at both the creation time and `rollout_started_at`. A policy added between scheduling and the start therefore gates the release.
- Raising the traffic of a release with a pending schedule is refused with `409 release_scheduled`. Cancel the schedule or reschedule it first. A parked release of an app with an approval policy can gain traffic only through a schedule, which starts a canary that can hold. A traffic change would skip the approval.

### Notifications

Schedule outcomes use the release notification outbox of ADR-20261020085706 (`release_approval_events`) with two more events: `schedule_started` and `schedule_blocked`. meterd delivers them to Slack and email like approval events. Their Slack message has only the Review button.

## Consequences

- A parked release holds a built snapshot and a live deployment row for up to seven days. It costs disk, not RAM: nothing routes to it, so it never wakes.
- A parked release that is never started is replaced by the next release, like any 0% deployment.
- Times are absolute. The CLI turns local times and offsets such as `+2h` into RFC 3339 before sending. Daylight-saving shifts between scheduling and the start do not move a release.
- Recurring releases, scheduled project promotions and scheduled config changes are out of scope.
