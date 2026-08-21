# ADR-052 · Control-plane mTLS for multi-box + handler-layer peer binding

- **Status:** accepted v1.0
- **Superseded (in part, PR-E):** prose referred to the monolithic
  `cmd/gatewayd/` daemon split by ADR-070 into `gatewayd-public` (TLS-only
  edge) and `gatewayd-internal` (routing + wake + proxy). Body is preserved
  verbatim; readers should substitute "gatewayd-internal" for the
  routing/wake/proxy path and "gatewayd-public" for the certmagic/TLS path.
  `cmd/gatewayd/<file>.go` citations in this body are stale; see PR-E for
  the new file locations.
- **Date:** 2026-07-30 (proposed)
- **Accepted:** 2026-08-09
- **Issue:** #95 slice 2 (multi-box rollout)
- **Decision:** Ship cross-host mTLS as the second slice of issue #95.
  Slice 1 (already landed on `main`) gave us `pkg/wire.Dial` / `wire.Listen`
  with a strict stdlib verifier (chain trust against the operator's `RootCAs`,
  RFC 6125 SAN matching, EKU enforcement, in a single handshake pass via
  `verifyServerCertificate`). This ADR commits to:

  1. **Per-daemon SANs.** Every leaf certificate carries a daemon-specific SAN
     (`schedd.faas`, `vmmd.faas`, `builderd.faas`, `gatewayd.faas`,
     `apid.faas`, `meterd.faas`, `githubd.faas`). Local-dev leaves also carry
     `127.0.0.1` / `::1` / `localhost` so single-box tests stay correct.
     The stdlib verifier performs SAN matching on every dial — a daemon
     dialling the wrong peer fails closed at the handshake.

  2. **Operator-facing `gregalectl pki` subcommand.** `cmd/gregalectl/commands_pki.go`
     follows the existing `sign-keys init|rotate|status` pattern. It generates
     an ECDSA P-256 CA + one leaf per daemon under `/etc/faas/tls/{ca,<daemon>}/`,
     enforced at `0400` (private) / `0444` (public) by mirroring the
     `pkg/cosign.LoadPrivateKeyFile` / `WriteKeyPairForGroup` semantics. The
     binary refuses to start a daemon whose cert/key/CA are missing, partial,
     or wrong-mode (reuses `pkg/cosign.ErrInsecurePrivKeyPerms` /
     `ErrInsecurePubKeyPerms`).

  3. **Threading TLS through every dial/listen site** that today hard-codes
     `nil` TLS. See "Dial/listen sites" below for the full inventory. The
     single new dependency is `wire.LoadClientTLSConfig*` /
     `wire.LoadServerTLSConfig*` — already provided by slice 1.

  4. **Handler-layer peer binding (instead of a custom
     `VerifyPeerCertificate` hook).** The original draft of this slice
     proposed a custom `VerifyPeerCertificate` hook that bound the leaf CN
     to a `compute_node.id` registry inside the TLS handshake. That design
     was rejected (see "Rejected alternatives") because it would either
     duplicate the stdlib verifier (DRY violation) or weaken it (race vs.
     CodeQL alert #58). Instead, gRPC services that care which peer they're
     talking to inspect the peer certificate **after** the handshake via
     `peer.FromContext` / `credentials.TLSInfo`, and refuse requests whose
     CN does not match the registered `compute_node.id`. This keeps the
     crypto story entirely in `pkg/wire` and moves identity-to-resource
     binding to the layer that already owns the registry.

- **Why:** ADR-025 commits to location-transparent gRPC and explicitly says
  "Services MUST enforce certificate verification via mutual TLS (mTLS) to
  prevent unauthorized control-plane calls." Slice 1 shipped the dial/listen
  surface; slice 2 is the production-grade dev PKI + the operator workflow
  + the missing config + the actual TLS threading. Without slice 2, the
  existing `wire.Dial` helpers refuse TCP/DNS targets outright
  (`pkg/wire/grpc.go:188`), so multi-box schedd↔vmmd has no working path
  at all — the helpers are correct but unusable until certs are issued and
  threaded through every config.

- **Consequences:**

  - **Strict verification everywhere.** No `InsecureSkipVerify`, no
    `grpc.WithInsecure()` for non-unix targets, no `nil` TLS on the
    production code paths. Single-box deployments stay on `unix://`
    sockets (ADR-015 socket-mode 0660 + group `faas`); TLS-over-UNIX
    becomes available as an opt-in if an operator sets the three path
    fields (slice 1 already supports this).
  - **Operator workflow.** A new compute node is brought up by:
    1. `ansible-playbook` lays down `/etc/faas/tls/ca/` from a vaulted
       source.
    2. `gregalectl pki init` issues per-daemon leaves for that box.
    3. Daemons start and refuse to come up if any expected cert is missing.
  - **Test surface.** Existing `pkg/wire/grpc_test.go` PKI helper
    (`newTestPKI`, lines 644-691) is reused for new round-trip tests in
    service packages. The new `cmd/e2e/mtls_e2e_test.go` exercises real
    TCP on `127.0.0.1` (single-box, but the only test that proves the
    cert path end-to-end — bufconn hides TLS bugs that show up only on a
    real listener).
  - **No schema change.** No new Postgres tables in this slice. Phase 2
    (`node_signature` on `CapacityReport`, ADR-TBD) introduces
    `compute_node_keys` for the per-node signing key registry.

## Dial/listen sites threaded by this slice

**Listen (server-side, add TLS):**
- `cmd/apid/main.go::runAdvisoryServer` — currently raw
  `wire.ListenOrRecreateByName`; switch to `wire.Listen` with server TLS so
  the vmmd→apid advisory RPC (ADR-047 PR-C) can be mTLS-over-UNIX when
  the operator opts in.
- `cmd/gatewayd/egress_grpc.go::newEgressGRPCListener` — currently raw
  `net.Listen("unix", ...)`. Switch to `wire.Listen` so the meterd→gatewayd
  egress RPC can carry TLS.

**Dial (client-side, replace `nil` TLS with `cfg.LoadClientTLSConfig(...)`):**
- `pkg/vmmdgrpc/advisory_client.go:149` (vmmd → apid advisory).
- `cmd/vmmd/capacity_publisher.go:249` (vmmd → schedd capacity reports).
- `cmd/meterd/main.go:419-431` (meterd → gatewayd egress stream — raw
  `grpc.NewClient("unix:///"+socketPath, grpc.WithInsecure())`, the only
  raw dial in the tree today; migrate to `wire.DialContext`).
- `cmd/meterd/main.go:604` (meterd → schedd; the `dialSchedd` dep is
  wired with TLS plumbing but the call site passes `nil`).
- `cmd/gatewayd/main.go:253` (gatewayd → schedd).
- `cmd/apid/main.go:349` (apid → githubd).

## Config additions

Per-daemon `*.toml.example` files gain three new fields per remote role
(PR-1: the v1 deploy/etc/*.toml.example fixtures are a tombstone now;
canonical is deploy/ansible/roles/*/files/*.toml.example):

```toml
# vmmd.toml — new client cluster for schedd + apid-advisory
[schedd_client]
target             = "unix:///run/faas/schedd.sock"  # or tcp://...
tls_cert_path      = "/etc/faas/tls/vmmd/schedd-client.crt"
tls_key_path       = "/etc/faas/tls/vmmd/schedd-client.key"
tls_ca_path        = "/etc/faas/tls/ca/ca.crt"

[apid_advisory_client]
target             = "unix:///run/faas/apid.sock"    # or tcp://...
tls_cert_path      = "/etc/faas/tls/vmmd/apid-client.crt"
tls_key_path       = "/etc/faas/tls/vmmd/apid-client.key"
tls_ca_path        = "/etc/faas/tls/ca/ca.crt"
```

```toml
# meterd.toml — new client clusters for schedd + gatewayd
[schedd_client]
target             = "unix:///run/faas/schedd.sock"
tls_cert_path      = "/etc/faas/tls/meterd/schedd-client.crt"
tls_key_path       = "/etc/faas/tls/meterd/schedd-client.key"
tls_ca_path        = "/etc/faas/tls/ca/ca.crt"

[gatewayd_egress_client]
target             = "unix:///run/faas/gatewayd-egress.sock"
tls_cert_path      = "/etc/faas/tls/meterd/gatewayd-egress-client.crt"
tls_key_path       = "/etc/faas/tls/meterd/gatewayd-egress-client.key"
tls_ca_path        = "/etc/faas/tls/ca/ca.crt"
```

```toml
# gatewayd.toml — new server cluster for the egress gRPC listener
listen_egress_addr = "unix:///run/faas/gatewayd-egress.sock"
tls_cert_path      = "/etc/faas/tls/gatewayd/egress.crt"
tls_key_path       = "/etc/faas/tls/gatewayd/egress.key"
tls_ca_path        = "/etc/faas/tls/ca/ca.crt"
```

```toml
# apid.toml — new server cluster for the advisory listener
advisory_listen_addr = "unix:///run/faas/apid.sock"
advisory_tls_cert_path = "/etc/faas/tls/apid/advisory.crt"
advisory_tls_key_path  = "/etc/faas/tls/apid/advisory.key"
advisory_tls_ca_path   = "/etc/faas/tls/ca/ca.crt"
```

```toml
# apid.toml — githubd client cluster
[githubd_client]
target        = "unix:///run/faas/githubd.sock"
tls_cert_path = "/etc/faas/tls/apid/githubd-client.crt"
tls_key_path  = "/etc/faas/tls/apid/githubd-client.key"
tls_ca_path   = "/etc/faas/tls/ca/ca.crt"
```

## Handler-layer peer binding (replaces custom hook)

Each gRPC service that cares about peer identity runs the same pattern:

```go
import (
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/peer"
)

func peerCN(ctx context.Context) (string, error) {
    p, ok := peer.FromContext(ctx)
    if !ok { return "", errors.New("no peer") }
    tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
    if !ok { return "", errors.New("peer not on TLS") }
    if len(tlsInfo.State.VerifiedChains) == 0 { return "", errors.New("no verified chain") }
    return tlsInfo.State.VerifiedChains[0][0].Subject.CommonName, nil
}
```

The first slice of services that adopts this is `pkg/scheddgrpc::ReportCapacity`
in the next ADR (Phase 2). For this slice we ship the helper in
`pkg/wire/peer.go` so it lives next to the verification code, and we use it
in two new test paths (`pkg/wire/grpc_test.go::TestMTLSRoundTripPeerCN`,
`cmd/e2e/mtls_e2e_test.go`) to prove the binding works without depending
on a future slice.

## Operator CLI (`gregalectl pki`)

```
gregalectl pki init      # generate CA + leaves for every daemon on this box
gregalectl pki status    # print per-leaf: serial, expires_at, CN, SANs, mode
gregalectl pki rotate    # re-issue leaves whose NotAfter is < 30d away
gregalectl pki revoke    # write a CRL entry (out of scope for slice 2;
                         # placeholder for the post-1.0 PKI replacement)
```

`init` is idempotent: leaves whose NotAfter is ≥ 30 days from `now` are
skipped (no re-issue churn). `init` is **destructive on the CA key**: if
`/etc/faas/tls/ca/ca.key` exists with a fresh CA cert, `init` reuses it
unless `--rotate-ca` is passed. (Operator mental model: CA is the trust
root, leaves are cheap.)

## File layout on disk

```
/etc/faas/tls/
├── ca/
│   ├── ca.crt    (0444 root:root)        — root pool for every daemon
│   └── ca.key    (0400 root:root)        — only `gregalectl pki` reads this
├── schedd/
│   ├── server.crt (0444 root:root)       — schedd's server leaf
│   └── server.key (0400 root:root)
├── vmmd/
│   ├── server.crt                       — vmmd's server leaf
│   ├── server.key
│   ├── schedd-client.crt                — leaf vmmd uses to dial schedd
│   ├── schedd-client.key
│   ├── apid-client.crt                  — leaf vmmd uses to dial apid
│   └── apid-client.key
├── builderd/                            — server leaf only (dial targets vmmd; uses schedd's vmmd-client material if collocated)
├── gatewayd/                            — server leaf for egress, client leaf for schedd + vmmd
├── apid/                                — server leaf for advisory, client leaf for githubd
├── meterd/                              — client leaves for schedd + gatewayd
└── githubd/                             — server leaf only
```

Per-daemon names follow the rule: `server.{crt,key}` for the daemon's
listener; `<remote-role>-client.{crt,key}` for the daemon's outbound
dial to a remote role.

## Out of scope (later slices)

- **CRL / OCSP.** `gregalectl pki revoke` is a stub that prints a TODO. A
  real CRL/OCSP story requires either an internal step-ca or external
  ACME; defer until either becomes a hard requirement (likely post-1.0).
- **Per-node `node_signature` on `CapacityReport`** (Phase 2 in the
  implementation plan). This ADR ships the CA + leaves + dial/listen
  plumbing; the signing key + handler-layer CN-binding for capacity
  reports is its own ADR.
- **TLS-over-UNIX enforcement.** Today a daemon can dial `unix://` with
  `nil` TLS and succeed. Slice 1 made this configurable; this slice
  doesn't add an "mTLS required for unix too" flag — that's a follow-up
  once an operator wants defense-in-depth on the local socket path.
- **ANSM SPIFFE / workload identity.** Out of scope.

## Rejected alternatives

- **Custom `VerifyPeerCertificate` hook for CN→`compute_node.id` binding.**
  The first draft of this slice proposed installing a per-dial
  `tls.Config.VerifyPeerCertificate` hook that looked up the peer CN in
  a Postgres registry and refused non-matches. Two reasons it was rejected:

  1. The stdlib verifier (chain/SAN/EKU) already runs *before* the hook
     in `verifyServerCertificate`. Adding a hook that re-implements
     chain/SAN/EKU is a strict DRY violation; adding one that does only
     the CN lookup would still duplicate the verifier's parse work.
  2. The first iteration of `loadClientTLSConfig` did exactly this and
     was rejected by CodeQL alert #58 for the literal
     `InsecureSkipVerify=true` plus the custom chain check, regardless
     of source-comment rationale (ADR-025 §Rejected alternatives).
     Keeping the hook would require re-litigating that decision.

  The handler-layer binding reads `peer.FromContext` after the handshake
  has already passed chain/SAN/EKU, so it's strictly stronger (the
  handshake can't be tricked into accepting a wrong peer) and strictly
  smaller (one ~10-line helper vs. a custom verifier).

- **External CA (step-ca, ACME, cert-manager).** Cleanest operationally
  but requires standing up infra that doesn't exist today. PR-B / post-1.0.
  A future slice that wires `cmd/gregalectl pki init --external-ca` against
  an ACME server is the natural extension; this slice keeps the door open
  by making the `init` flow idempotent and the file layout CA-agnostic.

- **Drop slice 1's stdlib verifier for an in-house verifier.** No.
  `crypto/tls` is the most audited TLS stack in the world; replacing it
  with our own verifier is the worst possible use of engineering time.

## Risks

- **Per-daemon SAN drift.** If an operator issues a leaf with the wrong
  SAN (e.g. `vmmd.faas` on a schedd cert), the handshake silently fails
  with `tls: handshake failure` and the dial log is uninformative.
  Mitigation: `gregalectl pki status` prints every leaf's CN + SAN list +
  mode, and the daemon refuses to start if its loaded leaf's CN does
  not match its expected role. `cmd/e2e/mtls_e2e_test.go` exercises the
  CN-mismatch path explicitly.
- **CA rotation pain.** `init` reuses an existing CA key unless
  `--rotate-ca` is passed. Rotating the CA is a multi-host, simultaneous
  operation; if even one box is left behind, every gRPC leg from that
  box fails. Mitigation: `gregalectl pki status` exits non-zero if any leaf
  expires within 30 days, and the operator runbook calls for rotating
  per-leaf first (cheap, no CA change), then scheduling a CA-rotation
  window. There is no automated CA rotation in this slice.
- **New dial site misses the threading.** Today's raw `grpc.NewClient` is
  only one site (`cmd/meterd/main.go:419`); this slice migrates it. A
  future contributor adding a new raw dial is not protected by an
  architecture test in this slice. Mitigation: a follow-up adds a
  `grep -r "grpc.NewClient\\|grpc.Dial(" --include="*.go" cmd/ pkg/`
  pre-commit check; deferred to keep this PR small.

## Reference call sites

| Site | Change |
|------|--------|
| `cmd/gregalectl/commands_pki.go` (new) | `gregalectl pki init\|status\|rotate` |
| `cmd/gregalectl/main.go` | register `pki` subcommand |
| `pkg/wire/peer.go` (new) | `PeerCN(ctx) (string, error)` helper |
| `pkg/wire/grpc_test.go` | new `TestMTLSRoundTripPeerCN` |
| `cmd/e2e/mtls_e2e_test.go` (new) | real-TCP `127.0.0.1` round trip |
| `cmd/vmmd/config.go` | new `ScheddTLSCfg` + `AdvisoryTLSCfg` |
| `cmd/vmmd/main.go` | thread TLS into capacity publisher + advisory client |
| `cmd/meterd/config.go` | new `ScheddTLSCfg` + `GatewayEgressTLSCfg` |
| `cmd/meterd/main.go` | migrate raw dial + thread TLS into `dialSchedd` |
| `cmd/gatewayd/config.go` | new egress `ServerTLSCfg` |
| `cmd/gatewayd/main.go` | thread TLS into schedd dial + new egress listener config |
| `cmd/gatewayd/egress_grpc.go` | switch to `wire.Listen` |
| `cmd/apid/config.go` | new `AdvisoryTLSCfg` + `GithubdTLSCfg` |
| `cmd/apid/main.go` | thread TLS into advisory listener + githubd client |
| `cmd/apid/githubd_client.go` | accept TLS config from caller |
| `deploy/ansible/roles/*/files/*.toml.example` | new fields in 5 files (PR-1: canonical TOML fixtures) |
| `deploy/ansible/roles/control_plane_service/tasks/main.yml` | new stat-assert task for `/etc/faas/tls/` |
| `Makefile` | no change — CLI rides on `gregale` binary |
| `docs/adr/025-decoupled-control-plane-and-compute.md` | update §1 to remove stale `credentials.NewTLS(creds)` snippet, reference `wire.LoadClientTLSConfig*` |

## Acceptance

- `make test` — all wire + per-daemon config + new mTLS tests pass.
- `make lint` — golangci-lint clean.
- `make spec-check` — ADR-052 cross-linked from `docs/adr/025-decoupled-control-plane-and-composite.md`.
- Live: stand up a two-VM fleet (default-local + compute-01), `gregalectl pki init` on each, deploy an app, observe schedd↔vmmd / gatewayd↔vmmd / meterd↔schedd all running over mTLS with no plaintext on the wire.

## Amendment 1 — Cert fingerprint collision guard (multi-host safety cluster PR-4 / audit F6, 2026-08-22)

### Context

The mTLS handshake verifies that the peer presents a cert signed by
the operator's CA (the trust anchor). It does NOT verify that the
cert the peer presents today is the SAME cert it presented yesterday
— a leaked leaf can be replaced under the same CA without the
handshake noticing. The PR-3a release-bundle carrier (migration
00271) added `compute_nodes.host_certificate` and
`compute_nodes.cert_fingerprint` as a column-level public-key-pinning
attestation; PR-3 release-install stamps the fingerprint on
`POST /v1/compute-nodes`; PR-3 secrets-init (PR-X) re-stamps on
rotation. PR-4 closes the audit F6 gap where vmmd's startup UPSERT
silently preserved the existing row's fingerprint via COALESCE —
fine for the cold-INSERT case, but a fingerprint mismatch (i.e. the
local leaf was rotated under the operator's nose) was tolerated
silently.

### Decision

Two layers:

1. **App-level (primary):** `pkg/state/pgstore.go::UpsertComputeNodeFromVmmd`
   runs a pre-flight SELECT on the existing row's cert_fingerprint
   before the upsert. If both old and new fingerprints are non-null
   AND differ, the upsert refuses with `state.ErrCertFingerprintDrift`
   (also wired into `pkg/state/memstore.go` for in-memory tests).
   The wrapped error message **MUST** include BOTH the OLD and NEW
   fingerprints so the operator can grep the log line and run the
   right reconcile command. `cmd/vmmd/register.go` calls
   `pkg/pki.LoadCertificateFingerprint` on `/etc/faas/tls/vmmd/server.crt`
   and stamps the result on the upsert.

2. **DB-level (belt-and-braces):** migration 00347 adds a UNIQUE
   partial index `compute_nodes_active_unique_idx` on
   `(name) WHERE active = true`. If a future code path skips the
   pre-flight check and inserts a second active row with the same
   name, Postgres raises 23505 at INSERT time rather than letting
   the silent-COLLISION complete.

The pre-flight SELECT lives on `*PgStore` (not the `Store`
interface) because it's an internal implementation detail of the
vmmd-side upsert path. The error sentinel
`state.ErrCertFingerprintDrift` is on the interface so callers can
match it with `errors.Is`.

### Helper

`pkg/pki.LoadCertificateFingerprint(certPath string) (string, error)`
reads the PEM leaf, parses it, and returns
`"sha256:" + hex(SHA-256(DER))` — the same fingerprint format
`openssl x509 -fingerprint -sha256` prints. The helper enforces the
project-wide file-mode policy (cert 0o444, key 0o400) via
`enforceCertMode`, mirroring `cosign.LoadPublicKeyFile`. Mode
violations return `ErrInsecurePubKeyPerms` BEFORE the parse, so an
attacker can't use a writable cert file to bypass the certificate
load.

### Failure mode contract

The error string format is locked:

```
state: compute_node cert fingerprint drift: node "X" existing fingerprint "OLD" differs from local leaf "NEW" — reconcile via `gregale pki reconcile X`
```

This shape is pinned by `cmd/vmmd/register_test.go::TestRegisterComputeNode_RefusesFingerprintDrift`
which asserts both fingerprints appear in the error message. A
future refactor that changes the wording breaks the test; the test
name is the contract.

### Rejected alternatives

- **Hash the public key (SPKI) instead of the DER body.** Matches
  public-key-pinning semantics, but a re-issued leaf under the same
  key would have the same fingerprint — exactly the case the guard
  is meant to detect. Hashing the DER body means any rotation
  (including a forced rotation due to a leak) produces a new
  fingerprint, which is the correct tripwire.
- **Compare at the gateway-internal verifier (mTLS handshake).**
  Defers the check to the wire, but a leaked cert that the operator
  hasn't rotated yet would still be trusted until the operator
  manually `pki reconcile`s. Catching the drift at registration
  time means the operator is alerted on the FIRST boot of the new
  box, not the first inbound mTLS connection — better signal
  latency.
- **Status:** accepted (PR-4 / multi-host safety cluster, audit F6).

