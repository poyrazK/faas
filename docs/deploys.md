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
