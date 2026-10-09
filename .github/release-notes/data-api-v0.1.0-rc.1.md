# Data API bundle v0.1.0-rc.1

This prerelease pairs the schema-generated Gregale Data API workflow with a
tested CLI and application SDK from one immutable source snapshot.

| Component | Version |
| --- | --- |
| Gregale CLI | `v0.1.18-data-api.1` |
| Application SDK | `@gregale/data` `0.1.0` (bundled tarball) |
| Source | `9e6901dbe6a0f1d6ea0ba54370fc662963184b88` ([PR #4368](https://github.com/poyrazK/faas/pull/4368)) |

The CLI and SDK have independent versions. `data-api-bundle.json` identifies
their exact compatible bytes, source commit, toolchains and immutable deploy
Action pin. Keep that manifest and `DATA-API-SHA256SUMS` with the downloaded files.

## Install and initialize

Prerequisites: Node.js 22 or newer, npm, GitHub CLI (`gh`), and tar. Use an empty
download directory. Download all assets so every checksum can be verified:

```sh
gh release download 'data-api/v0.1.0-rc.1' \
  --repo poyrazK/faas --dir data-api-v0.1.0-rc.1
cd data-api-v0.1.0-rc.1
sha256sum -c DATA-API-SHA256SUMS
# macOS: shasum -a 256 -c DATA-API-SHA256SUMS
```

Choose the archive matching your machine:

| Machine | Archive |
| --- | --- |
| Linux x86_64 | `gregale_0.1.18-data-api.1_linux_amd64.tar.gz` |
| Linux ARM64 | `gregale_0.1.18-data-api.1_linux_arm64.tar.gz` |
| macOS Intel | `gregale_0.1.18-data-api.1_darwin_amd64.tar.gz` |
| macOS Apple Silicon | `gregale_0.1.18-data-api.1_darwin_arm64.tar.gz` |

After checksums pass, extract that archive into `bin`. For Linux x86_64:

```sh
mkdir bin
tar -xzf gregale_0.1.18-data-api.1_linux_amd64.tar.gz -C bin
./bin/gregale version --json
./bin/gregale init --template data-api-starter --path ./notes
cd notes
node tools/artifacts.mjs pin ../data-api-bundle.json
npm run typecheck --prefix client
npm test --prefix client
```

The pin installs the matching CLI under `.gregale-tools/gregale` and vendors the
SDK into the starter. Commit `data-api-artifacts.json`, `client/package.json`,
`client/package-lock.json` and `client/vendor/gregale-data.tgz` together. Follow
the generated starter README for database binding, application authentication,
migrations and `data-api sync`. Running against Gregale requires a configured
account and Data API endpoint.

For a fresh checkout, keep a local copy of this bundle and run:

```sh
node tools/artifacts.mjs restore --from /absolute/path/to/data-api-v0.1.0-rc.1 --cli
npm ci --prefix client --ignore-scripts --no-audit --no-fund
```

Use local bundle restoration or the committed SDK tarball for application CI.
The starter's HTTPS restoration intentionally refuses redirects; GitHub release
download URLs redirect and should not be configured as its artifact base URL.

## Included capabilities

- Schema-derived TypeScript types and Swagger definitions; a typed starter.
- Migration/type/client synchronization with serving-schema fingerprint checks.
- RLS-isolated CRUD, relationships, pagination and optimistic updates.
- Allowlisted atomic RPCs, subject-scoped idempotency and receipt retention.
- Request IDs, runtime log correlation and SDK response diagnostics.
- Structural schema snapshots and breaking-change checks.

## Qualification and limitations

The source tree matches the PR commit that passed all six workflows and all 36
core CI jobs. The bundle is built twice with identical checksums, and the Linux
x86_64 packaged CLI/SDK is tested through fresh starter initialization,
dependency installation, type checks, request tests and clean-checkout restore.
All four CLI targets are cross-built and their archive/build metadata verified;
macOS and ARM64 executables are not run in this Linux x86_64 environment.

Live staging/native-fleet canary qualification remains pending because no target
is available. This release is for evaluation, and is not a stable production
qualification. Structural diffs do not detect RLS policy, function-body or
default-expression behavior changes; those require behavioral tests.

The SDK retains its existing proprietary LICENSE and requires authorization to
use or redistribute. This release does not change licensing or publish a package
to npm. The tarball is installed through the bundled starter pin workflow.

See the [Data API guide](https://github.com/poyrazK/faas/blob/9e6901dbe6a0f1d6ea0ba54370fc662963184b88/docs/data-api.md)
and [bundle guide](https://github.com/poyrazK/faas/blob/9e6901dbe6a0f1d6ea0ba54370fc662963184b88/docs/data-api-packaging.md).
