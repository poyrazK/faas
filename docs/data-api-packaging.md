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

## Install into an existing client

With a trusted CLI already installed, use the verified SDK installer from any
project containing a client package.json:

```sh
gregale data-api install --bundle /approved/data-api-bundle.json \
  --directory /path/to/project --client-directory web
# On a fresh checkout:
gregale data-api install --restore --directory /path/to/project \
  --client-directory web --from /approved
```

The command verifies the SDK without extracting or replacing the installed CLI.
It writes `data-api-artifacts.json` at the project root and a file dependency in
`web/package.json`, plus its npm lock. Commit all three files together. `--base-url`
can record an immutable HTTPS artifact directory during the initial install;
restoration supports the same private artifact token and no-redirect behavior as
the starter tooling. Restore verifies existing bytes and lock integrity before
running `npm ci --ignore-scripts`. A corrupt existing vendor archive fails rather
than being replaced. No package is published by this command.

## Application CI

The initialized starter includes CI workflows. For pull-request client checks and local scenario replay,
configure an immutable, publicly readable HTTPS artifact directory while pinning:

```sh
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json \
  --base-url https://artifacts.example.com/approved/COMMIT/
```

The directory must serve the exact filenames from the manifest with HTTP 200.
The pin contains no credentials. URLs must use HTTPS, have a trailing slash and
have no user info, query or fragment. Downloads use curl's normal proxy/CA
configuration, a timeout and pinned size bound, and refuse redirects. Errors
do not include response bodies, download URLs or bearer values.

Automatic client CI restores and verifies the SDK and matching CLI without
credentials, then runs the shipped CRUD, RLS and RPC scenarios against disposable
loopback services. Include a `linux/amd64` CLI target and a build supporting
`data-api dev --replay` and version 2 scenarios. Vendoring only the SDK does not
supply the CLI needed for replay. Manual preview CI, on protected main, also
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
assertions and client request tests, then replays the shipped scenarios with
the restored CLI. Docker with a running local daemon is required for this final
check. It uses no staging or management credentials.
The Data API acceptance workflow runs this gate and saves its tested files as a
GitHub Actions artifact; it does not upload a public release or publish to npm.

The gate also creates old/current schema snapshots with the shipped CLI, adds a
compatible migration, generates an acceptance policy, and replays pinned clients.
It imports the installed SDK tarball to verify old-client writes and RPCs,
current-client RLS isolation, and rejection of unapproved writes/RPCs before they
change rows. Separate runs verify revocation and stale-policy startup rejection
without overwriting accepted contracts or generated types. Local service and
volume cleanup is checked before a success report is written.

The same rollout driver also qualifies a fresh orders project with its own
migration directory, seed, generated output and buyer/other identities. Its
project-owned SQL RPC and RLS policy use both subject and tenant claims; a third
identity with the same subject and a different tenant must remain isolated.
The custom project installs and restores its CLI/SDK from the bundle, compiles
both old and current generated types against the installed SDK with library
checking enabled, exercises CRUD
and RPCs, and repeats policy acceptance, revocation and stale-policy rejection.
It has no notes table or dependency on starter identity names. Changes to the
orders fixtures trigger the existing packaging CI job.

The gate also executes pagination, deadline, optimistic mutation and bulk tests
against the installed SDK. It installs each concurrency, idempotency, outbox,
bulk and import-preview example as a fresh project, generates its database
contract, compiles its client with strict library checks, and runs its required
local scenarios using the shipped CLI/runtime. Their fingerprints are included
in the verification report. The full acceptance gate separately exercises
PostgreSQL races, crash recovery, cancellation and rollback for these workflows.

A successful run adds `data-api-verification.json` and
`DATA-API-VERIFICATION-SHA256SUMS` alongside the immutable bundle. The report binds
all 22 required passing checks, including both projects’ old/current contract
fingerprints, to the source commit, actual archive checksums,
manifest checksum and original checksum-list checksum. It identifies the host
CLI target actually executed; other target archives receive integrity checks,
not execution qualification. The report contains fixed check names and schema
fingerprints, not credentials, application responses, temporary paths or child
logs. Verify its sidecar checksum before retaining or sharing the report.

Starting a new verification removes previous reports. A failed run exits nonzero
and leaves no success report. Reports are written only after installation,
rollout checks and cleanup succeed, with artifact bytes checked again afterward.
The original `DATA-API-SHA256SUMS` remains unchanged, so build reproducibility
checks continue to cover only the deterministic bundle. CI uploads the bundle
and report together.

This gate records local packaged-runtime evidence. Live provider/native fleet
qualification and the isolated staging canary remain separate acceptance gates.
