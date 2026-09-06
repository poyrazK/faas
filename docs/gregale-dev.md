# `gregale dev`

`gregale dev` is a remote development loop backed by the same build,
deployment, routing, and Firecracker infrastructure as production and pull
request previews. It does not require Firecracker or KVM on the developer's
computer.

From an application directory:

```sh
gregale dev
```

The CLI detects the application shape, uploads the current working tree
(including uncommitted changes), waits for the real build, prints a stable URL,
and watches deployable files for the next change. Watching continues while a
build is running. If another settled edit arrives, Gregale cancels the obsolete
deployment and builds the newest source instead of letting old saves queue up.

The first sync uploads a complete source snapshot. Later edits transfer only
new, changed, and deleted archive entries when that is smaller than the full
snapshot. The server reconstructs and validates the complete source before
building, so incremental transfer does not create a second build path. Its
source cache is disposable: after a restart, eviction, or cross-host request,
the CLI automatically resends the complete snapshot. A new `gregale dev`
invocation also starts safely with a complete sync.

Railpack and Dockerfile developer builds keep their Firecracker VM isolation
but reuse a tenant- and workspace-scoped BuildKit dependency cache between
syncs. When the runtime, Dockerfile, and lockfiles are unchanged, matching
layers are restored; a dependency, build-input, or runtime change is validated
by BuildKit and rebuilds only the invalidated layers. Cache import, export, and
validation failures fall back to a cold build. The CLI prints whether the
dependency cache was restored and the total time until the new version is
live.

```sh
gregale dev --once             # sync once, do not watch
gregale dev --path apps/api    # select one workspace application
gregale dev --name payments    # choose the stable project identity
gregale dev --stop             # tear down the project's environment
gregale dev status             # show developer-environment quota usage
gregale dev --no-logs          # keep the watcher quiet for scripts
gregale dev --env-file .env.dev # opt in to syncing local config as secrets
```

`--env-file` is explicit and additive/update-only. Each non-empty, non-comment
`KEY=VALUE` entry is written to the developer app's sealed default secret
scope; omitted keys are left untouched. The CLI compares and reports key names
only, never prints values, and refreshes the secrets before the first deploy.
Changes to the file trigger the same debounced redeploy as source edits. The
file itself is excluded from the source archive, including when it lives inside
the watched directory. Use `gregale secrets unset --app <slug> KEY` when a key
must be removed intentionally.

Watch mode attaches one app-level runtime log stream after the first live sync.
It follows the stable developer URL across later redeploys, prefixes lines with
their stream (`runtime stdout` or `runtime stderr`), and reconnects after a
transient API or scheduler interruption. `--once` remains finite and does not
attach the stream.

The URL is stable for an account, local developer installation, and source
directory. Teammates and separate clones or worktrees therefore get independent
environments, while repeated runs from the same source directory resume the
same one. The non-secret local identity lives in the Gregale config directory;
`FAAS_DEVELOPER_ID` can override it with 32 lowercase hexadecimal characters
for reproducible automation.

Each sync renews a 24-hour lease; the existing preview janitor tears down an
expired environment. Stopping the watcher with Ctrl-C leaves the environment
available—use `--stop` from the same source directory when it should be removed
immediately.

Developer environments have a separate per-plan quota from production apps and
pull-request previews. They are still backed by the same preview lifecycle and
24-hour lease; `gregale dev status` reports the account-wide budget so a local
workspace cannot unexpectedly block a deploy or PR preview.
