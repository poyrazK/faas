# ADR-20261020085706 · Release approvals

- **Status:** accepted (shipped in PR 5126, 2026-10-22); n2 metal drill passed 2026-10-26 (hold, self-approval refusal, approve, reject, notices); amendments 1-2 (2026-10-26, 2026-10-27), 3 (2026-10-28)
- **Date:** 2026-10-20
- **Amends:** [ADR-1446](1446-safe-releases-by-default.md) (the default canary), [ADR-1447](1447-explained-rollout-decisions.md) (rollout decision rules)
- **Uses:** organization roles ([ADR-061](061-organizations-and-memberships.md)), deploy attribution (`deployments.deployed_by_user_id`), release freezes ([ADR-20261019071733](20261019071733-deploy-freezes-and-release-windows.md))
- **Decision:** An app can require people to approve each production release before the release takes more than its first canary stage.
  - The release starts its canary as usual. Then it waits at the stage the policy names until enough eligible people approve it.
  - By default an approver must be an organization owner or admin, and must not be the person who deployed.
  - A rejection aborts the rollout and restores the previous release.
  - A release still waiting when the timeout passes is aborted the same way.
  - While a release is waiting, nothing can give it more traffic: not the canary worker, a manual advance, a promote, or a traffic split.
- **Why:** Teams with change-control duties (SOC 2 CC8.1, regulated customers, or simply "two people sign off on prod") need a second person to approve production changes. Gregale can freeze releases and gate them on signals, but it cannot make a release wait for a human decision. Teams run approvals outside Gregale (CI environments, chat threads), so the record of who approved what is not attached to the release, and nothing stops a deploy from the CLI from skipping it.
- **Rejected alternatives:**
  - **Approving before any traffic.** The canary sets the first stage's share when the deployment is created, and the first stage's evidence (requests, errors, latency) is what an approver wants to see. A team that wants approval before any traffic deploys with `--no-traffic`, gets the approval, and then runs `gregale traffic set`; raising a waiting release's traffic needs approval too, so this order is enforced.
  - **Approvals for releases without a canary.** An `immediate` release takes all traffic at once, so there is no point to wait at. When an app requires approvals, its production releases must use a canary, and a release that would not is refused.
  - **Approving with a signed token outside Gregale (like project-environment approvals).** Those approve a reviewed plan before it is applied. A release approval is about a running candidate and its evidence, so it attaches to the deployment.
  - **Re-checking an approver's role at advance time.** The approval records the role the person had when they approved. Removing someone from the organization does not withdraw their past approvals, just as it does not undo their past deploys.
  - **Approving inside Slack without opening Gregale.** The native Slack app ([ADR-1496](1496-native-slack-response-actions.md)) can act on button clicks, but its identity links, delivery snapshots and link-confirmation page are built around issues. Reusing them for releases would mean a second security model to keep in step. Instead, approval messages carry buttons that open a Gregale page, and the person approves there under their own session, organization role and MFA. In-Slack actions can follow by extending ADR-1496's identity links.
  - **Sending notifications from apid during the request.** Channel delivery (sealed targets, retries, the mail transport and delivery metrics) lives in meterd's alert evaluator. apid records each event durably and meterd delivers it, so a slow Slack never delays a decision or the canary worker.

## Decision

### Policy

An app has at most one approval policy (`app_release_approval_policies`):

| Field | Meaning | Default and bounds |
|---|---|---|
| `required_approvals` | How many distinct eligible people must approve. | 1 to `api.ReleaseApprovalsRequiredMax` (5) |
| `after_stage` | The canary stage the release waits in, counted from 1. When the release's ladder has fewer stages before 100%, it waits before its final stage. | 1; 1 to `api.ReleaseApprovalStageMax` (10) |
| `timeout_minutes` | How long a release may wait before it is aborted. The clock starts when the release enters the waiting stage. | `api.ReleaseApprovalTimeoutDefault` (24 h); 10 min to 7 days |
| `approvers` | `admin`: organization owners and admins. `developer`: also developers. | `admin` |
| `allow_self_approval` | Whether the person who deployed counts as an approver. | `false` |

The policy applies only to request-mode apps on plans that allow traffic splits, because the wait happens in a canary stage. Setting it is refused when the app's `release_policy` is `immediate`.

An app outside an organization has one person, so for it `approvers` is ignored and `allow_self_approval` is always true. The approval still makes that person confirm the canary's evidence before the release continues.

Changing or removing the policy needs the organization's `manage_app_security` action (owners and admins). Otherwise a developer could switch approvals off to ship a release.

### Which releases need approval

A deployment needs approval when all of these hold:
- its app has a policy;
- it is in the app's production lane: the default scope, or the project
  `production` environment's scope (amended 2026-10-26, see Scope);
- it has never completed a rollout and is not the app's serving release;
- it was created, or its rollout started, after the policy. A scheduled release ([ADR-20261021152201](20261021152201-scheduled-releases.md)) starts its rollout at its time, so a policy set while it waited applies to it.

A rollback restores a release that already served, so rollbacks never wait. An app's first release has nothing to compare against and does not wait.

### At release time

After the ADR-1446 default fills in the rollout, a production release of an app with a policy and a live deployment must have a canary. Otherwise it is refused with `409 release_approval_canary_required`. That covers:
- `--rollout immediate`;
- an explicit `--traffic-percent` above 0;
- a canary worker that is unavailable.

The check runs on the same paths as freezes: JSON and image deploys, multipart source deploys, tarball deploys, upload sessions, git-ref deploys and GitHub pushes. `--no-traffic` (a dark deployment) is allowed, because it takes no traffic. Giving that release traffic by hand later is refused with `release_approval_canary_required`; it starts through a schedule, whose canary can wait (ADR-20261021152201).

### Waiting

When the canary reaches the waiting stage and its timer ends, meterd asks apid to advance as usual. apid refuses with `409 release_approval_required` until the release is approved:
- It records a `hold_awaiting_approval` rollout decision, with a summary such as "waiting for 1 more approval".
- meterd skips the release until its next tick, as it does for the route and profile gates.
- The canary circuit breaker and release gates keep judging the waiting stage, so a regression still aborts the release while it waits.

When the timeout passes, apid aborts the rollout on the worker's next request, with rule `abort_approval_timeout`, and restores the predecessor.

The same refusal applies to every other path that would raise the release's traffic:
- `POST /v1/deployments/{id}/canary/advance`;
- the `advance` and `promote` actions of `rollouts recover`;
- `POST /v1/deployments/{id}/promote`;
- `PATCH /v1/deployments/{id}/traffic` with a larger share.

Lowering traffic and aborting are never blocked.

### Approving and rejecting

`POST /v1/deployments/{id}/approvals` with `decision: approve | reject` and an optional `comment` (at most `api.ReleaseApprovalCommentMax` characters) records one decision per person per release (`deployment_release_approvals`).
- **Who may decide:** the person's organization role must match `approvers`; otherwise the response is `403 org_role_forbidden`.
- **The deployer:** when self-approval is off, the deployer's approval is refused with `403 release_approval_self`. The deployer may still reject their own release.
- **When it applies:** an approval counts toward `required_approvals` as soon as it is recorded, and the next canary tick advances the release. A release can be approved before it reaches the waiting stage.
- **Rejection:** a rejection is final. It aborts an in-flight rollout through the same path as a manual abort, with rule `abort_approval_rejected`. A rejected release can never gain traffic again.
- **Aborted releases:** a release whose rollout was aborted (by a rejection, the timeout, the circuit breaker or a person) accepts no more decisions. It can gain traffic again only if it was approved before the abort; otherwise it is deployed again.

`GET /v1/deployments/{id}/approvals` returns the release's approval status:
- `state`: `not_required`, `waiting`, `approved`, `rejected` or `expired`;
- the decisions so far;
- the deployer;
- when the wait started and when it expires.

`GET /v1/apps/{slug}/release-approvals` lists the app's releases that are waiting.

Every policy change and decision is audited:
- `release.approval_policy_set`;
- `release.approval_policy_removed`;
- `release.approved`;
- `release.rejected`.

### Notifications and the approval page

The policy names up to `api.ReleaseApprovalNotifyChannelsMax` notification channels (`notify_channels`). They must be Slack or email channels: PagerDuty is refused, because a release waiting for approval is not an incident.

apid records one event per release and kind in `release_approval_events`:
- `requested` when the release starts waiting;
- `approved` when the approval that satisfies the policy is recorded;
- `rejected` on a rejection;
- `expired` when the timeout aborts the release.

The unique key on release and kind makes each event exactly-once, whichever path triggers it.

Each event snapshots its message: app, release, deployer, commit, stage and traffic, approvals so far, the deciding person and comment, the expiry, and the review link. A later policy change does not rewrite a sent message.

meterd's alert evaluator delivers due events on its tick through the same channel sender as alerts. It records each channel's outcome on the channel, and retries an event whose channels all failed, with backoff, up to `api.ReleaseApprovalNotifyAttemptsMax` times. A deleted channel is skipped.

Slack messages for `requested` carry Approve, Reject and Review buttons. They are link buttons to `/dashboard/releases/{deployment}/approval` on the console host; they grant nothing by themselves.
- **Signing in:** the page needs a signed-in session. An unauthenticated visitor is sent to sign in and returned to the page.
- **Acting:** the page renders no release data on the server. Its script reads and decides through `GET` and `POST /v1/deployments/{id}/approvals` with the session, so the API's organization access, role check, MFA, same-origin check and self-approval rule all apply unchanged.
- **Rejecting:** a rejection needs a reason typed on the page.

Email carries the same links.

### CLI

| Command | Does |
|---|---|
| `gregale approvals require [--app <app>] --approvals N [--after-stage N] [--timeout 24h] [--approvers admin\|developer] [--allow-self-approval] [--notify CHANNEL,...]` | Sets the policy. |
| `gregale approvals policy`, `gregale approvals off` (with `--app`) | Shows or removes the policy. |
| `gregale approvals pending [--app <app>]` | Lists the app's releases that are waiting. |
| `gregale approvals status <deployment\|vN>` | Shows one release's status and decisions. |
| `gregale approvals approve <deployment> [--comment]`, `gregale approvals reject <deployment> --reason` | Decides on a release. |

`gregale deploy`'s live view shows when a release is waiting and prints the approve command.

### Scope

Named environment scopes and project environments are not covered. Project environments keep their own promotion approvals.

*Amendment (2026-10-26).* The `production` environment's scope later became
the app's production routing lane (`state.ProductionRoutingDeployments`), and
release freezes already treat it as production (`freeze.IsProductionScope`).
A release deployed straight into it, with `--environment production` on a
project app or `scope: production` on any app, therefore took production
traffic outside the policy. A plain CLI deploy could skip the approvals this
ADR exists to require. Such releases now wait like default-scope ones, and the
release-time canary check covers them (`state.IsProductionScope`). An app is
on its first release only when neither production scope has a live
deployment.

Environment promotions into `production` keep their own approvals. They are
not checked at release time and go live at full traffic, as before.

Services, workers and jobs have no canary. Their releases are not held, and a policy cannot be set on them.

## Consequences

- A release can sit at its first canary stage for up to the timeout. Billing is unchanged: the canary instance is billed like any canary, and the waiting stage holds only its stage's share of traffic.
- `RolloutConcurrencyGrant` ([ADR-199](199-rollouts-on-every-plan.md)) already covers a candidate beside the serving release, so waiting adds no capacity.
- Deploy pipelines that use `--rollout immediate` for apps with a policy start failing with `release_approval_canary_required`. The problem names the policy and how to remove it.
- `rolloutDecisionRules` gains:
  - `hold_awaiting_approval`;
  - `abort_approval_rejected`;
  - `abort_approval_timeout`.

## Rollout

Ship the tables, routes, CLI and enforcement together. The feature is off for every app until someone sets a policy. Tests cover:
- the refusal on every traffic path;
- self-approval and role checks;
- rejection and timeout aborts;
- release-time refusal of releases without a canary;
- the store conformance cases.

## Amendment 1 (2026-10-26): the paths without a canary

The Scope amendment above made the `production` environment's scope a production lane: releases deployed straight into it wait like default-scope ones (`state.IsProductionScope`). A release-tooling audit found that other paths still released into production without a canary to wait in, and so skipped the policy. This amendment covers them. It replaces the sentence "Environment promotions into `production` keep their own approvals …".
- **Promotions.** A promotion into an unprotected production environment releases every app at once, with no canary to wait in. When one of its apps requires approvals, it is refused with `409 release_approval_canary_required`; the hint says to protect the environment or remove the policy. Promotion approvals of a protected environment stand in for the app's policy. They are approved before the change is applied, which the original decision rejected for app releases, but a promotion has no running candidate to attach a decision to.
- **Project applies.** A project apply (`POST /v1/projects/scan`, `apply-plan` and `safe-deploy`, atomic or not) releases each app without a canary. It now refuses, per app, an app that requires approvals, like `--rollout immediate`.
- **Retries.** A retry of a release with no canary is refused while the app requires approvals, as a deploy would be. A retry keeps the failed release's canary.
- **GitOps activation.** Activating a graph in the production environment is held for a member app that requires approvals. The exception is a source with `approval_policy: manual`, where a person approved the revision in Gregale. A `protected_branch` revision was approved outside Gregale, and activation has no canary, so it waits until the policy is removed or the source switches to manual approval.
- **Metrics.** `faas_release_guard_decisions_total{guard="approval"}` counts holds, approvals, rejections, timeouts and canary-required refusals. `{guard="approval_notice"}` counts meterd's deliveries, and dropped notices alert (`FaasReleaseApprovalNoticeDropped`).

## Amendment 2 (2026-10-27): what a promotion approval and a rollback can stand in for

A security audit found two ways around an app's policy.

- **A protected environment's promotion approval is one decision.** Amendment 1 let it stand in for every promoted app's policy, but anyone who may promote could approve their own promotion, which an app with `allow_self_approval: false` (the default) forbids, and one approval cannot meet `approvals: 2`. The approval now records who gave it (`project_environment_approvals.approved_by_account_id`). A promotion into a protected production environment is refused for an app whose policy asks for more than one approval (`409 release_approval_canary_required`: release it with a canary), and, when the policy forbids self-approval, for an approval its own promoter gave or that predates the column (`403 release_approval_self`).
- **A rollback to a release that never took traffic is a forward release.** A rollback may target a release that is live at 0%. That covers a release demoted by `traffic promote`, but also one deployed with `--no-traffic` or parked for its schedule. Rolling back to such a release now meets the checks a traffic increase meets (`releaseApprovalTrafficProblem`): a scheduled release waits for its start, a release that waits for approval or was rejected is refused, and a parked release that needs a canary is refused. Plain and checked rollbacks, and automated route-monitor rollbacks, share the check. A superseded target served before and needs none.

## Amendment 3 (2026-10-28): decided releases, and releases without a known deployer

- **An approved release is decided.** apid accepted decisions after a
  release reached its approvals: a later reject aborted the approved release
  mid-rollout, and each later approval sent "approved" again. Decisions on an
  approved release are now refused (`409 release_approval_decided`); stopping
  it is an abort of its rollout.
- **No known deployer, one more approval.** A GitHub push records a GitHub
  login, not a Gregale account, so the self-approval rule could not recognise
  its author, who could approve their own push. When the policy forbids
  self-approval and the release has no recorded deployer, it needs one more
  approval than the policy asks, so at least one approver is someone else.
  The status shows the higher `required_approvals`.
