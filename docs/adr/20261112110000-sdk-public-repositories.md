# ADR 20261112110000 · The SDKs are published to public repositories in the gregale-dev org

- **Status:** accepted
- **Date:** 2026-11-12
- **Amends:** issue #266's plan to split the Go SDK into its own repository
  (the "PR 12" note that `sdk/go/go.mod` carried). This repository stays the
  source of truth; the public repositories are published copies.
- **Related:** [sdk/README-publishing.md](../../sdk/README-publishing.md),
  [Install the SDKs](../sdks.md), ADR-1190 (application Auth, dark-launched)

## Context

Customers install the SDKs from `github.com/poyrazK/faas`, a personal account,
by pointing pip at a subdirectory, building an npm tarball from a clone of
the whole platform, or importing the Go module
`github.com/poyrazK/faas/sdk/go`. The SDK's public address names a person and
carries the platform's history, and the Go import path would break the day
the repository moves.

The SDKs cannot simply leave this repository. They are generated from and
checked against platform code in the same pull request:

- `make sdk-spec` and `sdk-gen-node`/`sdk-gen-python` regenerate them from
  `api/openapi.yaml`;
- `scripts/sync_binding_runner.py` copies `pkg/bindingrun` into the Go SDK
  and checks its limits against `pkg/api/limits.go`;
- contract fixtures (`sdk/service-binding-contracts`, `sdk/binding-runner-tests`,
  `sdk/operation-tests`, `sdk/fakeapid`) are shared by the platform's tests and
  the SDKs' tests.

A feature that changes the API and its SDK methods lands as one change.

## Decision

1. **One public repository per language in the `gregale-dev` org:**
   `gregale-go` (the Go module at the root), `gregale-js` (npm packages under
   `packages/`, `@gregale/sdk-node` first) and `gregale-python`
   (`gregale-sdk` at the root). Swift and Kotlin get `gregale-swift` and
   `gregale-kotlin` when they ship; Swift Package Manager needs `Package.swift`
   at a repository root.
2. **This repository stays the source of truth.** `scripts/sdk_mirror.py`
   builds each public tree from `sdk/mirror.json` (tracked files only, plus the
   `sdk/mirror/` overlays: root README, CONTRIBUTING, SECURITY) and the
   `sdk-mirror` workflow commits it to the public repository's `main` after
   every merge that touches it. Each public commit names its source in an
   `Upstream-Commit:` trailer. Pull requests opened on a public repository are
   ported here and closed.
3. **The Go module path is `github.com/gregale-dev/gregale-go`.** Versions
   are plain `vX.Y.Z` tags on the public repository, not `sdk/go/vX.Y.Z` tags
   here. A vanity path (`gregale.dev/go`) was the alternative; it survives a
   future move but needs the website to serve `go-import` metadata first.
4. **Public repositories start from a single "Initial import" commit.** The
   SDK history here (about 2,500 commits across the three directories) stays
   here.
5. **Only packages that customers can already install are published.**
   `@gregale/auth`, Swift and Kotlin (application Auth, ADR-1190 dark
   launch), `@gregale/data` (Data API preview) and the sandbox packages
   (`@gregale/agents-sandbox`, `@gregale/sandbox-e2b`, `gregale-sandbox-e2b`)
   are not in `sdk/mirror.json`; each joins when its capability launches. The
   Go, Node and Python SDKs are published whole, the same content
   [Install the SDKs](../sdks.md) already points customers at.
6. **Links that leave the published tree are rewritten.** A Markdown link to
   a page the docs site publishes (`docs/customer-pages.json`) goes to
   gregale.dev; any other link into this repository goes to it on GitHub.
   `sdk_mirror.py check` fails on a link that resolves nowhere.
7. **Each public repository gets its own deploy key.** The workflow reads
   `SDK_MIRROR_DEPLOY_KEY_GO`, `_JS` and `_PYTHON`; a missing secret skips that
   push. Pull requests build and check the trees (Go build and vet, `npm pack`,
   a Python wheel) without pushing.

## Consequences

- The install page and package metadata name the public repositories.
  Existing users pinned to a `github.com/poyrazK/faas/sdk/go` commit keep
  working; the new path is a new module.
- Some SDK tests read fixtures that stay here (the shared contract fixtures,
  `pkg/flags/testdata`, `tests/fixtures`), so they fail in a public checkout.
  The full suite runs here. Making each public repository test itself is
  follow-up work, needed before those repositories run their own CI.
- Registry publishing (npm, PyPI, Go tags) is unchanged by this record and
  still follows [sdk/README-publishing.md](../../sdk/README-publishing.md),
  now from the public repositories.
- Moving the platform repository itself into the org later changes one
  value, `upstream` in `sdk/mirror.json`.

## Rejected

- **Moving SDK development out (a full split).** Every API change would need
  two pull requests in order, and the generators and contract checks would
  have to work across repositories.
- **One `sdks` repository.** Go would keep directory-prefixed tags and Swift
  could not be consumed from a subdirectory.
- **Carrying history over with `git subtree split`.** It publishes years of
  internal references for no customer benefit; blame stays available here.
