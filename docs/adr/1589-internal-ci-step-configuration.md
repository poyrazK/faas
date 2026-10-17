# ADR-1589: Explicit internal CI environment and working directories

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1574, ADR-1651 and ADR-1588

## Decision

Add optional `env` string mappings and `working_directory` strings to jobs and
steps. These are literal, non-secret configuration stored in ordinary immutable
revision/execution metadata. They do not provide secret storage, expressions,
interpolation or environment inheritance from the runner process. YAML numbers,
booleans and null values must be quoted when intended as environment strings.

```yaml
version: 1
repository: gregale/example
jobs:
  test:
    image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    env:
      BUILD_MODE: test
    working_directory: packages/api
    steps:
      - name: prepare
        working_directory: .
        run: mkdir -p packages/api
      - name: unit
        env:
          BUILD_MODE: unit
          OPTIONAL_FEATURE: ""
        run: make test
    artifacts: [packages/api/reports/**]
```

## Environment contract

Each step receives a freshly constructed environment: the runner's fixed `PATH`,
checkout-root `HOME`, `LANG=C.UTF-8`, `CI=true`, and `GREGALE_CI_WORKSPACE` set to
the checkout root, followed by job values and step overrides. Empty values are
real overrides; changes made by one shell do not become another step's settings.
The environment is sorted by name and never copied from the runner/daemon.
Values such as `$OTHER` remain literal bytes until explicitly used by shell code.

Custom names match `[A-Za-z_][A-Za-z0-9_]*`. Bounds in `pkg/api/limits.go` apply
to each declaration and its merged effective custom environment: 32 names,
64 bytes/name, 1,024 bytes/value and 8 KiB combined `NAME=value` entries including
terminating NULs. Values must be UTF-8 without NUL; multiline and empty strings
are supported. The fixed runner entries are outside these custom bounds. Existing
64 KiB definition and 16 KiB native manifest limits still apply.

Reject names case-insensitively for `FAAS_`, `GREGALE_`, `LD_` and `DYLD_` prefixes
and `PATH`, `HOME`, `LANG`, `CI`, `PWD`, `OLDPWD`, `SHELL`, `ENV`, `BASH_ENV`,
`SHELLOPTS`, `BASHOPTS`, `CDPATH` and `IFS`. Runtime identity/capability settings
are neither supplied to steps nor overridable through definition configuration.
Job templates retain empty environment overrides and native effective environment
snapshots remain empty; these values live inside the immutable CI manifest.

## Directory contract

An omitted/empty step directory inherits the job directory; an omitted/empty job
directory selects the checkout root. Explicit `.` selects the root, including
when overriding a job directory. Every path is relative to the checkout root,
not the job directory. Paths must be UTF-8, at most 256 bytes, with no absolute
path, backslash, NUL, CR/LF/tab, empty component or embedded `.`/`..` component.

Directories must exist when their step starts. A root-scoped earlier step can
create directories for later steps; the runner does not create them implicitly.
Every component is opened relative to the anchored checkout descriptor with
directory-only, no-follow, close-on-exec flags. Links, special files, missing
directories and traversal fail the affected step with exit 125 and skip later
steps. Existing cancellation/deadline behavior remains authoritative.

The Linux guest changes directory through `/proc/self/fd/<opened descriptor>`
before exec closes the descriptor. This keeps pathname replacement from changing
the selected directory and avoids passing the descriptor into repository commands.
Only the verified native Jobs guest constructs the OS executor. Portable tests
use fake executors and inspect command construction without starting a shell.

Artifact selectors, JUnit selectors and `.gregale-ci-inputs` stay relative to
the checkout root. Commands in a subdirectory can access inputs through
`$GREGALE_CI_WORKSPACE/.gregale-ci-inputs/NAME`. Working directories do not change
the VM boundary or create a new sandbox boundary within the guest.

## Snapshots and compatibility

Normalization clones both job and step maps; deterministic canonical JSON covers
the new settings in the revision digest, execution plan and immutable native run
command. API/controller restart reconstructs the same settings. No schema change,
new mutable configuration record or new feature flag is needed.

Any job with nonempty custom environment mappings or an explicit nonempty
working directory requires manifest version 3, including jobs with artifact
inputs. Jobs using inputs alone retain version 2; unchanged jobs retain version 1
and their existing canonical serialization. Reject versions inconsistent with
the declared features. Earlier runners reject version 3 rather than ignore the
configuration. Results retain version 1.

Upgrade readers and qualified pinned runner images before admitting definitions
with these fields. Drain such executions using current binaries before a binary
downgrade, keep admission off during rollback, and use current readers for retained
configured definitions before fresh admission.

## Verification and rollout

Portable tests cover strict string parsing, reserved settings, custom/effective
bounds, immutable maps/digests, version compatibility with and without inputs,
real PostgreSQL restart reconstruction, and fake-executor HTTPS execution.
Directory tests cover generated directories, overrides, links, special files,
replacement, cancellation and closed-on-exec descriptors. Root-relative outputs
and dependency inputs are exercised through the real scoped API.

The separate native agent must qualify the new pinned runner on a native Jobs VM:
confirm descriptor-based chdir and closure, literal environments, cancellation,
root-relative inputs/outputs, native stop and cleanup. No native/provider result
is claimed by portable tests. The frozen qualification bundle stays untouched.

The service stays internal, all feature flags stay off, and release stays paused.
This change does not publish images or deploy the service.
