# ADR-623: Authenticated external fence receipt delivery

Status: accepted · 2026-10-07

## Context

ADR-622 defines immutable provider-independent signed termination receipts and
locked database acceptance. It deliberately has no production issuer. Before
choosing an enforcement provider, add a usable private transport adapter for
retrieving an authority's existing signed bytes. Delivery must not manufacture
evidence, silently redirect administrative attribution, or refresh old claims.
The selected host/service/activation observations in ADR-620–621 still cannot
qualify an irreversible authority or clear a historical withdrawal.

## Decision

Add `runtimefence.ReceiptClient`, constructed only from a private administrative
`ReceiptClientConfig`. Its single operation, `Lookup(ctx, intent)`, retrieves an
existing receipt. It has no enforcement, signing, issuer enrollment, host or
network mutation operation. No CLI, API, daemon, polling worker, database schema
or production wiring is added. Existing private flags remain default off and
public `execution_available=false` stays unchanged.

Review an exact canonical HTTPS endpoint whose path is
`/v1/runtime-upgrade/fence-receipts:lookup`, an authority UUID and Ed25519 key,
explicit server CA roots, the SHA-256 of the server leaf certificate's DER SPKI,
and a client certificate/private key. Require bounded PEM inputs and parse them
into owned credentials rather than retaining caller-owned TLS configurations,
certificate pools, private-key slices or HTTP clients. Credentials come from
trusted private wiring; the adapter does not load credentials from disk, select
environment credentials, provide a secret store or establish issuer key custody.
The TLS client key is distinct from the authority's receipt-signing key.

Each lookup makes a fresh direct TLS 1.3 connection. Normal certificate chain,
hostname and validity checks precede the additional leaf SPKI pin check. The
pin cannot override an invalid certificate. The TLS peer must request a
compatible client certificate before any intent is sent. There is no TLS
resumption cache, pooled keepalive connection, environment proxy, cookie jar,
redirect following or compression. Use HTTP/1 only and disable transport replay;
all retries are explicit caller operations against the same immutable intent.
Client-authentication requests establish that credentials were requested, not
independent proof of the authority's server-side authorization policy.

The wire request is `POST` to that exact route with the complete fixed-order
`encoding/json.Marshal(Intent)` bytes. The content type is
`application/vnd.gregale.external-fence-intent.v1+json`; Accept is
`application/vnd.gregale.external-fence.v1+json`; Cache-Control is `no-store`.
The protocol is **lookup only**: a conforming authority must return its existing
immutable receipt or pending status, without initiating termination, signing a
new receipt, refreshing issuance, or changing a challenge in response. This
client cannot independently establish that a server obeys that contract.

Only HTTP 200 with exactly one receipt media-type header, no content encoding,
no trailers, and a complete bounded body can produce a `ReceiptDelivery`.
Chunked bodies are allowed under the same byte bound; truncated, oversized,
compressed or noncanonical data is rejected. HTTP 202 and 404 return
`ErrReceiptPending` with zero evidence. Redirects and every other status return
`ErrDelivery` with zero evidence. Error messages omit URLs, credentials, TLS
subjects and arbitrary peer text; cancellation and deadline causes remain
available through `errors.Is`.

Centralize delivery budgets in `pkg/api/limits.go`: PEM input ≤64 KiB each,
endpoint ≤2048 bytes, response headers ≤16 KiB, each lookup ≤10 seconds including
connection queuing/handshake/headers/body, and ≤4 concurrent connections per
client. The existing receipt body limit remains 16 KiB. DNS label/hostname and
TCP port bounds are protocol syntax, not configurable application quotas.

Split the existing verifier internally into authentic signed intent binding and
first-acceptance time checks. Delivery performs canonical encoding, signature,
contract, identity, challenge, complete intent digest and enforcement/issuance
ordering checks. It returns only an opaque `ReceiptDelivery` with a copying
`Envelope()` method, never a `VerifiedReceipt` or database acceptance result.
It does not apply a local wall clock to claim age. This permits original signed
historical bytes to be delivered again after restart, revocation or elapsed time
without refreshing any field. Future-dated authenticated bytes likewise gain no
acceptance from delivery; the locked database clock rejects premature admission.

The private caller obtains the original database-reviewed intent, calls Lookup,
and passes its exact Envelope bytes to
`RecordRuntimeUpgradeExternalFenceReceipt`. That existing store owns head/key/
revocation checks and samples database time after lock waits. A new receipt still
requires the original 60-second age window; an already accepted exact-byte retry
still returns its original permanent receipt. A stale first delivery remains
pending even if termination was real: recovery cannot re-sign the old event or
backdate observation to bypass ADR-622. Administrative remediation requires a
separately reviewed operation satisfying the original contract.

## Consequences and limits

This adds a concrete authenticated delivery adapter without selecting a cloud
provider or claiming that any provider has been qualified. It does not prove
the truth of a signed assertion, historical startup-to-host/resource attribution,
complete scope, issuer signing-key custody, server authorization, enforcement
completion before signing, or irreversible elimination of all execution/resume
paths. Enrollment remains a trusted administrative action; constructing a client
does not enroll or trust its signing key in PostgreSQL. Database acceptance still
uses the store's own enrolled key, independent of this transport configuration.

The next authority-specific slice must establish those missing facts and test
crash/restart, lost-response delivery and failure handling against the actual
provider. Temporary power state, network isolation, masks or a signer of
operator-supplied booleans remain ineligible. No VM quiescence, routing cutover,
runtime acceptance or retirement/deletion authority follows from a delivery.
Native Caddy/DNS/systemd acceptance, Linux amd64 KVM `test-metal` and final
`leakcheck` remain prerequisites for larger runtime-upgrade enablement.

## Validation and adversarial review

Portable tests use real CA-signed TLS 1.3 client/server credentials and actual
HTTPS sockets, with receipt signing only in test fixtures. Exercise canonical
intent transmission, copied credentials/returned bytes, hostname/expiry/CA and
SPKI rejection, rotated server keys, absent client authentication, rejected or
expired client certificates, TLS 1.2 rejection, response framing and byte/header
bounds, trailers, foreign signatures/intents/contracts, pending/error statuses,
all redirect families, partial-response failure, explicit caller retry after
restart, historical/future delivery versus first acceptance, concurrent fresh
handshakes, cancellation, bounded connection queuing and queued deadlines.
Keep all ADR-622 verifier tests unchanged as the acceptance regression suite.
Require normal and race checks, Linux amd64 compilation, focused pinned lint,
repository policy gates and an adversarial review before committing locally.

Local validation passed the same 19 top-level tests normally and with race
detection, with no failures or skips: 12 delivery tests plus all seven unchanged
ADR-622 verifier tests. Linux amd64 test-binary compilation passed; pinned
golangci-lint v2.4.0 on Go 1.25.13, including tests under the Linux target,
reported zero issues. Runbook SQL, text encoding, shell quoting and ADR number
policy gates passed. Database schema, SQLC output and store implementation did
not change; real PostgreSQL acceptance remains covered by ADR-622's previous
38-test database validation. This slice did not rerun PostgreSQL or native
provider/KVM enforcement acceptance. No PR, push or deployment was performed.

Adversarial pass: a trusted CA without the reviewed server pin cannot substitute
an authority; a pin cannot bless expired or wrong-host certificates; a peer that
does not request client identity receives no intent. HTTP success, a pending
body or a partial signed body cannot resolve a withdrawal. A caller cannot turn
historical delivery into fresh admission or alter retained keys/bytes. An
unavailable authority, ambiguous framing, TLS failure or cancellation returns
zero evidence. No implicit retry can conceal a disconnect or mint a new receipt.
Transport authenticity remains separate from actual authority qualification.

Primary references:

- [Go TLS verification and client certificates](https://pkg.go.dev/crypto/tls)
- [Go HTTP transport bounds, protocols and retry behavior](https://pkg.go.dev/net/http)
- [Go X.509 certificate verification](https://pkg.go.dev/crypto/x509)
- [ADR-622 signed receipt and acceptance contract](622-provider-independent-irreversible-fencing-receipts.md)
