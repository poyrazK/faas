# Deploying from the CLI

`gregale deploy` ships a directory, tarball, OCI image, or pinned
GitHub ref to an app on the control plane. The path that runs is
inferred from flags + the cwd's git state; this page captures the
non-obvious pieces (which files actually get shipped and what the
`--json` envelope looks like).

## Source-root semantics

When cwd is inside a git repository with an `origin` remote, `gregale
deploy` without a source selector still ships the
**committed tree at HEAD from the repo root**, preserving the original
zero-config behavior. A nested working directory does not implicitly
change the build root.

Use `--path` when one service in a monorepo should be deployed as its
own app:

```bash
cd monorepo
gregale deploy --path packages/api --name api
```

`--path` is resolved relative to the current directory. For a
self-contained directory, it archives `HEAD:<path>` and makes the
selected directory the uploaded archive root, so its `package.json`,
`go.mod`, or `Dockerfile` is detected normally. When the selected
directory is a deployable member of a recognized workspace manifest
(`package.json`, `pnpm-workspace.yaml`, `go.work`, and the other
`reposcan` workspace forms), Gregale uploads the repository tree as the
BuildKit context and records `source_root=<path>`. The builder runs in
that nested directory while retaining workspace lockfiles, shared
packages, and root-level build configuration.

Workspace context uploads still use the normal source exclusions,
secret scan, and source-size cap. `source_root` is validated against the
archive before the deployment is queued. It never requires removing
`.git`. The default remains reproducible and excludes uncommitted
changes.

Use `--worktree` to explicitly deploy local files from the selected
directory, including uncommitted and untracked files:

```bash
gregale deploy --path packages/api --worktree --name api
```

`--worktree` without `--path` applies the same behavior to the current
directory. Both modes keep the repository's `commit_sha` in the JSON
receipt when Git metadata is available; `dirty: true` indicates that
the repository had local changes at deploy time.

For a decomposed monorepo deploy (one CLI invocation, N apps), use
`gregale scan --path .` and the project-plan apply path; see the
decomposition PR (issue #791 / ADR-090). A direct `--path` deploy is
still one app per invocation, with the selected workspace member as its
working directory.

## Monorepo / nested-project detection

When the cwd contains **monorepo workspace markers** in a nested
subdir (e.g. `apps/web/package.json`, `apps/services/api/package.json`)
and there is no explicit `--path` selection, the CLI prints a hint and
exits with a "no deployable source here" error rather than guessing:

```
note: detected nested project marker(s) at apps/web/services/api/package.json
hint: this looks like a monorepo subdir; run `gregale scan --path .` to
      decompose into per-app plans and deploy each one.
```

The detection walks depth 2 from the cwd (so `apps/web/package.json`
and `apps/services/api/package.json` both trigger it). An explicit
`--path` is the operator intent that enables the workspace-context
behavior above. Depth 4+ remains intentionally out of scope for the
cwd hint — a `pkg/billing/internal/lib/` marker is too deep for the CLI
to act on without explicit operator intent. See
`detectNestedMarkerHint` at `cmd/gregale/pack.go:691`.

## `--json` receipt shape

`gregale deploy --json` emits a single JSON document with the
`DeploymentResponse` shape promoted to top-level (via embedding)
plus four provenance-only fields:

| Field            | Type   | Source                                                          |
|------------------|--------|-----------------------------------------------------------------|
| `id`             | string | `api.DeploymentResponse.ID` (server-issued)                      |
| `app_id`         | string | `api.DeploymentResponse.AppID`                                  |
| `status`         | string | `api.DeploymentResponse.Status` — `"pending"` at deploy time    |
| (all other `DeploymentResponse` fields) | — | see [`pkg/api`](../../pkg/api)                                 |
| `app_url`        | string | `deployedAppURL(slug)` — `https://<slug>.<FAAS_APPS_DOMAIN>` (default `gregale.dev`). Slug comes from `--name` / cwd-derived name (CLI input), NOT from `app_id` on the response — the wire's `app_id` is the 32-char hex primary key and the gateway routes on slug. |
| `commit_sha`     | string | `git rev-parse HEAD^{commit}` from the zero-config branch; empty on image / source-ref / non-git fallback paths |
| `dirty`          | bool   | `git status --porcelain` is non-empty; omitempty so a clean repo renders no key |
| `source_sha256`  | string | lower-case hex sha256 of the tarball bytes just shipped; empty on image and source-ref (server pulls) paths |

The receipt is consumed by CI / GitHub Actions tooling that needs to
pin a deploy to a specific upstream artifact. Parse with any JSON
decoder that accepts extra top-level keys: existing SDK clients that
unmarshal into `api.DeploymentResponse` keep working — the extra
fields are silently dropped.

For the source-ref CI path, commit pinning is captured server-side
rather than in the receipt (the CLI never sees the tarball bytes).
See [`docs/source-ref.md`](../source-ref.md) for the
`--repo OWNER/NAME --ref $SHA` shape and the install-token trust
boundary.

## GitHub release tags

After an app is connected to GitHub, a push to the configured production
branch still deploys as before. A newly-created SemVer release tag also
deploys through GitHub's normal `push` webhook: use the conventional
`vMAJOR.MINOR.PATCH` shape (for example `v1.4.0` or `v1.4.0-rc.1`). githubd
uses the tag's immutable `after` SHA and applies the repository's production
binding.

Release tags are a one-way promotion boundary. Moving or force-updating an
existing tag is ignored, so a release cannot silently change underneath a
customer's deployment history; publish a new version instead. Tags that do
not satisfy SemVer, tag deletion webhooks, and malformed tag deliveries are
also ignored before source fetch or build enqueue. The repository default
branch is the initial lookup key, with the project's configured production
branch as the authoritative fallback.

## Reproducibility note

Three flavors of "what was deployed", pinned differently:

- **Zero-config (`gregale deploy` from a git repo)**: pinned at
  `commit_sha` (HEAD) + the committed tree of HEAD. Without `--path`,
  that tree is the repository root. With `--path`, a self-contained
  source uses the selected `HEAD:<path>` tree; a recognized workspace
  member uses the repository HEAD tree plus its `source_root`. Re-runs
  of the same SHA are byte-identical assuming the selected tree/context
  is unchanged.
- **Working-tree zero-config (`--worktree`)**: pinned by the shipped
  `source_sha256`; `commit_sha` and `dirty` remain useful provenance,
  but local edits and untracked files are intentionally included.
- **Tarball (`gregale deploy --tarball foo.tar.gz`)**: pinned at
  `source_sha256` (the bytes shipped). Re-runs of the same SHA
  are byte-identical. No `commit_sha` because no git detection ran.
- **Image (`gregale deploy --image registry.x/app@sha256:...`)**:
  pinned at the OCI digest in `--image`. `dep.ImageDigest` on the
  response carries the same value.
- **Source-ref (`gregale deploy --repo OWNER/NAME --ref SHA`)**:
  pinned at the GitHub ref. SHA-pinned refs (`--ref
  $(git rev-parse HEAD)`) are byte-identical upstream; branch refs
  are not. See [`docs/source-ref.md`](../source-ref.md).
