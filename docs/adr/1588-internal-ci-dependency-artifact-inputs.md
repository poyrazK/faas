# ADR-1588: Explicit internal CI dependency artifact inputs

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1574, ADR-1336, ADR-1337 and ADR-1587

## Decision

Allow a job to declare up to eight named artifact files from its direct `needs`
dependencies. Paths are literal, relative artifact paths covered by the producer's
artifact selectors; names are unique CI identifiers. Inputs do not add dependencies.
Canonical normalization sorts declarations by name and never mutates caller data.

```yaml
jobs:
  build:
    image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    steps:
      - name: build
        run: mkdir -p dist && make build
    artifacts: [dist/app]
  test:
    image: registry.example/ci@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    needs: [build]
    inputs:
      - name: app
        job: build
        path: dist/app
    steps:
      - name: integration
        run: chmod +x .gregale-ci-inputs/app && .gregale-ci-inputs/app --self-test
```

## Frozen ownership and admission

Before inserting a consumer Jobs run, dispatch resolves each declaration against
its own account/execution's successful producer attempt. The producer must have
sealed output, verified frozen artifact metadata and a confirmed stopped native
task. Its managed object must be ready with matching owner, attempt, digest and
length. Failed, skipped and cancelled dependencies retain existing skip behavior.
Missing required artifact metadata or unusable producer receipts freeze a failed
consumer receipt (exit 125, skipped steps) in the fenced execution ledger without
creating a native run. The native output journal keeps its existing requirement
for a real run. Temporary database errors retain normal dispatch recovery with
the same attempt identity.

Resolved producer attempt IDs, SHA-256 digests and lengths are included in the
immutable native run command. Runtime reconstruction uses the same execution
snapshot and compares the complete command at existing grant/launch checks.
Controller/API restart cannot resolve another run's artifact or choose a newer
producer. Reruns resolve their own fresh producer attempts. No new database table,
column, mutable alias or migration is required: existing revisions, execution
snapshots, run commands, seals and storage journals retain the evidence.

Input jobs require manifest version 2; jobs without inputs retain version 1.
Upgrade the control plane and qualified pinned runner images before submitting
input definitions. Older runners reject version 2 rather than execute without
inputs. Drain input executions with current binaries before a binary downgrade;
keeping the schema alone does not make older binaries understand these contracts.
Keep admission off during binary rollback. Retained input definitions require
updated revision/execution readers before fresh admission.

## Scoped transport and guest materialization

`GET /v1/internal/ci/runtime/{run}/inputs/{input}` accepts a declared input name,
never a provider key or URL. It uses existing trusted HTTPS, task/instance-bound
capabilities, current native lease, account/repository/template policy and
internal/GitHub/dispatch/transport gates. Cancelled, sealed, stopped, foreign or
replaced consumers cannot download. Mixed account Authorization, cookies and
query overrides are refused. Producer seal/stop/verified-object evidence is
rechecked on each download. No public download authority or provider credentials
are issued.

The handler acquires the existing consumer transfer lease and managed storage I/O
lease, verifies the full bounded object into a temporary spool, and rechecks
consumer authority before returning bytes. Success is binary, no-store, with
pinned digest and length. Blob I/O occurs outside database transactions.

The guest checks out source, creates a new `.gregale-ci-inputs` directory, then
downloads each named regular file. An existing source directory/file/symlink at
that reserved name fails preparation. Destinations are flat validated names;
exclusive root-relative creation prevents overwrite or escape. Files are private
0600 byte copies with no executable mode, links or automatic archive extraction.
Commands can explicitly change their own copy's mode; producer objects remain
immutable. All inputs must pass independent guest length/digest checks before any
step starts. A fresh control read after all downloads observes cancellation or
lease replacement received during transfer before commands begin.
Lost/transient/truncated responses retry the same pinned descriptor
at most the existing three attempts; unavailable, corrupt or cancelled preparation
cannot run commands or claim success. No attempt or command is resubmitted.

Limits live in `pkg/api/limits.go`: eight inputs/job, existing 32 MiB/file and
64 MiB combined inputs/job, existing 16 KiB manifest and 15-second transfer timeout.
The job's native deadline also bounds preparation. Existing final-output sealing,
partial-output handling and confirmed native stop remain authoritative.

## Retention, gates and qualification

Reuse execution-wide storage retirement safety: producer objects cannot retire
while an execution/consumer remains active, even after normal TTL. Existing
managed reads allow expired objects while their execution is active. Transfers
use the existing bounded operation leases. After the execution and native work
finish, normal retention and archive cleanup apply. Input preparation failures
without a native run use the existing no-run retirement path.

Portable tests cover declarations/manifests, producer stop/seal and digest bounds,
account/execution isolation, immutable restart, missing inputs without native
admission, guest binary/empty inputs, retry/corruption, source/symlink collisions,
cancellation/replaced leases, protected HTTPS and retention through consumer stop.
Native/provider qualification belongs to the separate native agent: use new
pinned runner images and a real build-to-consumer pipeline, inject lost downloads,
restart apid/controller, cancel the consumer and confirm retention/cleanup.

All flags remain off and the service remains internal. Release stays paused;
no image publication, deployment or enablement is part of this change. Leave the
previously frozen qualification bundle untouched.
