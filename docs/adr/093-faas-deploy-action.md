# ADR-093 · `faas-deploy-action` customer-facing GitHub Action (issue #270)

- **Status:** accepted
- **Amended:** 2026-09-12 (public beta uses the maintained `v0` Action ref)
- **Date:** 2026-08-11
- **Issue:** #270 (`SDK: Publish an official GitHub deploy action`)
- **Labels:** `tier-2-public-launch`, `customer-facing`, `dx`
- **Supersedes:** none (no first-party Action was published before this slice)
- **Related:** ADR-092 (headless source-ref deploy — the wire contract the
  Action wraps), ADR-012 (githubd install-token model — the server-side
  plumbing the Action depends on), ADR-020 (sealed env-var secrets;
  `GREGALE_API_KEY` is the bearer token the Action forwards), ADR-014
  (push-webhook bind flow — complementary, not replaced), spec §14
  milestone M5 (DEPLOY-PROV), docs/source-ref.md (`## GitHub Actions`
  section is the customer-facing entry point).

## Context

PR-A (#828) + PR-B (#832) + PR-D (#838) shipped the **headless**
source-ref deploy path end-to-end: `gregale deploy --repo OWNER/NAME
--ref <sha>` posts to `POST /v1/apps/{slug}/deployments/source-ref`,
and the webhook push-to-deploy loop is wired with per-tenant secrets
and the Checks API writer. But the explicit-CI shape — a team that
wants a workflow run on every push, not a push listener — has no
first-party story. Customers hand-roll a `gregale deploy --repo`
step in their `.github/workflows/`, with all the boilerplate that
implies: unpinned CLI install via `curl | bash`, no token-redacted
error annotations, no fixture-tested pattern, no per-step outputs.

Issue #270 ships the customer-facing Action. The original plan
called for a separate `poyrazK/faas-deploy-action` repo (separate
PR); the user selected the **monorepo shape** so PR-1 (snippet
generator + ADR + docs) and PR-2 (Action repo) collapse into one
big PR.

## Three alternatives

| Axis | Option 1 (monorepo composite) | Option 2 (separate repo) | Option 3 (JavaScript Action) |
|------|--------------------------------|--------------------------|------------------------------|
| Single PR? | **Yes.** The action lives at `.github/actions/deploy` inside `poyrazK/faas`. | No. Two PRs (one per repo) plus a release-coordination step. | Yes. |
| Runtime dependency | None. Vendored binary committed to the repo per release. | None. | Node.js runtime in the Actions runner. |
| Determinism | Best. `cli-version` is the literal vendored binary. | Best. | Mixed. |
| Repo size | Heavier: ~15 MB binary in `.github/actions/deploy/bin/`. `git clone --depth 1` keeps day-to-day work fast. | Tighter: ~15 MB in the action repo only. | ~200 KB. |
| Customer `uses:` URL | `poyrazK/faas/.github/actions/deploy@v0` during public beta | `poyrazK/faas-deploy-action@v1` | `poyrazK/faas-deploy-action@v1` |
| Cross-repo coordination | None. | Required (issue #270 plan R8). | None. |
| Vendor lock-in | Customer pinned to `poyrazK/faas` repo path. | Customer pinned to a separate repo; cleaner namespace. | Customer pinned to a separate repo. |
| Release pipeline | New `.github/workflows/release.yml` (no precedent in this repo) — cross-builds at tag, vendors binary, updates `src/version.txt`, commits to `release/v<tag>` branch, attaches to GitHub Release. | New release workflow in the action repo. | New npm publish workflow. |
| Spec compliance | Clean. Reuses poyrazK/faas LDFLAGS (`-X github.com/onebox-faas/faas/pkg/wire.Version=...`). | Same. | Reimplements the wire shape in TypeScript. |

## Decision

Choose **option 1 (monorepo composite)**.

The action lives at `poyrazK/faas/.github/actions/deploy`. Layout:

```
.github/
├── actions/
│   └── deploy/                  # the customer-facing composite Action
│       ├── action.yml
│       ├── src/
│       │   ├── run.sh           # bash wrapper: set -euo pipefail, exports $GITHUB_OUTPUT
│       │   ├── annotate.sh      # RFC 7807 → ::error, redacts tokens
│       │   └── version.txt      # bundled CLI version (written by release.yml)
│       ├── bin/
│       │   ├── .gitkeep         # placeholder; the vendored binary lands here at release
│       │   └── gregale          # linux-amd64 binary, vendored at release time
│       ├── examples/
│       │   └── basic.yml        # copy-paste starter workflow
│       ├── README.md
│       └── LICENSE
└── workflows/
    ├── cd-controlplane.yml      # existing single-host deploy
    ├── ci.yml                   # existing tests, lint, spec-check, etc.
    ├── codeql.yml               # existing
    ├── no-direct-push.yml       # existing
    ├── release.yml              # NEW: tag-driven cross-build + SHA256SUMS + Release
    └── stripex-sandbox.yml      # existing
```

The release workflow runs `go build` for the linux-amd64 target with
the same `LDFLAGS` (`pkg/wire.Version` stamping) that
`poyrazK/faas`'s `Makefile:15-17` already uses, generates a
`SHA256SUMS` file, copies the binary into `.github/actions/deploy/bin/gregale`,
updates `src/version.txt`, commits everything to a `release/v<tag>`
branch, **force-updates the `vN` moving tag** so customers pinned at
`@v0` always resolve to the latest public-beta vendored binary, and attaches the
binary + SHA256SUMS to a GitHub Release via
`softprops/action-gh-release@3bb12739c298aeb8a4eeaf626c5b8d85266b0e65 # v2.6.2`
(the same SHA pinned at
`poyrazK/faas/.github/workflows/cd-controlplane.yml:99-110`).

The companion `gregale deploy --github` flag in `poyrazK/faas`:

- New file `cmd/gregale/cmd_deploy_github.go` (snippet generator).
- New flag `--github` in `cmdDeployTarball` (`commands2.go`).
- Updated `cli_meta.go:280` Short description.
- New `cmd/gregale/cmd_deploy_github_test.go` (table-driven).
- New `TestCmdDeployTarball_GithubFlag` in `commands2_test.go`.

The snippet uses `${{ github.repository }}` / `${{ github.sha }}`
placeholders by default; when run inside an Actions runner
(`GITHUB_REPOSITORY` + `GITHUB_SHA` env vars are set), the snippet
hard-codes those values. The Action reference is pinned to
`poyrazK/faas/.github/actions/deploy@v0` during public beta (the moving-major shape
from the plan) and a `# pin:` comment line surfaces the immutable
SHA for customers who want reproducibility.

## Why no new server endpoint

The Action reuses the existing `POST /v1/apps/{slug}/deployments/source-ref`
endpoint (server-side handler at `cmd/apid/handlers_source_ref.go:1-156`,
SDK binding at `pkg/api/client.go:611-628` `DeployFromSourceRef`).
The wire shape is `{repo, ref, format}` — the Action sends
`format: "tarball"` exactly like the CLI does today. Server-side
resolves the install token from `github_installations` keyed by
`account_id` (not `repo_full_name`), so the Action has no
install-token plumbing of its own.

A new endpoint would have been duplicate code with a single
discriminator (the deployment kind, which already exists). The
audit row (`deploy.source_ref`) gains two new JSON fields
(`cli_version`, `action_version`) but no schema change — the
existing `audit_log` JSON column absorbs them.

## Why no OIDC in this slice

The first action uses the existing bearer-token contract
(`secrets.GREGALE_API_KEY`). OIDC / keyless requires a new
`pkg/auth/<provider>/` design and an ADR for the token-exchange
semantics. The issue body explicitly defers OIDC to a follow-up
proposal; the plan builds this in as a future ADR-094-track.

## Why no replacement of the webhook push-to-deploy path

PR-D's webhook loop is the right shape for teams that want
push-to-deploy. The Action is the right shape for teams that
want explicit-CI. Both stamp `DeploymentKind = "github"` on the
deployment row and the audit row distinguishes them by
`action_version` (the Action sets this; the webhook path sets
`null`). Customers pick one or the other — both are first-class.

## Consequences

- **Vendored binary = 15 MB repo.** Acceptable; `git clone
  --depth 1` keeps day-to-day work fast. The binary is committed
  per release, not on every commit, so the main branch stays
  light.
- **No signed releases in v1.** cosign signing is deferred to
  v2 (per the plan R5 mitigation). The release notes call this
  out, and the SHA256SUMS file is the deterministic verification
  surface for v1.
- **The snippet generator is a CLI-only path.** Adding
  `gregale deploy --github` to a non-CI customer is a no-op
  (the snippet is documentation). The wiring is `cmdDeployTarball`
  → `cmdDeployGithubSnippet` in `cmd/gregale/cmd_deploy_github.go`.
- **No migration needed.** The audit-row fields live in
  `audit_log` JSON column (no schema change). Plan R6 documents
  this explicitly.
- **Monorepo coupling.** Customers who pin `poyrazK/faas/.github/actions/deploy@v0`
  are coupled to the release cadence of the whole repo. A fork
  or refactor that moves the action to a separate repo is a
  documentation break for those customers. Plan R8 identified
  this risk; the tradeoff is "one big PR now" vs "two-PR cluster
  forever."

## Rollback

- The `gregale deploy --github` flag can be removed in a single
  revert; the Action code at `.github/actions/deploy` is unaffected.
  Customers who copied the snippet still have a working install.
- The Action code at `.github/actions/deploy` can be deleted in
  a single revert; no customer is yet pinned to it on day-1.
- The release workflow at `.github/workflows/release.yml` can be
  removed in a single revert; pre-existing tags are still tagged
  and the release assets still exist.
- The ADR has no downstream ADRs to retire.
