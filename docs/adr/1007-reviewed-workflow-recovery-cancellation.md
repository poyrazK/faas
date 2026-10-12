# 940: Reviewed cancellation from recovery health

An execution attention alert identifies an admitted generation, but the ordinary workflow cancellation endpoint does not fence that generation. Operators need a preview and an explicit action that cannot follow a newer resume.

Native recovery jobs expose `workflow-cancellation-preview` and `workflow-cancel` POST endpoints. Requests require the frozen item position, admitted resume count and a nonblank operator reason. Authentication supplies the actor. Preview needs read scope; cancellation needs deployment write scope. Both follow existing MFA requirements.

The store locks the recovery job and run, checks account/app/workflow scope, checks the frozen expected count plus one, and verifies admitted execution evidence. Pending or skipped items, changed generations, missing evidence and operation-owned workflows cannot be cancelled. Terminal generations are reported without mutation. Preview is advisory: cancellation repeats the checks under the mutation lock. The action never cancels other pending job items or selects another run.

Successful cancellation and a `workflow_cancelled` recovery history entry commit together. The history identifies the target position, run, resume count, actor and reason. The memory store applies the same operation under its mutex. Repeating cancellation of the same cancelled generation returns `already_cancelled` without another audit entry. A newer generation is refused even after an earlier successful cancellation.

`gregale automations recovery-health --app SLUG` displays attention observations, run and diagnostics links, preview commands, the saved-failure retry-plan command and audit history command. `recovery-cancel-preview` requires an explicit position and resume count. `recovery-cancel` additionally requires `--yes`. Changed and ineligible outcomes return CLI exit code 5. Retry selection remains the existing reviewed saved-result workflow.

Cancellation is cooperative: it closes active workflow steps and attempts using existing cancellation behavior and fences subsequent scheduler writes. It cannot undo external effects or guarantee that an already executing remote handler stopped. The reason belongs to audit history; the existing operator-cancellation execution marker is preserved.

The migration extends retained recovery history with the target identity. Rollback refuses to discard retained workflow cancellation history.
