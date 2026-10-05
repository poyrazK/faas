# ADR-595: Regional deployment previews

Date: 2026-10-02

Status: Internal implementation; native fleet qualification pending.

## Context

ADR-591 introduced immutable app placement, and ADR-593 carries it through
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
Connected-repository previews carry the flag through ADR-593's project planner.

The local simple-app `--plan` contract carries compute region through JSON,
human output, and its compiled CreateAppRequest. It performs canonical validation
without contacting apid or requiring authentication. It describes creation
intent; it cannot validate current admission or an existing app's identity.
The human deployment preflight also shows explicit placement.

Go, Node, and Python SDKs support the diff request and effective-region response.
The Go SDK adds the previously missing typed diff client, and Node exposes the
generated diff service through its public entry point. Workflow durations in the
Go preview DTO use the same duration strings as the wire contract.

Workflow snippets accept `--region` only when the referenced immutable deploy
Action supports it. Releases verify the Action's versioned capability manifest
and bundled CLI at the same SHA, then bind that capability to the embedded pin.
The default remains network-free. An older embedded Action refuses region
selection with guidance to use `--pin-action` or `--pinned-sha`; these explicit
pins verify the manifest and released bundle before emitting a workflow.
Source commits without a vendored CLI cannot satisfy this check.

The Action's optional `region` input forwards canonical placement intent to
`gregale deploy --region`. It verifies bundled CLI support before exchanging
OIDC or deploying. Omission preserves existing placement and remains compatible
with older CLI bundles. Explicit intent must match an existing app's immutable
choice, including the legacy unconstrained identity. The release canary exercises
region creation through the exact Action and compiled CLI before publishing.

## Validation and rollout

Portable tests cover omission, matching intent, canonical validation, immutable
conflicts, disabled admission, unknown/paused/unavailable regions, and zero app
creation during preview. CLI tests verify the three preview spellings issue only
the read-only planner request, and local plans remain network-free. SDK tests
cover request/response transport, including explicit empty versus omission.
OpenAPI and embedded specs remain identical; generated clients retain the fields.
Action tests cover canonical identifiers, omission, older bundles, and argument
forwarding. Snippet tests cover exact-pin capability binding, older/unreleased
Actions, cancellation, and failure without workflow output. A hermetic API fixture
checks the compiled CLI's creation request carries the region.

This is a preview extension to the shared-fleet compute placement contract.
It does not add regional data residency, independent control planes, global
ingress routing, or multiple regional targets under one app identity. Retain the
internal capability and default-off gate until ADR-592/468 native acceptance,
affected `test-metal` coverage, and `leakcheck` pass on Linux x86_64/KVM.
