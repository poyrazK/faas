# ADR-643: Fresh image healthchecks gate readiness

- **Status:** implemented; native boot/restore qualification pending
- **Date:** 2026-10-07
- **Problem:** Importing OCI and Compose checks (ADR-642) did not make their
  results authoritative for main workload readiness. A container could accept
  TCP connections while its declared command failed, and an unsolicited pass
  carried no identity for a new boot or snapshot restore.
- **Decision:** After merging the immutable image config, accepted Compose
  override, and deployment runtime overrides, imaged records whether the
  effective image manifest declares a command check in the deployment's
  existing runtime profile. Schedd projects this receipt through the wake
  contract. Vmmd requires both the existing network/characterization readiness
  result and a new command success before completing a serving wake. No-bind
  worker characterization cannot bypass the command gate.
- **Protocol:** A host-generated 256-bit challenge travels over the instance's
  private Firecracker Unix-vsock proxy to the existing guest STREAM listener
  on port 1028. Message types 12/13 distinguish the command check from HTTP/gRPC
  liveness probes. The response echoes that challenge only after executing the
  check; old DGRAM reports, counters, timestamps, and cached passes are never
  readiness evidence. Frames are bounded, and responses contain closed outcome
  codes rather than command output or environment values.
- **Guest execution:** The main process installs a runtime only after launch.
  The check uses its user/groups, environment, working directory, and open
  workload cgroup. Rotated scoped secret bindings are refreshed for each
  attempt while platform identity and configuration remain intact. Each
  request executes the declared CMD or CMD-SHELL afresh.
  Failures during startup grace do not consume retries; interval, startup
  interval, command timeout, and retry counts retain OCI precision/defaults.
  The existing startup deadline bounds the entire readiness sequence, including
  transport failures and grace. Probe output is bounded; timeout/cancellation
  kills its process group. Main-process retirement cancels in-flight checks,
  and a replaced runtime cannot acknowledge its predecessor's success.
- **Lifecycle:** Cold boot and ordinary restore apply the same gate. Restore
  runs it after the mandatory resume entropy/clock hook. Paused warm-pool
  reservations do not claim verification; their in-place serving resume runs
  the resume hook and a fresh check. Migration adoption follows ordinary wake.
  Failure uses the existing startup-failure cleanup and deployment failure
  paths, so it cannot emit successful priming or move traffic from the serving
  deployment. Public-route smoke checks and latest-revision fences still apply.
- **Compatibility:** Schedd checks the node's advertised support before sending
  a required check and verifies the serving RPC acknowledgement afterward. An
  older daemon, replaced handler, older guest, missing check, malformed frame,
  canceled request, stale challenge, or missing result cannot authorize a
  required serving wake. Existing deployments without the effective-manifest
  receipt retain legacy readiness; reassembly/redeployment records the receipt.
  `NONE` and absent command checks preserve normal network readiness. Runtime
  profile updates remain constrained to the image materialization stages;
  accepted Compose contracts and profile-copy paths retain ADR-642 semantics.
- **Scope:** This applies to primary image deployments. It does not add command
  execution on a host, a new VM lifecycle owner, recurring command-driven
  eviction, or Compose overrides for source-built workloads.

[ADR-644](644-image-healthcheck-runtime-recovery.md) adds recurring command
monitoring and scheduler recovery after serving startup.

Portable regression coverage includes command execution with runtime context,
startup grace/retries/timeouts, process retirement, framed challenge handling,
network and worker readiness, stale snapshot results, missing reports, daemon
compatibility and teardown, immutable-image receipt handoff, and preservation
of the serving deployment after candidate failure.

`TestMetalImageHealthcheckAcrossSnapshots` builds guest-init from the current
checkout and exercises cold boot, ten successful restores from one snapshot,
a paused warm-pool resume, a listening but unhealthy guest, and restore of a
snapshot containing an old
pass plus a failing current check. Run on an isolated dedicated native x86_64
Linux KVM host with static busybox, debugfs, mkfs.ext4, Firecracker/jailer, the
normal native network/cgroup prerequisites, and `FAAS_TEST_KERNEL` configured:

```sh
FAAS_TEST_IMAGE_HEALTHCHECK=1 \
RUN_REGEX='^TestMetalImageHealthcheckAcrossSnapshots$' \
make test-metal PKGS=./pkg/fcvm
make leakcheck
```

A skip, unavailable KVM, or portable fake transport is not native acceptance.
