# ADR 20261117183000 — Run system packages and typed task handles

Status: implementation under the existing disabled environment and Run writer gates.

## Decision

Extend immutable dependency recipes with at most 32 exact Wolfi
`NAME=VERSION-rN` system package pins, each at most 128 bytes. Packages may be
combined with the existing locked language dependencies or used alone. Sort
pins for identity, reject duplicate package names and build syntax, and keep
language-only recipe digests unchanged. The complete recipe remains bounded
by the existing byte allowance and environment quotas.

The platform emits a fixed exec-form `apk add --no-cache -- ...` instruction
inside the existing isolated builder VM. The pinned installer supplies its
repository configuration and signing keys. A fixed interpreter-version check
refuses a package selection that replaces the selected Node/Python major/minor.
No package-manager command runs on the Gregale host. Existing image size,
consumed-layer provenance and complete vulnerability-scan gates still apply.
Direct package versions and the installer enter the recipe identity; the final
produced image digest pins execution. This does not promise identical transitive
repository resolution between independent builds.

System recipes use `queued_system_v1`, `building_system_v1`, `built_system_v1`
and `imaging_system_v1`. N-1 claim predicates cannot acquire them. New workers
claim both lanes in creation order; expiry, attempts, diagnostics, retirement
and collection preserve their existing fences. Ready images use the existing
read/admission contract. Public receipts normalize the worker states. Expand,
validate and contract migrations widen the status check before new writers run.
Upgrade API readers before admitting the new lane; retain capable workers until
its obligations drain during rollback.

Add preview TypeScript and Python `Task`/`TaskRun` handles. A caller supplies an
exact task version, explicit idempotency key and runtime result decoder. The
published server schemas remain authoritative. File locators are prepared
before an optional durable intent callback; admission is probed before upload.
Only the exact missing-file refusal permits staging and another admission
request under the same identity. `submit` replays a persisted intent without
requiring the original local files. No helper invents a new retry identity.

Handles expose result waits, explicit cancellation and durable artifact delivery.
A failed execution or invalid result contract cannot reach decoding or delivery.
Ending a client wait does not cancel execution. Delivery uses a deterministic
Run/artifact key under the destination route, polls pending receipts, and retains
the last delivery locator on a timeout. Account and tenant authorization remain
server decisions. The SDK mints no customer download URL automatically.

This increment supports system tools and typed invocation. Private package
authentication and automatic schema-to-source type generation remain separate
increments. It adds no guest protocol or VM lifecycle owner.

## Acceptance

Exercise injection/duplicate rejection, canonical identities, unchanged legacy
digests, packages-only recipes, memory/PostgreSQL owner transitions, diagnostic
leases, recipe copies and old-worker exclusion. Check CLI pin forwarding and
OpenAPI/SDK parity. SDK tests cover missing input staging, replay before upload,
persisted intent recovery, invalid contracts, failed Runs, client wait endings
and pending delivery recovery in TypeScript and Python sync/async paths.

The separate `system-environments` functional KVM cohort installs real pinned
Python/Poppler packages, requires a real produced-image scan, converts a PDF
through a named task, checks escaped stored HTML and the result contract, replays
the task and retires its resources with one usage entry. Functional evidence on
the selected virtualized internal node does not widen native production eligibility.
