# ADR-021 · Image digest enforcement hardening (G1): RFC 7807 codes + OCI pull timeout (M9)

- **Status:** accepted
- **Date:** 2026-07-21
- **Closes:** Spec §17 G1 (image digest enforcement; "M5 blocked real apps
  without it" — partially closed by apid's `isDigestPinned` gate at
  apid ingest in PR #35; this ADR hardens the imaged puller-side and the
  operator override surface).

## Context

PR #35 closed the customer-facing half of G1 by gating
`api.CreateDeploymentRequest.Image` on a full `host/repo@sha256:…`
regex at `cmd/apid/handlers.go:118` (`isDigestPinned`). The four bases
in `pkg/imaged/base.go`, however, were not hardened. Operators can
override those via `FAAS_BUILDER_BASE_REF` and `FAAS_DEPLOY_BASE_REF`;
today the overrides accept any ref shape, including bare `:latest` tags
(`cmd/imaged/main.go:141-142`). That makes the G1 policy asymmetric:
the customer-facing surface enforces digest pinning, the operator-
facing surface does not. Drive0 (the read-only base ext4 every guest
boots on top of) is silently mutable across deployments because the
`:latest` rebase passes through with no operator warning.

Separately, the imaged pull path lands three fundamentally different
failure modes — registry 404, manifest-list rejection, egress-policy
denial — behind a single free-text `deployments.error` string. There
are no sentinels to `errors.As`; the only signal an operator gets is
`"oci pull failed: registry returned 404 for manifest-…"`. The
separation between "your image doesn't exist" and "your image's
manifest is multi-arch" and "your image's registry tried to reach our
metadata service" matters: those are three distinct remediation paths,
and the customer-side RFC 7807 response collapses them to one opaque
422.

A third issue: `pkg/oci/registry.go:63` hardcodes the registry
`http.Client.Timeout` to 30 seconds. Every other timeout in the
codebase is a named constant in `pkg/api/limits.go`
(`BuildTimeoutSeconds`, `BuildE2ETimeoutSeconds`, `WakeQueueTTLSeconds`,
`IdleTimeoutFloorSeconds`). The magic number breaks the spec's
"platform-level ceilings, not magic numbers" rule.

This ADR captures the four coupled decisions that harden G1:

1. **Three puller-side sentinels in `pkg/oci`** that map to stable
   RFC 7807 codes via `pkg/api.SentinelToCode`.
2. **`deployments.error_code` column** for persistence, so audits /
   the M7.5 dashboard can group by failure mode without parsing free
   text.
3. **`OCIPullTimeoutSeconds` constant** in `pkg/api/limits.go`,
   overridable at the daemon env var boundary; no per-deployment knob.
4. **Operator override gate** that refuses to start imaged with a bare
   tag in `FAAS_BUILDER_BASE_REF` / `FAAS_DEPLOY_BASE_REF`; the four
   base-ref constants in `pkg/imaged/base.go` themselves stay
   `…:latest` (see D4 / "Rejected alternatives").

## Decisions

### D1. Three puller-side sentinels, mapped to RFC 7807 codes

`pkg/oci/errors.go` (new) defines three stable sentinels:

- `oci.ErrImageNotFound` — registry returned 404 on the manifest blob.
- `oci.ErrImageEgressDenied` — the egress policy denylist
  (`pkg/oci/egress.go`) refused the resolved IP at dial time.
- `oci.ErrImageManifestInvalid` — the manifest body is a multi-arch
  manifest-list, fails schema validation, or otherwise cannot be
  reduced to a single-platform `Manifest`.

Each sentinel is wrapped at its site via `fmt.Errorf("%w: …", …)` so
`errors.Is` matches both the bare sentinel and any %w-wrapped form.

`pkg/api/errors.go::SentinelToCode` (and the convenience wrapper
`LiftSentinel`) consults `errors.Is` against each sentinel and returns
the matching code. The mapping (`pkg/api/errors.go::StatusForCode`)
uses:

| Sentinel                     | Code                          | HTTP |
| ---                          | ---                           | ---  |
| `oci.ErrImageNotFound`       | `CodeImageNotFound`           | 422  |
| `oci.ErrImageEgressDenied`   | `CodeImageEgressDenied`       | 403  |
| `oci.ErrImageManifestInvalid`| `CodeImageManifestInvalid`    | 422  |

`CodeImageEgressDenied` is intentionally 403, not 422. The egress
denylist exists to enforce spec §11 / ADR-019 — an outbound dial
attempt is a security-class signal, not a "your image was malformed"
signal. Conflating it with the 422-class validation codes means a
customer cannot tell "registry said 404" apart from "platform stopped
us from reaching your image's host"; both are recoverable but via
different playbooks.

`pkg/imaged/handler.go::buildImageLayer` lifts at its three pull-side
failure sites (`PullDigest`, `PullImageConfig`, the two-drive /
`PullLayers` fallback):

```go
digest, err := h.oci.PullDigest(ctx, ref)
if err != nil {
    code, _ := api.LiftSentinel(err)
    return h.markDeployFailed(ctx, dep, code,
        fmt.Sprintf("oci pull failed: %v", err))
}
```

The free-text `error` column is preserved as before; the new
`error_code` column carries the stable signal.

### D2. `deployments.error_code` column

`migrations/00021_deployment_error_code.sql` (NEW):

```sql
-- +goose Up
alter table deployments add column if not exists error_code text;

-- +goose Down
alter table deployments drop column if exists error_code;
```

Backfill: none. Existing rows have `error_code = NULL`. The DTO
(`pkg/api/dto.go::DeploymentResponse`) uses `omitempty` so the wire
shape is unchanged for customers who don't care.

The column makes the failure mode durable across the dashboard's
group-by queries, the audit logs, and ad-hoc psql analyses. The
imaged handler writes the code at the moment of `DeployFailed`
transition; thereafter the row is read-only.

A new `Store` method `SetDeploymentFailed(ctx, id, code, message)`
updates both columns in one transaction. The existing `Mark*`
methods that only set `status` are not modified.

### D3. `OCIPullTimeoutSeconds` is the only knob

`pkg/api/limits.go::OCIPullTimeoutSeconds = 60` (60 seconds; the
spec's plan for `BuildTimeoutSeconds` is 600, so 60 is comfortably
under it and leaves headroom for `PullImageConfig` + `PullLayers`
to complete).

`pkg/oci/registry.go::NewRegistryClient` defaults the new
`http.Client.Timeout` to this constant. A new option `oci.WithTimeout`
overrides it per-daemon:

```go
puller := oci.NewRegistryClient(
    oci.WithHTTPClient(oci.NewEgressHTTPClient()),
    oci.WithTimeout(timeoutSec * time.Second),
)
```

`cmd/imaged/main.go` reads `FAAS_OCI_PULL_TIMEOUT_SECONDS` (int)
and falls back to `api.OCIPullTimeoutSeconds` if unset or unparseable.
Per-deployment and per-account overrides are explicitly rejected —
the design rationale is that every other timeout in the codebase
sits at the platform level, and there is no plan dimension that
varies the image-pull ceiling. A future per-app or per-account
override can be added in a follow-up ADR if a customer need
emerges.

### D4. Operator override gate; in-source defaults unchanged

`cmd/imaged/main.go` lines 141-142 (the `FAAS_BUILDER_BASE_REF` and
`FAAS_DEPLOY_BASE_REF` env reads) become:

```go
if v := os.Getenv("FAAS_BUILDER_BASE_REF"); v != "" {
    ref, err := oci.ParseReference(v)
    if err != nil || ref.Digest == "" {
        return fmt.Errorf("imaged: FAAS_BUILDER_BASE_REF %q must be a digest-pinned reference (e.g. registry.DOMAIN/img@sha256:...)", v)
    }
    h.WithDeployBaseRef(v)
}
```

Same for `FAAS_DEPLOY_BASE_REF`.

`pkg/imaged/base.go::BaseRefNode22` / `BaseRefPython312` /
`BaseRefMinimal` / `BaseRefBuilder` stay as `…:latest` — the
operator-facing override is the gate, not the source constant. A
base-image rebuild is an operator-initiated, Makefile-gated
workflow; the digest lives in the operator's pin file (separate
concern), and pinning the constant in source would force a code
change per rebuild for no real safety gain.

## Why these decisions (per D)

### Why D1 over a single `CodeImagePull`

Three failure modes, three codes. A single code means a customer
cannot script their CI to retry only the recoverable subset (a
manifest-list is recoverable by re-pinning a single-arch
sub-manifest; a 404 is not until they push the image). The
granularity is at the boundary of what a customer needs to act on;
splitting further (per-status-code, per-content-type) buys nothing
because the spec's intended customer behavior is "fix the image or
the policy".

### Why D2 (column) over D2-bare (no column)

Spec §11's audit rule requires "all security-relevant decisions
must be observable in pg without parsing free text." The 403-class
egress denials are security-relevant. Keeping the code in PG
satisfies the audit rule and the M7.5 dashboard's group-by
requirement with one column add.

### Why D3 over per-deployment timeout

There is no plan dimension that varies the image-pull ceiling. The
spec puts every other timeout at the platform level. The 30 s
hardcode was a magic number; promoting it to a constant is the only
change needed. Per-deployment knobs are deferred to a future ADR if
a customer need emerges.

### Why D4 over pinning source base refs

Pinning `BaseRefNode22` etc. to specific `@sha256:…` digests would
force a code change per builder-base rebuild. The base image
rebuild is operator-initiated today; the digest value lives outside
this codebase (in the registry layer). The override gate is the
load-bearing change — it stops an operator from accidentally
bypassing the G1 policy — without coupling rebuilds to code
review.

## Rejected alternatives

- **Per-deployment OCI timeout.** No plan dimension supports it;
  spec policy is platform-level ceilings. Adds a knob we don't have
  a use for.
- **`CodeImagePullTimeout` separate from `OCIPullTimeoutSeconds`.**
  The timeout knob is for the daemon, not the deployment; the
  failure mode is identical at the sentinel layer (the http client
  returns its own context-deadline-exceeded, not one of the three
  puller-side sentinels). Existing free-text is enough.
- **Pin source base refs to digests.** See D4. Couples rebuilds to
  code review without closing the override loophole an operator
  could still open via `FAAS_BUILDER_BASE_REF=:latest`. The
  override gate is sufficient and proportional.
- **Surfacing codes only at the API boundary (no D2 column).**
  Spec §11 requires the security-relevant decision be observable in
  PG without parsing. The 403-class egress denials are security-
  relevant; no-column would force audits to grep free text.
- **`CodeImageLayerTooBig` etc.** Already covered by `CodeAppLayerTooBig`
  at the apid/quota boundary; not a puller-side signal.

## Implications

- `pkg/api.errors` gains three codes + the `SentinelToCode` map;
  `StatusForCode` grows three lines.
- `pkg/api/limits.go` gains one constant. Overridable via env, no
  per-deployment knob.
- The spec's "platform-level ceilings, not magic numbers" rule is
  no longer violated by `pkg/oci/registry.go:63`.
- imaged's M9 daemon-loop changes are minimal — the codes flow
  through `pkg/imaged/handler.go::buildImageLayer`; no imaged-side
  change touches the loop, GC, or scheduler.
- The plan that this PR pairs with must also wire vmmd's host-key
  lifecycle (`pkg/fcvm/manager.go::SetHostIdentity`). That is the
  G2 ship-blocker that PR #73 missed — see the companion PR's
  context, not this ADR's.

## Follow-up

- Per-deployment timeout (when a customer need emerges).
- Dashboard widget for `error_code` histogram (M7.5 §6.7).
- `oci.HealthCheck` (`HEAD /v2/`) so customers learn about egress
  denials pre-flight, not in the wake path.
- Function-deploy RFC 7807 codes (build OOM, source validation).
  Out of scope here; covers the imaged image-deploy path only.
- Base-ref rebuild workflow warnings: when on-disk drive0's manifest
  digest doesn't match the operator's pinned digest, imaged emits a
  structured warning. Tracked as future work; the policy fix in D4
  ships in this PR, the operator workflow lands later.
