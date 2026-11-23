# Apply a reviewed project rollback plan

Status: Accepted

A runnable rollback preview returns a SHA-256 plan hash. Pass it to `gregale project rollback <slug> --yes --plan-hash <hash>` with the same rollback options to require that plan at admission. The hash identifies the project, selected deploy group and options, the ordered service revision pairs and serving periods, the release runs reviewed for each service, and the configured verification probes.

Admission recomputes the plan with write authorization. If it differs from the reviewed plan, the request returns `project_rollback_plan_changed` before saving an operation. The worker still checks the saved serving pins before preparing or switching traffic.

The accepted receipt includes the hash. A retry with the same request ID and options returns that receipt even if a fresh preview would differ. Older workers can update a receipt during a rolling upgrade, so the store preserves its original hash. The expected hash is optional on start.
