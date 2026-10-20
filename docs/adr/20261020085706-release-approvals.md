# ADR-20261020085706 · Release approvals

- **Status:** proposed
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
  - **Notifying approvers in Slack in this change.** Notification channels carry alert events only. Slack messages with approve and reject buttons are a separate change. Here the release reports that it is waiting in `gregale deploy`'s live view, in `gregale approvals pending` and in the rollout decision.

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
- it is in the app's default (production) scope;
- it has never completed a rollout and is not the app's serving release.

A rollback restores a release that already served, so rollbacks never wait. An app's first release has nothing to compare against and does not wait.

### At release time

After the ADR-1446 default fills in the rollout, a production release of an app with a policy and a live deployment must have a canary. Otherwise it is refused with `409 release_approval_canary_required`. That covers:
- `--rollout immediate`;
- an explicit `--traffic-percent` above 0;
- a canary worker that is unavailable.

The check runs on the same paths as freezes: JSON and image deploys, multipart source deploys, tarball deploys, upload sessions, git-ref deploys and GitHub pushes. `--no-traffic` (a dark deployment) is allowed, because it takes no traffic.

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

### CLI

| Command | Does |
|---|---|
| `gregale approvals require <app> --approvals N [--after-stage N] [--timeout 24h] [--approvers admin\|developer] [--allow-self-approval]` | Sets the policy. |
| `gregale approvals policy <app>`, `gregale approvals off <app>` | Shows or removes the policy. |
| `gregale approvals pending <app>` | Lists the app's releases that are waiting. |
| `gregale approvals status <deployment>` | Shows one release's status and decisions. |
| `gregale approvals approve <deployment> [--comment]`, `gregale approvals reject <deployment> --reason` | Decides on a release. |

`gregale deploy`'s live view shows when a release is waiting and prints the approve command.

### Scope

Named environment scopes and project environments are not covered. Project environments keep their own promotion approvals.

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
