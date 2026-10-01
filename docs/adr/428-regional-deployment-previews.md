# ADR-428: Regional deployment previews

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

ADR-424 introduced immutable app placement, and ADR-426 carries it through
project plans. Standalone deployment previews previously refused `--region`.
This prevented operators from reviewing placement before deploying and left
existing regional placement invisible in otherwise successful diffs.

## Decision

Add optional `compute_region` creation intent to the read-only app diff request.
Omission or null preserves an existing app's region. An explicit different
choice, including clearing a selected region or repinning a legacy app, emits
a blocking `conflict` break before any mutation. Invalid identifiers emit a
blocking validation break. Region selection is not a mutable app-config patch.

The shared diff engine reports effective placement in the inner diff payload
and human output. Existing apps report their stored choice even when the request
omits the field. A fresh regional app reports a creation change. The JSON and
server response adapters preserve the same effective region and blocking bit.

For fresh regional apps, apid checks its default-off operator gate, the registry
admission policy, and eligible regional nodes. A disabled gate, unknown or paused
region, or outage produces a blocking break in the normal HTTP 200 diff envelope.
Existing identities do not require new admission and remain previewable after
the operator disables new regional creation or pauses its registry policy.
The planner reserves no capacity and deployment repeats admission checks.

`gregale deploy --region REGION --dry-run`, `--diff`, and `--server-diff` all use
apid's read-only planner for explicit regional intent. This keeps operator
policy authoritative without duplicating it in CLI discovery reads. Omitting
the flag retains the existing local diff path and still exposes stored placement.
Connected-repository previews carry the flag through ADR-426's project planner.

The local simple-app `--plan` contract carries compute region through JSON,
human output, and its compiled CreateAppRequest. It performs canonical validation
without contacting apid or requiring authentication. It describes creation
intent; it cannot validate current admission or an existing app's identity.
The human deployment preflight also shows explicit placement.

Go, Node, and Python SDKs support the diff request and effective-region response.
The Go SDK adds the previously missing typed diff client, and Node exposes the
generated diff service through its public entry point. Workflow durations in the
Go preview DTO use the same duration strings as the wire contract.

Workflow snippet generation still rejects `--region`: the CLI's default pinned
deploy Action predates that input. Reserve the regional app first and let the
workflow deploy to that existing identity until a supporting Action release can
be pinned. Do not emit an input the referenced immutable Action cannot consume.

## Validation and rollout

Portable tests cover omission, matching intent, canonical validation, immutable
conflicts, disabled admission, unknown/paused/unavailable regions, and zero app
creation during preview. CLI tests verify the three preview spellings issue only
the read-only planner request, and local plans remain network-free. SDK tests
cover request/response transport, including explicit empty versus omission.
OpenAPI and embedded specs remain identical; generated clients retain the fields.

This is a preview extension to the shared-fleet compute placement contract.
It does not add regional data residency, independent control planes, global
ingress routing, or multiple regional targets under one app identity. Retain the
internal capability and default-off gate until ADR-425/427 native acceptance,
affected `test-metal` coverage, and `leakcheck` pass on Linux x86_64/KVM.
