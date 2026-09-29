# Apps

An app is a named HTTP API with a current deployment, runtime profile, and
optional domains, secrets, environment variables, and triggers.

```bash
gregale apps
gregale app my-api
gregale app my-api scale --min 1
gregale open my-api
gregale apps --q my-api
```

`gregale deploy` creates the app on first use and updates it on later runs.
Use `gregale app my-api --json` in automation; API errors include a stable
code, the observed value, the limit, and a next action.

Apps are isolated from one another. A parked app consumes no resident memory;
its next request wakes a snapshot or cold-boots from its immutable artifact.
See [scale-to-zero](cold-wake.md) and [plans](plans.md) for the limits that
control this lifecycle.

## Settings in a project environment

Read and edit the desired workload configuration of a registered environment:

```bash
gregale app my-api --environment staging
gregale app my-api --environment staging --ram 512
gregale app my-api scale --environment staging --request-timeout 17
```

Deploy to that environment to test the saved configuration. A deployment pins
its settings revision: subsequent desired settings edits take effect on the next
deployment. Changes in staging leave the production app settings unchanged.
The CLI uses the observed revision to reject concurrent edits instead of
silently replacing a newer nested scaling policy. Reload and reapply an edit
when the API returns a revision conflict.

Environment cloning materializes workload settings into an independent revision.
Protected environments require an approved project plan or promotion for edits.
The full clone workflow is still being implemented: this settings support alone
does not copy active deployments or every resource, and existing release-only
promotion does not yet carry these settings to production.
