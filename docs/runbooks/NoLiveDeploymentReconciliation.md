# Apps without a live deployment

Use this runbook when an app looks active but wake, invoke, or an app-backed
workflow reports `no_live_deployment`. This is a read-only audit first; an old
revision is not safe to restore merely because it was once successful.

## Customer-facing checks

The app list and detail API report `deployment_availability` independently of
the app lifecycle `status`. `gregale apps` displays `NO LIVE DEPLOYMENT`, and
`gregale inspect <slug>` explains that the app cannot serve requests and
recommends redeploying from current source. JSON clients can check:

```sh
gregale apps --json | jq 'select(.deployment_availability == "no_live_deployment") | {slug, status, deployment_availability}'
gregale inspect <slug> --json
```

The app may still have `status: "active"`; do not use that lifecycle field as
proof that a deployment is runnable.

## Read-only account audit

Run against the primary database using a role that has `SELECT` only. The
query identifies active apps with no live deployment and provides an initial
history/artifact-metadata classification. `candidate_metadata_present` is
only a lead for verification: database metadata cannot prove the object,
signature, or boot readiness is still valid.

```sql
BEGIN READ ONLY;

WITH live AS (
    SELECT DISTINCT d.app_id
    FROM deployments d
    JOIN apps a ON a.id = d.app_id
    WHERE a.status <> 'deleted'
      AND d.status = 'live'
), successful AS (
    SELECT DISTINCT d.app_id
    FROM deployments d
    WHERE d.deleted_at IS NULL
      AND d.status = 'superseded'
), candidate AS (
    SELECT DISTINCT ON (d.app_id)
           d.app_id, d.id, d.revision, d.rootfs_key, d.rootfs_bytes
    FROM deployments d
    WHERE d.deleted_at IS NULL
      AND d.status = 'superseded'
      AND NULLIF(d.rootfs_key, '') IS NOT NULL
      AND COALESCE(d.rootfs_bytes, 0) > 0
    ORDER BY d.app_id, d.created_at DESC, d.id DESC
)
SELECT a.id AS app_id,
       a.slug,
       CASE
         WHEN successful.app_id IS NULL THEN 'no_previous_success'
         WHEN candidate.id IS NULL THEN 'candidate_artifact_metadata_missing'
         ELSE 'candidate_metadata_present'
       END AS audit_class,
       candidate.id AS candidate_deployment_id,
       candidate.revision AS candidate_revision,
       candidate.rootfs_key,
       candidate.rootfs_bytes
FROM apps a
LEFT JOIN live ON live.app_id = a.id
LEFT JOIN successful ON successful.app_id = a.id
LEFT JOIN candidate ON candidate.app_id = a.id
WHERE a.status = 'active'
  AND live.app_id IS NULL
ORDER BY a.slug;

COMMIT;
```

Interpret the output conservatively:

- `no_previous_success`: there is no prior live or superseded deployment; ask
  the customer to deploy current source.
- `candidate_artifact_metadata_missing`: do not attempt restoration. Ask the
  customer to deploy current source.
- `candidate_metadata_present`: inspect the deployment and rollout history
  with the customer. This is not yet a recovery classification; the rollback
  path must validate the retained rootfs/signature and pass readiness before
  the revision can serve.

## Recovery and verification

For a confirmed incident victim only, obtain customer/operator approval for
the exact revision. Prefer a redeploy from current source when available. If
restoring a prior revision is necessary, use the normal explicit rollback
path (`gregale rollback <slug> --to <deployment-id>`). The API checks retained
artifact/signature availability, then queues the readiness-gated rollback;
the scheduler verifies the full artifact signature before boot. Never update
deployment status directly in SQL, and do not bulk-restore every app returned
by the audit.

After recovery, verify the app has a live deployment and that its normal wake
path succeeds. For workflow-backed apps, run a representative workflow and
confirm it completes. Record the app, chosen revision, artifact/readiness
result, and verification time in the incident record.

## State-damaging release backfill

After shipping a code fix for a release that already changed persisted state:

1. Run the read-only audit and preserve its output before acting.
2. Classify each affected app; separate confirmed incident victims from
   historical/test-only rows.
3. Recover only individually confirmed apps using the guarded workflow above.
4. Verify wake and one customer-level operation for each recovered app.
5. Record unresolved/missing-artifact apps for customer redeploy or support;
   do not close the incident solely because the forward code path is fixed.

Related: [issue #3061](https://github.com/poyrazK/faas/issues/3061),
[issue #2705](https://github.com/poyrazK/faas/issues/2705).
