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
and watches deployable files for the next change.

The first sync uploads a complete source snapshot. Later edits transfer only
new, changed, and deleted archive entries when that is smaller than the full
snapshot. The server reconstructs and validates the complete source before
building, so incremental transfer does not create a second build path. Its
source cache is disposable: after a restart, eviction, or cross-host request,
the CLI automatically resends the complete snapshot. A new `gregale dev`
invocation also starts safely with a complete sync.

Railpack developer builds keep their Firecracker VM isolation but reuse a
tenant- and workspace-scoped BuildKit dependency cache between syncs. When the
runtime and lockfiles are unchanged, matching install layers are restored; a
dependency or runtime change is validated by BuildKit and rebuilds only the
invalidated layers. The CLI prints whether the dependency cache was restored
and the total time until the new version is live. Dockerfile builds remain on
the cold path for now.

```sh
gregale dev --once             # sync once, do not watch
gregale dev --path apps/api    # select one workspace application
gregale dev --name payments    # choose the stable project identity
gregale dev --stop             # tear down the project's environment
```

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

Developer environments currently consume the same deployed-app quota as pull
request previews. Separate development quotas remain a follow-up improvement.
