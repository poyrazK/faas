# ADR-383: Curated dependency profiles for stateless executions

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** Add immutable, platform-built dependency profiles to disposable Runs.
- **Why:** Agents need numerical and tabular tools without installing dependencies on each run.
- **Consequences:** Profile-specific images and snapshots, pinned image provenance, and guest protocol v3; shared base residency increases.
- **Rejected alternatives:** Per-run pip, arbitrary dependency manifests, and persistent workspaces add latency, network access, mutable supply chains, or a separate disk lifecycle.

## Decision

The optional `profile` field defaults to `standard`. The first additional
profile, `python-data-v1`, requires `python313` and provides NumPy, pandas,
python-dateutil, and six. Exact versions, CPython 3.13 Linux x86_64 wheel URLs,
and SHA-256 checksums live in
[`pkg/executionprofiles/python-data-v1.json`](../../pkg/executionprofiles/python-data-v1.json).
The upstream distributions are [NumPy](https://pypi.org/project/numpy/),
[pandas](https://pypi.org/project/pandas/),
[python-dateutil](https://pypi.org/project/python-dateutil/), and
[six](https://pypi.org/project/six/).

The trusted platform image pipeline installs this closed wheel set without
pip, dependency resolution, build hooks, or wheel entry scripts. Changed
package bytes require a new profile ID. OS/kernel/guest security rebuilds may
produce new image digests with the same package set; receipts identify the
selected base bytes. The image is a shared read-only base, counted once in
fleet residency. Runs still use fresh jails, deny-all networking, existing
resource/output caps, ephemeral scratch, and teardown before terminal commit.

Profile becomes part of snapshot identity, publication, and durable lookup.
The standard profile preserves its historical key. Nonstandard profiles use
separate release metadata and never inherit standard artifact keys. An active
restore lease pins the base image digest in the execution row before VM
restore or cold boot. Dispatch rejects unpinned data runs, and a retry cannot
silently select a different image. Profile and selected digest are immutable
in PostgreSQL as well as through the state APIs.

The encrypted request binds the profile to source/input. The scheduler checks
this binding before dispatch. Data runs require guest protocol v3; older
vmmd/guest binaries reject them. A platform-owned execution marker identifies
the guest image profile, and a mismatch fails before caller code runs. The
data wrapper verifies Python 3.13 and every installed distribution version,
imports NumPy/pandas, and caps numerical-library threads to one. Standard
Python retains `-I -S`; the data profile uses `-I` to load trusted site packages.

Receipts expose the resolved `profile`, its declared `packages`, and optional
`runtime_image_digest` after selection. Declared versions appear on admission;
they are verified before caller code can produce a successful data receipt.
No source, input, dependency credentials, or host path enters this projection.
CLI and SDKs accept profile selection and preserve receipt provenance.

## Verification and rollout

Local tests cover admission, encrypted profile binding, real Python data
execution and CSV export, profile mismatch, both gRPC paths, snapshot storage,
lease-fenced pinning, retry image changes, and terminal provenance immutability.
The image CI lane checks imports and versions as uid 1000 with a read-only
rootfs and no network, staged guest-init, and all CRITICAL vulnerabilities.

Apply the migration before deploying the new API/scheduler. Build and scan the
profile image, opt into the existing image staging path with a digest-pinned
`FAAS_EXECUTION_PYTHON_DATA_V1_BASE_REF` on imaged, and build sanitized snapshots
with the matching guest digest. Unconfigured profiles consume no base residency.
Configure all `FAAS_EXECUTION_PYTHON313_PYTHON_DATA_V1_*` fields only after this
image passes validation; configure no fallback to standard layers. Layers must
preserve the data profile marker. Existing execution API/dispatch gates remain.

Verified wheel extraction adds 95,824,011 bytes (91.38 MiB) of package files;
this excludes the OS, interpreter, guest-init, ext4 metadata, and filesystem
allocation. Measure total shared-base residency, cold/warm startup latency, and
peak guest memory on native x86_64 Linux KVM before enabling production.
`make test-metal` and `make leakcheck` remain release gates. Local interpreter
tests and cross-compilation do not establish VM isolation or performance.
