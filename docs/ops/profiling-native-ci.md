# Native profiling CI gate

`.github/workflows/profiling-native.yml` runs on relevant main-branch changes,
manual dispatch, or as a reusable workflow called after the native candidate stack
is installed. It has no PR trigger. Fork code must not run on this privileged
acceptance environment.

## Host and GitHub setup

Use a dedicated x86 Linux KVM acceptance stack and a self-hosted runner with labels
`linux`, `x64`, `gregale-profiling-acceptance`. Provision the GitHub environment
`profiling-acceptance` with:

- Variable `GREGALE_ACCEPTANCE_API_URL`: the dedicated stack's API origin.
- Secret `GREGALE_ACCEPTANCE_TOKEN`: a dedicated account token permitting app
  creation/deletion, deployments, secrets, lifecycle, instance/timeline reads and
  profiling investigation writes. The account must allow at least four concurrent
  instances and CPU profiling. Use an appropriate Scale acceptance account.
- Secret `GREGALE_NATIVE_PROFILE_TOKEN`: an independent random fixture-only secret
  of at least 32 characters. The provisioner seals it into the disposable app.

The host requires Go 1.25.13, Python 3, `timeout`, `flock`, `git`, `/dev/kvm`, and
write access to `/var/lock/faas-builder-acceptance.lock`. Install
`/etc/faas/profiling-acceptance-host` to designate this stack for the drill. Native
builder/e2e operations share the same host lock; the live platform remains running
while this gate holds it.

Install the candidate apid, scheduler, profiling service, VM daemon and updated
runtime/guest-init images before qualification. Host release tooling must then
write the tested 40-character source SHA into
`/etc/faas/profiling-acceptance-source-sha`, as a root-owned regular file without
group/other write permission. The gate requires it to match the checkout SHA and
records it as a **release stamp**, not an independent binary attestation. An
outdated acceptance stack fails preflight. Main-branch triggers therefore require
release automation to keep this stack current; the reusable workflow can instead
be called after candidate installation.

Pyroscope and gateway request telemetry must be enabled and working. Deployment
preview URLs must route through the real gateway to their pinned revisions.
Source builds use the platform builder VM, not host containers. Required public
builder image/module access follows the stack's normal network policy.

## Execution and artifacts

The provisioner creates a uniquely named `prof-ci-<random>` app, declares static
routes, enables two-second capture windows, and uploads four source revisions.
Baseline owns production traffic; regression, label loss and sparse revisions have
zero production traffic and are accessed using their deployment-pinned URLs.
The workload archive contains checkout source and a Dockerfile with a fixed mode;
it contains no API or probe tokens.

The gate runs the deployment runner and native adapter. It requires the known
hotspot, exact client/gateway request reconciliation, collector labeling evidence,
route filtering, persisted assessments, expected regression/sparse/label-loss
findings, native snapshot/restore evidence and post-restore traffic. It uses
`/readyz` to observe workload mode; `/healthz` can be answered from gateway cache.

A separate verdict step runs on failure as well. Missing evidence, incomplete
runs and failed cleanup cannot produce a passing gate. Artifacts are retained for
14 days and include:

- `acceptance.json`: captured windows, profiles, route metrics, saved investigation
  URLs and native evidence (including failure-stage receipts).
- `fixtures.json` and `config.json`: ownership journal, revision IDs and pinned URLs.
- `cleanup.json`: deletion scheduling and confirmation that no instances remain
  resident; errors produce a separate bounded error record.
- `provenance.json` and `verdict.json`: tested source/release stamp and final status.

API token values and sealed fixture secrets are omitted. Investigation URLs are
included for correlation, but deleting the disposable app can make them
unavailable; the JSON artifact retains the assessment summaries.

## Cleanup and interruption

The shell trap attempts cleanup on success, failure, TERM and INT with its own
four-minute budget. A failed cleanup retains the helper so the workflow's
`always()` step can retry. Journals are written before creation and after every
acknowledged resource mutation. Cleanup checks the generated fixture namespace
and observed app ID before scheduling deletion, then waits for zero resident
instances. Deletion follows Gregale's normal grace period; it does not claim
immediate physical removal of snapshots or historical rows.

Every GitHub run/attempt uses a separate evidence directory. If the runner host
crashes or a process receives SIGKILL, traps cannot run. Reap that run's journal
before the next qualification:

```sh
go run ./scripts/ci/profilefixtures -action cleanup \
  -out /path/to/interrupted-run-evidence
```

The same dedicated API URL and account token are required. Cleanup can be retried;
no fixture journal means no remote deletion. Do not reuse an interrupted evidence
directory for a new run.

For a manual host run, export the three configuration variables above and set a
new `GREGALE_PROFILE_EVIDENCE` directory, then run:

```sh
make native-profiling-acceptance
```

## Verification limits

The helper/workload build, shell syntax, Python syntax and workflow YAML parsing
can be checked in a portable workspace. A passing native execution requires the
configured candidate stack, credentials and KVM host. This change does not claim
that such a run has been performed here.
