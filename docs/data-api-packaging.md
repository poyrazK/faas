# Data API CLI and SDK bundles

Build a compatible Gregale CLI and `@gregale/data` artifact from one committed
source snapshot. This is local packaging; it does not create a release, tag,
pull request or registry publication. Public SDK distribution remains governed
by [the SDK publishing policy](../sdk/README-publishing.md).

## Build

Use the Go toolchain pinned by `go.mod`, Node.js 22 or newer, npm, Git, GNU tar
and gzip. Install the repository's Go dependencies normally. The builder
requires a clean checkout, copies `git archive HEAD` into a temporary directory,
installs locked SDK dependencies without lifecycle scripts, compiles its types,
and cross-builds the CLI with CGO disabled. It never builds a customer image.

```sh
node scripts/build-data-api-bundle.mjs --version v0.0.0-data-api.local \
  --out-dir /path/to/data-api-bundle
```

The output directory must be new and outside the source checkout. By default
it builds the current host's target. For all four supported CLI targets:

```sh
node scripts/build-data-api-bundle.mjs --version v0.0.0-data-api.local \
  --out-dir /path/to/data-api-bundle \
  --targets linux/amd64,linux/arm64,darwin/amd64,darwin/arm64
```

Include `linux/amd64` when the application's preview CI runs on GitHub's
Ubuntu runner; the local pin operation also needs the developer's host target.

`--version` labels the CLI; the SDK keeps its version from `sdk/data/package.json`.
The manifest pairs their exact bytes rather than assuming equal versions imply
compatibility. The deploy Action pin defaults to the snapshot's CLI source;
`--action-sha FULL_SHA` can select an already-approved immutable Action commit.
The builder performs no remote ref resolution or upload.

The output contains:

- `gregale_<version>_<os>_<arch>.tar.gz` for each selected CLI target.
- `gregale-data-<sdk-version>.tgz`, containing the built application SDK.
- `data-api-bundle.json`: format/contract version, full source commit, commit
  timestamp, Go/Node/npm versions, CLI version and Action pin, SDK name/version,
  artifact sizes, SHA-256 checksums and the SDK's npm SHA-512 integrity.
- `DATA-API-SHA256SUMS`, covering every archive and the manifest.

The CLI embeds the full source commit and uses the commit time as its build
timestamp. Builds use `-trimpath -buildvcs=false`; archives reuse Gregale's
existing deterministic tar/gzip helper. With the same source, options and
toolchain versions, repeated builds must produce identical checksums. The
packaging gate verifies this, including the SDK tarball and manifest.

## Initialize and pin

Extract the host CLI from the approved bundle, then use it to initialize the
starter. Verify `DATA-API-SHA256SUMS` against a trusted bundle before extracting
or executing a downloaded CLI:

```sh
cd /path/to/data-api-bundle
sha256sum -c DATA-API-SHA256SUMS
mkdir /path/to/gregale-bin
tar -xzf gregale_<version>_linux_amd64.tar.gz -C /path/to/gregale-bin
/path/to/gregale-bin/gregale init --template data-api-starter --path /path/to/notes
cd /path/to/notes
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json
```

Pin verifies the local SDK and host CLI before installing them. It checks the
CLI's embedded version, source commit and timestamp. It writes the application
pin, installs `.gregale-tools/gregale`, and updates npm's lockfile using the
explicit SDK file path so replacing a same-version tarball updates its integrity.
Commit `data-api-artifacts.json` and `client/package-lock.json` together.

To restore from a local copy:

```sh
node tools/artifacts.mjs restore --from /path/to/data-api-bundle --cli
npm ci --prefix client --ignore-scripts --no-audit --no-fund
```

Restore validates the committed SDK dependency, version and lock integrity
before any download. It fails on a corrupt existing vendor file; it does not
silently replace a mismatched file or rewrite the lockfile. A changed pin
requires another explicit `pin` operation. Checksums establish consistency with
the reviewed pin; obtaining that pin and bundle from a trusted source remains
necessary.

## Application CI

The initialized starter includes CI workflows. For pull-request client checks,
commit the authorized SDK tarball at `client/vendor/gregale-data.tgz`, or configure
an immutable HTTPS artifact directory while pinning:

```sh
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json \
  --base-url https://artifacts.example.com/approved/COMMIT/
```

The directory must serve the exact filenames from the manifest with HTTP 200.
The pin contains no credentials. URLs must use HTTPS, have a trailing slash and
have no user info, query or fragment. Downloads use curl's normal proxy/CA
configuration, a timeout and pinned size bound, and refuse redirects. Errors
do not include response bodies, download URLs or bearer values.

Automatic client CI restores/verifies only the SDK without credentials. Vendor
it for private artifact locations. Manual preview CI, on protected main, also
restores the matching CLI. Its optional `GREGALE_ARTIFACT_TOKEN` is passed only
to artifact restoration, through curl stdin. Management credentials and
application JWTs are passed only to their separate drift/authorization steps.
See [the starter setup](../cmd/gregale/templates/data-api-starter/README.md).

## Validate

```sh
make data-api-packaging-check
# Retain a tested host bundle:
bash scripts/test-data-api-packaging.sh --out-dir /path/to/tested-bundle
# Test a previously built bundle:
node scripts/test-data-api-bundle.mjs /path/to/data-api-bundle
```

The packaging gate builds twice, compares all checksums and metadata, runs the
packaged CLI's `init`, and installs the packaged SDK into that fresh starter.
It then simulates a new CI checkout with another empty npm cache, restores the
pin, and runs locked dependency installation, migration unit checks, client type
assertions and client request tests. It uses no staging or management credentials.
The Data API acceptance workflow runs this gate and saves its tested files as a
GitHub Actions artifact; it does not upload a public release or publish to npm.

This gate proves packaging and client installation. Live provider/native fleet
qualification and the isolated staging canary remain separate acceptance gates.
