# SDK publishing

Status: **no Gregale SDK is published to any public registry yet.** The
metadata is valid, the artifacts build, and the license is decided (Apache-2.0,
2026-10-29; see [Licensing](#licensing)). The Data API release job is enabled;
its first publication requires the registry credential and a release. Other
language packages retain the setup steps below.

The Go, Node and Python SDKs are published as source to public repositories in
the `gregale-dev` org ([ADR-20261112110000](../docs/adr/20261112110000-sdk-public-repositories.md)):
[gregale-go](https://github.com/gregale-dev/gregale-go),
[gregale-js](https://github.com/gregale-dev/gregale-js) (`packages/sdk-node`)
and [gregale-python](https://github.com/gregale-dev/gregale-python). This
repository stays the source of truth: the `sdk-mirror` workflow runs
`scripts/sdk_mirror.py` on every merge that touches `sdk/go`, `sdk/node`,
`sdk/python` or `sdk/mirror*`, and commits the result with an
`Upstream-Commit:` trailer. Never commit to those repositories directly; the
next sync overwrites it. Packages missing from `sdk/mirror.json` (Auth, Data,
the sandbox packages, Swift, Kotlin) stay here until they launch.

Each repository has its own deploy key with write access, stored here as
`SDK_MIRROR_DEPLOY_KEY_GO`, `SDK_MIRROR_DEPLOY_KEY_JS` and
`SDK_MIRROR_DEPLOY_KEY_PYTHON`. To publish a new repository, add it to
`sdk/mirror.json` and the workflow matrix, create the empty public repository,
add its deploy key and secret, and run the workflow. Check a tree locally with:

```sh
python3 scripts/sdk_mirror.py check
python3 scripts/sdk_mirror.py build --repo gregale-go --out /tmp/gregale-go
```

| Language | Install name | Import as | Registry state |
|---|---|---|---|
| Go | `github.com/gregale-dev/gregale-go` | `faas` | needs a `vX.Y.Z` tag on gregale-go |
| Node | `@gregale/sdk-node` | `@gregale/sdk-node` | not published; npm scope `@gregale` exists |
| Data API | `@gregale/data` | `@gregale/data` | release job enabled; first publication requires the npm credential (see [Data API SDK](#data-api-sdk)) |
| Application Auth | `@gregale/auth` | `@gregale/auth` | source package in `sdk/auth`; not published |
| Python | `gregale-sdk` | `faas_sdk` | PyPI project unregistered |

## What was broken

Every documented install path failed:

- **Node** — `package.json` name was `gregale` + `/skd-node`: a typo (`skd`),
  and npm rejects it outright because an unscoped name may not contain a
  slash. `"private": true` also blocked publishing, contradicting the
  `publishConfig` and `prepublishOnly` in the same file. The documented
  install `npm install github:poyrazK/faas#v0.1.0` cannot work either: npm has
  no support for installing from a subdirectory of a repo, the monorepo root
  has no `package.json`, and `dist/` is gitignored. The README's own quick
  start imported the invalid name.
- **Go** — `go.mod` declared `github.com/poyrazK/faas-go` and the README said
  `go get github.com/poyrazK/faas-go`. That repository does not exist (404).
- **Python** — distribution name was `faas_sdk`, which is generic and, worse,
  collides with a **real, unrelated PyPI package**: `faas-sdk` v1.0.1 is owned
  by Sonra Intelligence Ltd. A customer following the SDK's own name would
  have installed another company's code.

## Licensing

Decided 2026-10-29: the client SDKs are licensed under the **Apache License
2.0**, while the platform stays proprietary (the root `LICENSE` names the
exception). `sdk/node`, `sdk/python`, `sdk/go`, `sdk/data` and `sdk/auth` each
carry the license text in `LICENSE`; the npm packages declare `Apache-2.0` with
`publishConfig.access: "public"`, and the Python project declares `Apache-2.0`.
The Kotlin and Swift sources are not packaged and keep the root license.

No publish workflow publishes the Node, Python or Go SDKs yet. A public
npm/PyPI publish is effectively irreversible: npm's unpublish window is 72
hours and PyPI does not allow re-uploading a version at all. Name squatting is
permanent.

## Go

The module lives at the root of `gregale-dev/gregale-go`, so versions are
plain tags there. Tag the mirror commit whose `Upstream-Commit:` trailer is the
release you want:

```sh
git clone https://github.com/gregale-dev/gregale-go && cd gregale-go
git tag v0.1.0 <mirror-commit>
git push origin v0.1.0
```

Then `go get github.com/gregale-dev/gregale-go@v0.1.0` resolves via the module
proxy. Nothing else is required — no registry account, no secrets. Go is the
only one of the three that needs no external setup. A Go tag is permanent
once the proxy has seen it.

Verify a tag before announcing it:

```sh
GOPROXY=proxy.golang.org go list -m github.com/gregale-dev/gregale-go@v0.1.0
```

## Node

Manual, one-time, and not automatable from CI:

1. The `@gregale` npm organization exists (it holds the CLI's
   `@gregale/cli-*` packages).
2. Create an automation access token and add it as the `NPM_TOKEN` repository
   secret.

Then:

```sh
cd sdk/node
npm ci && npm run build
npm publish
```

`prepublishOnly` reruns the build, so a stale `dist/` cannot ship.

## Data API SDK

`release.yml` has a `publish-data-sdk` job for `@gregale/data`. It runs on release
tags by default; set `PUBLISH_DATA_SDK=false` to pause publication. Configure the
existing `NPM_TOKEN` repository secret before releasing. On each release tag it:

1. installs the locked dependencies without lifecycle scripts and runs
   `npm test` (build, declaration and type tests, unit tests);
2. checks the packed files, including the live-read module (`dist/live`)
   behind `@gregale/data/live` and `@gregale/data/live/react`;
3. publishes the version in `sdk/data/package.json` unless the registry already
   has the exact package integrity. Reusing a version with different bytes fails
   the release and requires a version bump.

`publishConfig.access` is `public` (Apache-2.0, see [Licensing](#licensing)).
The `@gregale` scope already holds the CLI's `@gregale/cli-*` packages, so no
registration step remains.

The generated Python and Go clients (`gregale data-api types --language`) are
not packages: the CLI renders them into each project.

## Python

1. Register the `gregale-sdk` project on PyPI (currently unregistered).
2. Prefer a Trusted Publisher (OIDC) over a long-lived API token.

```sh
cd sdk/python
poetry build
poetry publish
```

Do **not** publish as `faas-sdk` or `faas_sdk`. That name resolves to Sonra
Intelligence Ltd's package and shipping under it would be a supply-chain
confusion hazard for customers.

#4262 (2026-10-06) put the distribution name back to `faas_sdk`, and the
customer docs then told users to `pip install 'faas_sdk[auth]'`, which
installs Sonra's package (Hunt 13 H13-4). `tests/test_packaging.py` now pins
`gregale-sdk`. Until `gregale-sdk` is registered on PyPI, anyone can claim
that name too, so customer docs install only from the repository
([docs/sdks.md](../docs/sdks.md)), never by bare name.

## Local install (works today, no registry)

The customer-facing install page is [docs/sdks.md](../docs/sdks.md). The
paths customers can use right now:

- Node — `npm pack` a tarball, then `npm install /path/to/tarball.tgz`
- Python — `poetry build -f wheel`, then `pip install dist/*.whl`
- Go — `go get github.com/gregale-dev/gregale-go@<commit>` works today; a `vX.Y.Z` tag on gregale-go adds versions
