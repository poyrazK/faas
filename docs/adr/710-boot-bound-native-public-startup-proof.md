# ADR-710: Boot-bound native public startup proof

Status: accepted · 2026-10-07

## Context

ADR-708 defines provider-independent irreversible host-epoch termination receipts;
ADR-709 adds authenticated receipt delivery. Neither qualifies a real enforcement
authority. The selected target is Gregale's native bare-metal deployment, with no
out-of-band control configured yet. An unreachable Linux host cannot supply fresh
termination evidence through its own daemon. A reset acknowledgement, temporary
power state, systemd mask or operator-supplied boolean is insufficient to attest
irreversible elimination of the old epoch and every execution/resume path.

Before selecting hardware control, bind the public gateway's actual startup
session and configuration to its kernel-visible boot/process context. ADR-702's
proof authenticates only slot, session and configuration; ADR-706–621's selected
native inventories do not cryptographically join those fields to that context.

## Decision

Add a separate private native-startup protocol in `pkg/gateway/ingress`. Its
`NativePublicStartup` contains canonical public slot/session/configuration plus
machine ID, boot ID, PID, process start ticks, PID namespace and network namespace.
The public constructor accepts the existing startup identity and private ingress
token, then captures its own Linux identity once. It accepts no caller-supplied
epoch, PID, filesystem root or hardware resource ID. The production gateway
passes its actual generated session and selected configuration fingerprint.

Capture two equal snapshots from fixed Linux sources: retained `/proc` root,
`/etc/machine-id`, `sys/kernel/random/boot_id`, `self/stat`, and self PID and
network namespace links. Check procfs filesystem type; require protected
root-owned `/` and `/etc`; open the machine ID without following its final
symlink or blocking on a special file. Require a root-owned regular machine-ID
file without group/other write permissions and stable retained-fd metadata
before/after reading. Bound metadata reads with ADR-706's centralized 64 KiB
budget. No arbitrary filesystem fallback or discovery is provided.

Require canonical nonzero machine/boot IDs, a positive signed-32-bit PID other
than PID 1, nonzero canonical start ticks and namespace inode numbers. Parse
`/proc/self/stat` using its matched PID and final comm delimiter, accounting for
spaces, closing parentheses and newlines in comm; use field 22 for process start
time. Read only self namespace links: the deployed gateway runs as `faas` with
`ProtectProc=invisible`, and other-process namespace links have
[ptrace permission checks](https://man7.org/linux/man-pages/man7/namespaces.7.html).
Do not grant extra capabilities or weaken service hardening. These are identifiers
within that procfs view, **not attestation of an initial host namespace, a
container-free environment or physical hardware**. Matching them to an independently
reviewed native host belongs to a separate privileged inventory observation.
Malformed, missing, changed or inaccessible metadata and cancellation return no
startup identity. Other platforms fail closed without an invented Linux epoch.

Install the endpoint on the actual public listener only when
`FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_NATIVE_IDENTITY=1`. This default-off private
flag requires public identity, withdrawal, activity and confirmation flags, the
installed connection guard, corresponding existing stores and the canonical
private token. The native mode versions the configuration fingerprint as
`adr624/native-startup-v1`; it cannot silently reuse the prior protocol's hash.
The control listener is not the identity endpoint. Customer Host/path routing
continues through the existing public handler. No deployment manifest enables
the flag in this change.

The route is exact GET
`/v1/internal/runtime-public-edge/native-startup`, with Host
`gatewayd-public.faas`, one canonical fresh nonce and one request HMAC. Reject
queries, alternate path encodings, request bodies, transfer encoding and
protocol upgrades. Unauthenticated/ambiguous requests receive 404 without epoch
fields. The handler freezes the captured startup by value and never recaptures
or changes it in response to probes. Its authenticated response describes the
**startup** epoch; it does not assert that namespaces or local files remained
unchanged afterward.

Use the existing canonical 32-byte ingress token and HMAC-SHA-256, with separate
domains from ADR-702 and from each other:

- request input: `gregale/runtime-public-edge/native-startup/request/v1:` plus
  the nonce;
- response input: `gregale/runtime-public-edge/native-startup/response/v1:` plus
  fixed-order `encoding/json.Marshal` of the complete envelope with empty proof.

The envelope contains `startup`, `nonce`, `proof` in that order; startup fields
are slot, session, configuration, epoch; epoch fields are machine ID, boot ID,
PID, start ticks, PID namespace and network namespace. Authenticate every field,
then emit the canonical JSON with proof, `application/json`, `no-store` and an
explicit length. The body uses the existing 1024-byte identity budget.

`ProbeNativePublicStartup` requires an explicit canonical loopback literal TCP
address and a complete expected startup. Each probe uses a new nonce, fresh
direct connection, no environment proxy, keepalive, compression or redirect
following, bounded headers and the existing two-second total probe budget.
Require HTTP 200, exactly one JSON media-type header, positive bounded exact
Content-Length, no content encoding, transfer encoding or trailers, canonical
byte-for-byte envelope, the fresh nonce, valid response HMAC and equality of
every expected startup field. Unknown/duplicate/aliased fields, extra bytes,
foreign epochs, old protocol proofs, truncation and cancellation yield a zero
proof with a generic error, without arbitrary peer text or credentials.

Successful probing returns opaque `NativeStartupProof` with by-value startup
and a copying `Envelope()` accessor. It grants no membership, admission,
withdrawal, dead-process receipt, traffic cutover or retirement authority. Logs
record the startup tuple for attribution review without the token or proof.
No schema, SQLC, receipt store, signing authority or worker changes are made;
public runtime-upgrade `execution_available=false` remains unchanged.

## Consequences and limits

This supplies the missing authenticated boot/process fields on the real public
startup path. It does not qualify the first real native fencing authority.
Machine ID and namespace inode values are local identifiers rather than hardware
attestation. A holder of the shared HMAC token can forge responses; host-root or
kernel compromise, cloned machine IDs, namespaces inside containers, hibernation
or snapshot restoration are outside the proof's independent assurance.

Next, reconcile this proof with the selected retained native service/socket
scope and persist immutable enrollment before an epoch can become unreachable.
Then establish trusted machine-to-physical-resource attribution, provision an
out-of-band controller with independent credentials and qualified signing-key
custody, and demonstrate actual enforcement with every reset, recovery and
resume path excluded. The [Linux hibernation documentation](https://www.kernel.org/doc/html/latest/power/swsusp.html)
describes restoring execution from a saved image. Consequently, inference from
a reboot or power-cycle response alone cannot satisfy ADR-708's permanent
termination contract. Native enforcement, crash/restart and lost-response
acceptance remain pending; there is no production signer of boolean assertions.

This slice adds no joint service/socket collector, durable historical enrollment,
hardware adapter, deployment, remote host operation or active fencing. Native
Caddy/DNS/systemd acceptance and the larger runtime-upgrade Linux amd64 KVM
`test-metal`/`leakcheck` gates remain pending. Portable synthetic reader tests
exercise consistency/error handling; they cannot validate the actual Linux
source opener or any hardware-enforcement semantics on macOS.

## Validation and adversarial review

Require portable normal/race coverage for canonical capture, changed boot/PID
start/machine/namespaces, missing metadata, proc-stat comm ambiguity, malformed
and overflowed values, immutable returned bytes, customer routing, all startup
field tampering, old HMAC domains, nonce replay, HTTP/body framing, concurrent
fresh nonces/connections, cancellation before I/O and during an incomplete body,
exact flag dependencies, configuration versioning and rejected partial startup
installation. Compile the Linux amd64 source and tests, run focused repository-
pinned lint including Linux files/tests, policy gates and diff checks before
committing locally. Record executed tests separately from cross-compilation and
pending native acceptance.

Local validation passed 58 top-level tests normally and with race detection,
with matching test sets and no failures/skips: all 37 ingress tests and 21
selected public-gateway/connection-guard tests. Linux amd64 test-binary
compilation passed for both packages; those binaries were not executed on this
macOS arm64 host. Repository-pinned golangci-lint v2.4.0, including tests under
the Linux target and whole changed files, reported zero issues. The full focused
lint run found three existing `bodyclose` diagnostics in two untouched ingress
test files; both files were verified byte-identical to the base commit. Runbook
SQL, text encoding, shell quoting, ADR-number policy and diff checks passed.
No PostgreSQL test rerun was needed because schema, generated queries and store
implementation did not change. Actual non-root hardened-service capture and
native hardware/KVM enforcement acceptance remain unexecuted. No PR, push,
deployment or remote host operation was performed.

Adversarial review: authentication of an epoch is separate from its real-world
attribution or termination. PID alone is insufficient; bind boot and start ticks
too. A caller cannot substitute a production epoch DTO or retarget the daemon's
generated session/configuration. A valid old-protocol HMAC cannot authenticate
the native endpoint. Canonical framing and complete authentication prevent
ambiguous fields from carrying unauthenticated identity. Copies cannot mutate
retained proof or handler startup. Capture errors or late cancellation cannot
install partial native identity. Self namespace values and root-owned local
metadata cannot establish physical bare-metal identity. Probe success cannot
close a historical withdrawal or bypass authority qualification.

Primary references:

- [Linux process stat fields and start time](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html)
- [Linux process namespace links](https://man7.org/linux/man-pages/man5/proc_pid_ns.5.html)
- [Go retained filesystem roots](https://pkg.go.dev/os#Root)
