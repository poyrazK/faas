# Five-minute source Runs and immutable dependency environments

Status: implemented as an operator-gated preview; writers default off. Bounded
functional KVM qualification passed. No production qualification or deployment
is implied.

## Context

Source Runs currently have a 30-second hard ceiling (10 seconds on Hobby) and
platform-curated dependency profiles. Larger data files and typed results make
longer transformations useful, but package installation must remain outside the
execution guest and the host.

## Decision

Add `limits.execution_class=extended` to v2 source admission. Its default and
maximum admission-to-result budget is 300000 ms, including queueing, restore,
execution and output publication. Standard defaults and maxima remain unchanged.
The extended class consumes the existing total account cap and a separate live
cap of one on Hobby/Pro and two on Scale. A separate extended worker lane
reserves standard dispatch capacity; per-class fair cursors avoid starvation. Existing RAM-second metering, memory,
CPU, PID, scratch and output limits apply; a timeout increase grants no other
resources. Cancellation, account suspension, lease fencing and acknowledged
teardown continue to own slot release.

Add immutable, account/key-family scoped dependency environment intents. The
customer supplies public-registry hash-pinned Python requirements or npm
`package.json` and integrity-pinned lockfile v3. Installer images are selected
and digest-pinned by the operator. Build identity covers the runtime, complete
recipe, installer image and recipe format. The recipe contains dependency
configuration, never credentials, custom Dockerfiles or shell commands. Python
accepts binary wheels only and requires hashes for every dependency. npm install
scripts, registry overrides, workspaces and non-registry sources are excluded.

`apid` owns customer intent, immutable selection and retirement. `builderd`
claims work on the existing bounded builder slot, generates a fixed context,
executes it in a fresh ephemeral builder VM and publishes a checksummed OCI
export. `imaged` verifies the export chain and bounded extraction, installs the
platform guest-init and marker, retains the complete OCI consumed-layer and
exact-byte artifact producer evidence, requires a complete Grype verdict with
no fixable critical findings, and publishes a reusable read-only base plus a
payload-free per-environment layer. `schedd` resolves only admitted, owned
identities and pins the exact image. Execution receives a fresh writable copy
and no package-install network access or persistent customer workspace.

Publication is fenced by expiring unique leases. Attempt keys are recorded
before writes in the durable `run-environments/` namespace. OCI storage uses
fixed, restart-enumerable repositories and reversible compact UUID tags; scan
sidecars retain separate exact-byte identities. Local artifacts use
shared faas-group directories under restrictive umasks, with physical empty
directory pruning after object deletion. Retirement denies fresh admissions immediately; active Runs
retain their immutable selection. Artifact ownership survives account deletion.
Physical GC waits for leases and active Runs; quotas remain reserved until GC
acknowledges deletion. Per-plan environment count is 2/5/10 and final base+layer
allowance is 256/512/1024 MiB on Hobby/Pro/Scale, with bounded extraction,
archive size and retries in `pkg/api/limits.go`. These are preview capacity caps,
not a change to the financial model or a new price.

Use additive stored columns and private protocol v5 for extended classes or
custom environments. Old guests reject v5. The encrypted payload version binds
custom environment selection to the dispatch identity. Existing file framing
continues to work in v5. Snapshot identity incorporates the environment's base
and actual guest executor digests; a cache miss cold boots the same base.

## Rollout and rollback

Apply migrations first. Upgrade imaged, builderd, scheduler, vmmd, guest artifacts,
API readers, CLI and SDKs before enabling new writers. Configure only tested,
digest-pinned installer images. `FAAS_EXECUTION_V2_ENABLED=1` remains necessary.
`FAAS_EXECUTION_EXTENDED_ENABLED=1` opts in to the class.
`FAAS_EXECUTION_ENVIRONMENTS_ENABLED=1` enables new environment intents/builds.
Both default off. Disable new writers to roll back, drain admitted v5 Runs and
builds, and retain compatible readers and imaged GC until obligations finish.
Native KVM acceptance and leakcheck remain release gates; Google virtualized
functional evidence does not confer native production eligibility.

## Validation and remaining gates

The [2026-11-15 evidence](../ops/evidence/20261115-run-extended-environments/README.md)
records all eight race-instrumented real-VM scenarios: actual isolated npm and
Python builds, producer/Grype publication, imports in fresh guests, artifacts and
result contracts, a 45-second Run alongside standard dispatch, the actual
five-minute deadline, running cancellation, retirement/GC, payload deletion,
single usage settlement and clean physical resources. Selected class/plan,
admission/protocol, ownership, lease, state/PostgreSQL, SDK/CLI, migration,
compatibility and lint checks pass.

The internal host reports Google virtualization. Native production acceptance,
production installer/runtime image provenance, measured fleet capacity, other
runtime/profile combinations, warm custom-environment restoration, remote
artifact backends and complete billing remain separate gates. Environment
builds use bounded builder capacity; this preview does not establish build
pricing or billing reconciliation. Retain compatible v5 readers when disabling
writers and until all admitted work and cleanup obligations drain.
