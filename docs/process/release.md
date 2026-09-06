# Production release process

Gregale production deployments use a signed release tag and a protected
GitHub Environment. A merge to `main` is not itself a production deploy.

## One-time repository configuration

Repository administrators must configure the `production` environment in
GitHub Settings → Environments:

- add at least one required reviewer from the platform-on-call group;
- do not enable administrator bypass for required reviewers;
- keep deployment branches restricted to the repository's release path.

The workflow declares `environment: production`, but required reviewers are a
repository setting and cannot be defined in YAML. `.github/CODEOWNERS` keeps
the workflow and deployment surfaces under explicit review as well.

## Deploying a release

1. Merge the release commit to `main` and wait for the `ci` workflow to pass.
2. Create and push the pre-1.0 tag (for example `v0.1.18-rc.1`).
3. Wait for `release.yml` to publish the signed canonical artifacts.
4. Dispatch `cd-controlplane` with that exact `release_tag`:

   ```bash
   gh workflow run cd-controlplane.yml --ref main \
     -f release_tag=v0.1.18-rc.1
   ```

Before the deploy job is eligible for approval, its CI preflight verifies
that the tag resolves to the same commit as a successful, push-triggered
`ci.yml` run on `main`. A missing, running, or failed CI run blocks the
deployment.

The deploy job then pauses at the `production` environment for reviewer
approval. Approval is required for both normal dispatches and reruns. The
workflow downloads and verifies the signed release bundle before changing the
host.

## Queue and rollback behavior

All control-plane dispatches use the `cd-controlplane-production` concurrency
group with `cancel-in-progress: false`. A later release waits for the earlier
deployment to finish; it cannot cancel a deployment while migrations or
service restarts are in progress.

If a release fails after approval, investigate the deployment logs and use the
operator rollback procedure in
[`docs/ops/release-readiness-checklist.md`](../ops/release-readiness-checklist.md).
Do not bypass the environment gate by running deployment commands manually.
