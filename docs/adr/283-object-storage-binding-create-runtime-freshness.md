# ADR-283 · Runtime freshness for object-storage binding creation

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** When creating a managed S3 compute binding, write the app runtime-config change stamp and mark its existing snapshots stale in the same transaction as the credential and six app secrets. The memory store applies those changes under one lock. After commit, the API sends one identity-only secret-change notification to invalidate runtime configuration caches. This applies to direct creation and project environment cloning.
- **Why:** Binding creation previously added secrets without the runtime freshness steps used by ordinary secret writes. A later wake could restore a snapshot taken before the binding existed, leaving the workload without its six S3 settings. Stamping after commit would also leave a failure window that a duplicate-create retry could not repair.
- **Consequences:** An existing snapshot cannot be restored after binding creation; the next wake cold-boots and stages the new secrets. A running workload is not forcibly restarted by creation. Notification remains best-effort, like ordinary secret writes, because the committed database rows are authoritative.
- **Rejected alternatives:** Post-commit snapshot invalidation leaves a committed binding with stale snapshots if invalidation fails. Restamping on a retry can move the freshness boundary past a new instance. Automatically restarting every live app on binding creation would change the existing secret-write delivery contract.
