# ADR-20261021083000: Accept nested-KVM evidence for the GitOps native journey

- **Status:** accepted
- **Date:** 2026-10-21
- **Related:** ADR-568, ADR-20261021080847, `docs/ops/gitops-native-ci.md`

## Context

The guarded GitOps runner (`make test-environment-gitops-native`) qualifies a
run only on a bare-metal host (`native`). `nested-diagnostic` runs can pass but
are recorded as `diagnostic_passed`. No dedicated bare-metal acceptance host
exists. Gregale's other metal suites are run and accepted on the internal n2
node, `gregale-internal-test-1`, which is an x86_64 Linux GCE VM with nested
KVM. The product owner decided on 2026-10-10 that n2 metal runs are sufficient
evidence, and that decision applies to GitOps too.

## Decision

1. Add the host class `nested` to the runner, the private report and the CI
   verdict. A complete run on such a host is `passed`, sets
   `native_acceptance_passed` and `source_scheduled_jobs_passed`, and records
   `evidence_host: "nested"` (`"native"` on bare metal). The field names stay
   as they are so that the v2 report contract is unchanged.
2. A `nested` claim must name its hypervisor (`systemd-detect-virt --vm` is
   not `none`). Bare metal cannot claim `nested`, and a VM cannot claim
   `native`. `nested-diagnostic` remains a troubleshooting class that cannot
   qualify.
3. Nothing else is relaxed. The same journey still has to pass: both
   providers, every scenario, clean pre- and post-run leak gates, an unchanged
   clean checkout, pinned images and Go, and binaries built from the checkout.
4. The manual CI lane keeps `native`, because its self-hosted runner label
   names a bare-metal host. Operators run the n2 evidence directly with
   `GREGALE_GITOPS_NATIVE_HOST_CLASS=nested`.

## Consequences

- A GitOps release can be qualified on n2. Its restore latencies and
  `after_restore` timing reflect n2's nested-KVM limits (ext4 without reflink,
  two vCPUs), not the production SSD node's.
- Reports and summaries name the evidence host, so a reader can tell n2
  evidence from bare-metal evidence.
- Enabling production execution is still an operator decision taken through
  the `environment_gitops_execution` flag (ADR-20261021080847). A report
  never changes it.
