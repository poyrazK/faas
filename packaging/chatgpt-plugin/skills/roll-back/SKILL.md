---
name: roll-back
description: Roll a Gregale app back to an earlier release, or undo changes this agent made, after showing the user exactly what will change. Use when the user asks to roll back, revert or undo a Gregale deployment or change.
---

# Roll back a Gregale app

A rollback changes the release serving production traffic. Always show the
user the plan and wait for their confirmation before applying it.

## 1. Choose the release

Call `gregale_list_app_deployments` for the app. Pick the deployment to
restore: usually the last one that served traffic before the broken one.
When it is not obvious, show the user the candidates with their times and
ask.

## 2. Review the plan

Call `gregale_plan_rollback` with the app, the `target_deployment_id` and a
short `reason`. Tell the user which release serves now and which will serve
after the rollback, and ask them to confirm.

## 3. Apply and follow it

1. After the user confirms, call `gregale_apply_rollback` with the plan's
   `plan_id`. After an unclear error, retry with the same `plan_id`.
2. When a tool returns `approval_required`, tell the user a person must
   approve it in Gregale, call `gregale_wait_for_approval` with the
   `approval_id`, and follow its recovery.
3. Follow progress with `gregale_get_rollback` until it finishes or reports
   a blocker, then check the app with `gregale_get_health`.

If the rollback is blocked because the serving release changed since the
plan, review a new plan; do not force it.

## Undo this agent's own changes

To undo changes this agent made, call
`gregale_plan_undo`, show the user what each undo will do, then call
`gregale_apply_undo` with the `plan_digest`. A person must approve every
undo in Gregale.
