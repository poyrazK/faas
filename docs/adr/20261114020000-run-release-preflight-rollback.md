# ADR 20261114020000 — Run release preflight and compatible rollback

Status: implemented and verified on Linux; production native qualification
remains a separate gate. See the
[tooling evidence](../ops/evidence/20261114-run-release-preflight/README.md).

## Decision

`vmmd run-release` supplies read-only `capabilities`, `prepare`, `preflight`
and `rollback` commands. Dispatch happens before daemon startup. No command
opens a database or daemon socket, starts a VM, repairs a journal, creates
operator directories, edits configuration, installs a binary or restarts a
service. `prepare` writes a draft plan to stdout for review.

A reviewed plan pins candidate and rollback binaries, their reader manifests,
both TOML configurations, six runtime/helper artifacts and the native Run
acceptance summary. Paths are canonical and absolute. Inputs reject symlinks,
special files and group/world write; JSON has explicit size/depth bounds and
rejects unknown plan fields, duplicates, case aliases and trailing values.
Large runtime artifacts are hashed as streams. The candidate's real config
loader parses the exact pinned bytes and applies its normal environment
overrides, so a release check sees the deployment environment's settings.

## Reader and rollback compatibility

Capability manifests name their exact vmmd binary digest and Linux/amd64
target. This release requires launch-owner version 3, template-flight version
1, nested runtime-capture authority version 2, KVM-only device scope version
2 and publication-intent version 2. Both candidate and rollback must support
this complete profile, even with no native records. Unknown newer format
claims also require review rather than an inferred compatibility promise.

Capabilities are declarations from trusted release artifacts, not signatures.
The normal signed installer and operator-selected release bundle remain the
trust boundary. The tool never executes a referenced old binary to discover
its capabilities: an old vmmd can start its daemon on an unknown verb.
It neither proves that other nodes installed a reader nor supplies a fleet
lock. The existing rollout must check every node and keep its drain and
operation-completion barriers.

The rollback configuration turns runtime capture off while retaining native
recovery and launch-owner records. It must retain publication, image, snapshot,
persistent-resource and volatile-resource roots and volatile-write settings.
Mixed adoption, qualification capture and prepared-network caching are refused
for this bounded profile. Directory inventories report retained entry counts;
they do not parse ownership, prove physical retirement or classify audit
records as disposable. Runtime startup remains the strict journal reader.

Older-reader downgrade and journal migration are unsupported. Incomplete
capture records and catalog-protected artifacts stay intact. A missing native
journal before first enablement grants no permission to use an older reader.
An incompatible target means retain a compatible release, not erase state.

## Enablement and rollback are different decisions

`preflight` requires a locally observed nonvirtualized Linux amd64 KVM host
with cgroups v2. Its reviewed, independently pinned Run summary must explicitly
qualify native production operation, bind the exact source and candidate vmmd,
bind all six release artifacts, and cover all 26 crash/publication/clone/OOM
scenarios, ledger assertions, KVM-only device receipts and leak exits.
Functional-only internal-node evidence cannot pass this gate.

`rollback` validates a compatible, writer-disabled target. It retains the
original acceptance bytes without promoting them to production evidence.
It does not require a passing new-release qualification, so a failed release
or functional test environment can still retreat to a compatible reader.
It still requires Linux amd64, KVM, cgroups v2, exact pins, retained roots and
both compatible readers. Its result explicitly reports that production
enablement was not checked. Neither command grants runtime admission or
replaces signed image provenance, ADR-739 qualification or a release rehearsal.

Commands return 0 for a passing check, 3 for a blocked check and 1 for invalid
input or an inability to parse configuration. Successful checks emit one JSON
object with plan digest, host observations, per-check reasons, retained counts
and ordered operator steps. TOML contents and parser diagnostics stay private.

The [operator runbook](../ops/run-release-preflight.md) defines the rollout and
rollback sequence. Existing source admission, SQL, RPCs, native journal formats,
default capture settings and native cleanup ownership are unchanged.
