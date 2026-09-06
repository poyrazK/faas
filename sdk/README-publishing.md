# SDK publishing

Status as of the packaging-metadata fix: **no Gregale SDK is published to any
public registry.** All three are now *publishable* — the metadata is valid and
the artifacts build — but the actual release is gated on the licensing decision
in [Blocker: licensing](#blocker-licensing).

| Language | Install name | Import as | Registry state |
|---|---|---|---|
| Go | `github.com/poyrazK/faas/sdk/go` | `faas` | needs a `sdk/go/vX.Y.Z` tag |
| Node | `@gregale/sdk-node` | `@gregale/sdk-node` | npm scope `@gregale` unregistered |
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

## Blocker: licensing

`LICENSE` at the repo root reads *"PROPRIETARY SOFTWARE — NOT OPEN SOURCE"*,
and both SDK `LICENSE` files are `LicenseRef-onebox-internal`.

Publishing a client SDK to a public registry under a non-redistributable
license is a legal question, not an engineering one. The conventional pattern
for a closed-source platform is to license the **client SDK** permissively
(Apache-2.0 or MIT) while the service stays proprietary — that is what
customers need in order to vendor, audit, and redistribute the client inside
their own applications.

Until that is decided:

- `sdk/node/package.json` keeps `publishConfig.access: "restricted"`.
- No publish workflow is wired into CI.

**Decide first, then publish.** A public npm/PyPI publish is effectively
irreversible: npm's unpublish window is 72 hours and PyPI does not allow
re-uploading a version at all. Name squatting is permanent.

## Go

Nested modules in a monorepo resolve versions from **directory-prefixed
tags**. A bare `v0.1.0` will not work:

```sh
git tag sdk/go/v0.1.0
git push origin sdk/go/v0.1.0
```

Then `go get github.com/poyrazK/faas/sdk/go@v0.1.0` resolves via the module
proxy. Nothing else is required — no registry account, no secrets. Go is the
only one of the three that needs no external setup.

Verify a tag before announcing it:

```sh
GOPROXY=proxy.golang.org go list -m github.com/poyrazK/faas/sdk/go@v0.1.0
```

## Node

Manual, one-time, and not automatable from CI:

1. Register the `@gregale` npm organization (currently unregistered).
2. Create an automation access token and add it as the `NPM_TOKEN` repository
   secret.
3. Flip `publishConfig.access` to `"public"` **only** once licensing allows,
   and align `"license"` with the decision (it is `SEE LICENSE IN LICENSE`
   today).

Then:

```sh
cd sdk/node
npm ci && npm run build
npm publish
```

`prepublishOnly` reruns the build, so a stale `dist/` cannot ship.

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

## Local install (works today, no registry)

The paths customers can use right now are documented in each SDK's README:

- Node — `npm pack` a tarball, then `npm install /path/to/tarball.tgz`
- Python — `poetry build -f wheel`, then `pip install dist/*.whl`
- Go — already works via `go get` once a `sdk/go/vX.Y.Z` tag exists
