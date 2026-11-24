# Undo a paused project rollback

Status: Accepted

A paused project rollback may have switched several services. `gregale project rollback <slug> --undo <operation-id>` restores their exact pre-rollback deployments. Gregale prepares every undo target and restores them in reverse cutover order. Admission checks each completed child receipt and the serving period it established. A newer serving period or revision causes a conflict before the undo starts. The checked rollback path supplies its usual artifact, readiness, binding, and release gates.

The undo has its own receipt and preparation deadline. Admission links it to the paused rollback in the same transaction that reserves the project's running operation. A repeat with the same options returns that receipt, and the source stays linked to it. Use the undo operation ID with `--status` or `--resume`. An undo crosses release commands only with its own explicit acknowledgement.

`--preview-undo <operation-id>` shows the inverse cutovers in execution order, the release runs they cross, and any blockers. A runnable preview includes a SHA-256 hash covering the source operation, the ordered deployment pairs, the current production serving periods, and the release runs and acknowledgement. Pass it to `--undo <operation-id> --plan-hash <hash>` with the same acknowledgement.

Reviewed admission rechecks the serving periods and exact release runs under store locks before reserving and linking the undo receipt. A changed plan returns `project_rollback_plan_changed` and requires a fresh preview. Retries with the accepted hash and acknowledgement return the saved receipt.

Each forward cutover holds the original revision's immutable root filesystem, sidecar layers, and their signatures in the same transaction that switches traffic. The store uses the garbage collector's artifact locks. If it cannot acquire the hold, the rollback pauses before switching that service.

Holds persist while a rollback runs. A settled pause starts a 24-hour window. Status and undo preview show the deadline for each held revision. Resuming the original reactivates its unexpired holds, and admitting an undo transfers those holds to its receipt. Completing an operation releases its holds. Serving-period, artifact, and release preflight checks still apply.
