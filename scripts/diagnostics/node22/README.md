# Node 22 identity-map diagnostic

The production Node 22.23.2 musl runtime prints `Check failed: !is_iterable()`
without naming which of seven V8 operations failed. This diagnostic labels the
existing checks while preserving their conditions and fatal behavior. It is a
diagnostic artifact, not a proposed crash correction or production runtime.

The dedicated CI workflow follows the platform base-image build pattern in
`images.yml` (ADR-114). It uses the pinned Alpine runtime parent, verifies the
official Node source checksum and installed executable, builds an unmodified
control, then applies the seven labels and incrementally rebuilds with the same
compiler and default configuration. It checks musl/x64, runtime build flags,
compiled labels, and 8,000 validated framed responses for each executable.
The job has a 120-minute cap and uses two compiler jobs. It uploads a seven-day
artifact; it does not publish images or invoke deployment jobs.

The adapter fixture is extracted from `pkg/rootfs/build.go`. Inputs and logs
contain only synthetic invocation IDs. Core dumps are disabled. Artifact
metadata includes source, patch, executable, compiler and package identities.

For a VM comparison, put each executable in its own newly created app layer,
then cold boot and capture a **fresh** snapshot. Never replace the binary under
previously captured process memory. Keep the same handler, plan, CPU allocation,
load pattern and Firecracker pin for both cases. A passing fresh control does
not clear the previously failing production snapshot. Nested GCP KVM results are
diagnostic; native lifecycle and recovery acceptance is required for a fix.
