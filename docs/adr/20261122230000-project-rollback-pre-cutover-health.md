# Verify prepared project rollback targets before cutover

`gregale project rollback <slug> --yes --verify --verify-before-cutover` keeps the project cutover barrier closed until all prepared targets with configured HTTP probes pass. The same options with `--dry-run` show which services will be probed and include the policy in the reviewed plan hash.

After every target is ready, the coordinator uses private deployment-scoped GET or HEAD probes against the pinned revisions at zero production traffic. Each result is saved with the checked rollback attempt. An attributed non-2xx response blocks the operation and releases unused preparations. Unattributed responses are retried within the preparation deadline. Resuming creates new attempts and checks their prepared revisions again.

The cutover transaction checks the saved results for the entire batch, and a database trigger requires that authorization before increasing any opted-in target's traffic. This also fences workers that predate the pre-cutover policy. Post-cutover verification still checks the restored route and gates the next service.
