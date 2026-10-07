# Managed PostgreSQL host-veth identity diagnostics

Date: 2026-10-03. ADR-402. Customer cutover disabled; native acceptance pending.

The final source passed full macOS race suites for `pkg/fcvm` and `cmd/vmmd`,
bounded Linux race regressions,
49 selected top-level metal/portable tests (126 including subtests),
zero skips/failures, changed-code Linux lint and three lock-held leak checks.
The surviving-guest fixture records version-4 veth index/creator provenance;
replacement-daemon recovery still quarantines its lease and grants no cleanup.

## Source and environment

- Validation snapshot: `dcb0c6077323c99f58a10aa6abeb0f4934079481`.
- Tree: `a812cd46e5d42b153ef78cebcf1215c96ece903d`; parent: `6173eca54f3d08c0513211cd643a911278dbdb38`.
- Source archive SHA-256: `0bfaf8f5cf075098840f9b727dd53c678ea25cd6b09df94dde718465b03482ac`.
- Lossless change SHA-256: `aaab3b94c679709927cdf31bcc7a720a5a8687a4198e0f06b26bd3698e531edd`.
- All 6,258 workspace Go/module blobs matched the snapshot before upload.
- All 4,226 selected Go/module files matched the uploaded source after execution.
- Node: `gregale-prod / us-east1-b / gregale-internal-test-1`.
- Unit: `gregale-managed-pg-resource-links-f8b10c6a-complete`; invocation: `493a6a07d4064f11ac6e11716112ec20`.
- The GCP node has nested virtualization enabled. This run is diagnostic only;
  supported lifecycle acceptance requires dedicated native x86_64 Linux KVM.
- Private Go/module caches were copied into the owned disk stage; lint used a new private cache. The unit
  used a private jail tmpfs and build tmpfs, and held the shared acceptance lock
  across fixture construction, tests and all three leak checks.

The immutable source archive includes the Makefile, dependency pins, selected
runtime source, guest sources, deployment generator inputs, scripts and ADR.
[validation.json](validation.json) lists the selection and input hashes.
The final implementation commit adds result documentation; its Go/module source
must match this snapshot. No source changed during the diagnostic run.

## Coverage and results

[runner.sh](runner.sh) contains the exact commands and bounded metal selection.
The full macOS race suites are in [portable-full.log](portable-full.log);
bounded Linux race regressions are in [portable-linux.log](portable-linux.log).
The bounded `make test-metal` output is in [metal.log](metal.log).
The final source also passed 20 race-enabled repetitions of the real replacement
fixture (80 replacement cases), recorded in [link-repeat.log](link-repeat.log).
Real-link tests cover atomic tagged creation, indexed cleanup, same-name foreign
veth/dummy replacements, MAC mutation and renamed originals. The real prepared
alias test moves a veth peer with its namespace and checkpoints the claim.
Portable regressions also cover intent/checkpoint/retirement fsync failure,
creator changes on absent resources, unknown owners, live private replacement/
detachment and unpublished failed attachments. Rtnetlink parser tests reject
malformed nested attributes.

The selected run also repeats jail/nsfs replacement guards, surviving guests,
restart quarantine, failure reports, retained teardown and daemon configuration.
It does not replace the unfiltered native release gate. Generated deployment
checks, test citations, text encoding, shell quoting, sealed-environment scope and
ADR-number checks passed. Explicit test/lint exits are zero. The completed unit
is inactive; [cleanup.log](cleanup.log) confirms removal of the owned disk stage.
The three leak checks in [run.log](run.log) ran before releasing the shared lock.

The first bounded metal run found that initial IFLA_IFALIAS was ignored by the
kernel. Cleanup retained that unrecognized link, and an early prepared fixture
failure left a namespace. `bounded-failed-*` preserves this failed source run
(snapshot `69a9e6f14231c489d5908246412b2baf9f638531`). The final source uses an
initial locally administered MAC marker; the prepared fixture registers cleanup
before creating resources. The exact UUID fixture namespace was removed under
the test lock, and leakcheck passed (`link-probe.log`). `address-probe.log`
confirms initial MAC application. The complete final diagnostic run follows this
repair; failed results are not counted as a passing gate.

A later MAC-based run (snapshot `cb5c87cd61b7b746f367327d916880b7ca8707cd`)
refused teardown correctly but failed the foreign-veth observation assertion;
its lint also found one expression-style issue. `observation-failed-*` preserves
both results. A diagnostic retry (snapshot `ce890f2b744cc598e263baebde9e0ffcf9818295`)
added before/after/error reporting and passed 20 non-race fixture repetitions.
The node has active udev with `MACAddressPolicy=persistent` (`link-policy-read.log`).
Its asynchronous policy is a plausible explanation for the original mismatch,
not a proven diagnosis because that failure did not capture field differences.
The final fixture explicitly sets the foreign MAC during creation, which avoids
persistent-MAC reassignment according to the
[systemd implementation](https://github.com/systemd/systemd/blob/main/src/udev/net/link-config.c).
The final source fixes lint and retains the richer assertion diagnostics.
`repeat-prior-failed-*` preserves the intervening run; the final pinned run is
counted separately. No earlier failure is counted as a passing gate.

An initial unfiltered Linux race attempt was OOM-killed before metal execution.
Its cleanup leak check passed; the failure evidence is preserved in `initial-*`
and `failed-diagnostics.log`. The unit reported a 3 GB peak below its 10 GB cap;
the specific OOM constraint was not established. The retry used lower Go memory
settings and the established bounded Linux selection, on the same pinned source.
Initial local disk pressure also interrupted development builds. A private local
cache enabled the final full macOS suites; both packages passed. Local lint also hit
a disk-space error (`lint-initial.log`); final changed-code lint ran on Linux.
No failed attempt is counted as a successful gate.

## Limits and next work

A 46-bit random MAC marker detects accidental replacement; it is not authentication against
privileged actors. Interface indices can be reused between observation and
indexed deletion because Linux has no atomic address compare-and-delete. Context
checks are observations rather than namespace anchors held for the operation.
Peers and TUN resources are not separately journaled. Reopened observations grant
no lifecycle authority, serving state or recovered-report application.

Crash-safe prepared spare/alias handoff, verified restart cleanup, complete
incarnations/receipts, loop/parent mounts, jail-local staging, immutable snapshot
publication, filesystem power-loss qualification and durable all-node drain proof
remain pending. Customer activation remains disabled.

## Artifact integrity

[SHA256SUMS](SHA256SUMS) covers committed evidence bytes. `RAW_SHA256SUMS` records
original captured bytes before log whitespace normalization. The downloaded
result archive hash matched the remote hash before stage cleanup.
`source-change.patch.json` preserves every original patch byte as JSON lines;
joining `lines` reconstructs a patch whose SHA-256 is the one above. The raw
source/result archives and private scratch scripts are outside the repository.
