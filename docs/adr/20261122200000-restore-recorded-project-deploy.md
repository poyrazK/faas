# Restore a recorded project deploy

Status: Accepted

`gregale project rollback <slug> --targets` lists recent recorded production deploy IDs whose members have served and still belong to the project. Use `--dry-run --to-deploy <uuid>` to preview the exact member revisions. Services outside that deploy keep their current revisions. A member already serving the selected revision needs no cutover.

The preview checks availability and policy for each target. A member that never served, an unavailable artifact, or a changed serving revision can block admission. The project receipt pins exact source and target IDs and uses the existing preparation, cutover, and verification workflow. Pass `--yes --to-deploy <uuid> --plan-hash <hash>` to apply a reviewed preview. The target also participates in request ID replay.
