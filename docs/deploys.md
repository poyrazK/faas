# Deploys

The normal deploy path is source-first and wait-by-default:

```bash
gregale deploy
gregale deploy --path services/api
gregale deploy --tarball build/source.tar.gz
gregale deploy --image ghcr.io/acme/api@sha256:...
```

Use `--no-wait` when a CI job only needs the queued deployment ID. Use
`--timeout 900` to bound a wait, and `--idempotency-key KEY` when a retry must
represent the same logical deploy. `--reason`, `--tag`, and `--deployed-by`
annotate deployment history.

Every successful wait ends with readiness plus a platform-side smoke request;
a queued build is not reported as live. The final output includes the app URL,
a release summary against the previous deployment, and a copy-paste rollback
command when a previous release is available:

```
✓ Deployed. https://my-app.gregale.dev
Release summary:
  Changes since d0:
    commit_sha         "old" -> "new"
  Rollback: gregale rollback my-app --to d0
```

With `--json`, the receipt keeps the existing deployment/provenance fields and
adds `release_summary` with `previous_deployment_id`, `changes`,
`rollback_target_id`, and `rollback_command`. A queued `--no-wait` deploy has
no release summary yet because the new release is not live.

## GitHub Actions

Print a workflow starter with:

```bash
gregale deploy --github
```

The checked-in action can then be pinned to a release. Connect a repository
with `gregale connect` when pushes should deploy automatically.

## Safe changes

Preview a change with `gregale deploy --diff` or `--dry-run`. For a bad live
release, use `gregale rollback APP`; rollback reuses the previous live
artifact instead of rebuilding it. See [deployment history](deployments.md)
for annotations and receipts.

## Revisions

Every deploy creates an immutable revision, numbered per app and shown as
`v41`, `v42`, `v43`. A revision is never rewritten — a redeploy supersedes it
and creates the next one — so it is a stable handle you can keep in a runbook
or an incident channel.

Use a revision anywhere a deployment ID is accepted:

```bash
gregale rollback my-api --to v41
gregale traffic set --app my-api --deployment v42 --percent 10
```

`gregale traffic status my-api` lists the live revisions and their weights.
Revision numbers are per app, so `v41` of one app is unrelated to `v41` of
another. Numbers may skip values: preview deploys and retries consume them
too.

## Dark deployments

Stage a revision, wait until it is ready, and keep production traffic on the
current release:

```bash
gregale deploy --no-traffic
```

The command prints the new revision's preview URL and a copy-paste promotion
command after readiness succeeds. For example:

```
✓ Staged v44 with 0% production traffic.
  Preview: https://deploy-44-my-api.gregale.dev
  Production traffic remains unchanged. https://my-api.gregale.dev
  Promote: gregale traffic promote --app my-api --deployment v44
```

`--no-traffic` is the discoverable spelling for `--traffic-percent 0`. It is
mutually exclusive with `--traffic-percent`, `--safe`, and canary flags. With
`--json`, a waited dark deployment adds `preview_url` (when the platform's
preview zone is enabled) and `promotion_command` to the deployment receipt.
`--no-wait` returns once the dark deployment is queued, before a preview URL
is guaranteed to be live.

## Progressive rollouts

Instead of moving all traffic at once, shift it in stages and let the platform
promote only while the app stays healthy:

```bash
gregale deploy --canary-preset balanced
```

`balanced` sends 1% of traffic to the new revision for 2 minutes, then 10%,
then 50%, then 100%. `slow` and `aggressive` trade speed against exposure, and
`--canary-stages "5@1m,25@5m,100@0s"` defines your own ladder. Preview how a
preset would behave against your app's recent traffic before deploying:

```bash
gregale canary simulate my-api --canary-preset balanced
```

Promotion is health-gated. If an alert rule with a `rollback` or `demote`
action is firing, the ladder holds at its current stage rather than advancing.
`gregale deploy --safe` combines the balanced ladder with automatic rollback
when the new revision returns 5xx responses in its first window. If a rollout
wedges, `gregale rollouts recover my-api` is the manual escape hatch.

Traffic splitting and canary rollouts are available on every plan. During a
rollout an app runs one instance above its plan's concurrency limit so both
revisions can serve at once; that extra instance lasts only as long as the
rollout and is still subject to available capacity. If a node has no headroom,
the rollout holds at its current stage instead of promoting.
