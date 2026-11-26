# Pause a running project rollback

Status: Accepted

`gregale project rollback <slug> --pause <operation-id>` pauses a running rollback or undo. The request records a timestamp, closes the cutover gate, and fences any prior worker claim in the same receipt transaction. A cutover that committed first remains recorded. Future cutovers wait until an operator resumes the operation.

During cleanup, Gregale settles committed handoffs and releases unused preparations. The receipt becomes blocked with the pause timestamp once cleanup finishes. Repeating a pause returns the current receipt. Resume clears the pause request and prepares any remaining targets using the saved serving pins. A paused original rollback with completed cutovers can also be undone using its saved receipt.

`--preview-resume <operation-id>` reads the paused receipt, checks its completed serving periods and pending cutovers, and shows which verification will run. The preview leaves the saved preparation attempts and release acknowledgement intact.

A runnable preview returns a hash bound to the paused receipt version, the exact serving periods, and the checked release runs. Pass it to `--resume <operation-id> --plan-hash <hash>` to continue the same receipt. Admission rechecks those pins before committing the new attempts. A stale review returns `project_rollback_plan_changed` and leaves the operation paused. The receipt saves its accepted resume hash so a request retry follows the accepted attempt.
