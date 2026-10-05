# ADR-508 · Process-generation secret acknowledgements

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** Guest-init generates a fresh 128-bit opaque execution ID for
  every opted-in main/sidecar process, registers it with vmmd before exec and
  stamps FAAS_SECRETS_RELOAD_GENERATION into that process's environment. The
  application echoes generation alongside revision/status in its ACK. The
  metadata proxy forwards that supplied identity; it never fills it from the
  current process. vmmd derives account/app/instance identity from VSOCK and
  validates the workload's persisted reload grant.
- **Ordering:** Persist one generation per instance/workload. Starting a new
  generation requires the previously registered generation, while retrying the
  same active registration is idempotent. An empty record permits the first
  registration, including a restored process on a new instance. Retirement is
  conditional on the exact generation and cannot retire a replacement. A retired
  generation cannot be reactivated. Registration, retirement and ACK writes lock
  the same workload record. Transitions clear earlier live application receipts
  atomically; ACK writes require an active matching generation as well as the
  current authorized secret versions. Promotion revisions include these changes.
- **Guest lifecycle:** Never exec before successful registration. Retry transport
  unavailability within a bounded startup budget, retaining an uncertain start's
  ID across retries. Attempt retirement on every exit/start failure, then reconcile
  the current active/retired generation with the reload worker. Reconciliation
  registers a restored process in its newly bound instance without erasing an
  idempotent current ACK. The runtime configuration VSOCK client wraps a
  nonblocking file in Go's poller, with bounded connect/read/write deadlines;
  `net.FileConn` cannot wrap AF_VSOCK sockets. Configure the metadata listener's
  loopback address through kernel ioctls so PID1's PATH and application-image tools
  cannot prevent ACK delivery. A transport outage can delay host knowledge of exit;
  replacement execution still requires registration and invalidates old evidence
  before it starts. This is a trusted-workload self-attestation, not process
  authentication or an instantaneous distributed liveness guarantee.
- **Why:** An application ACK keyed only by instance/workload/version survives an
  in-VM restart at the same secret version. Strict promotion could accept evidence
  belonging to the previous process. Late registration, retirement and ACK writes
  must not restore that evidence or overwrite a newer execution.
- **Compatibility:** The new host accepts legacy revision-only ACKs only while no
  execution generation has been registered. They remain visible as legacy status,
  but strict application adoption requires a nonempty active generation matching
  the ACK generation. Missing generation coverage is unknown. Upgrade vmmd and
  apply the migration before replacing guests; redeploy applications with the
  updated helper to send generation-aware ACKs. Old helpers in new guests cannot
  satisfy the fence. Historical completed revocation operations retain their
  completion history; new revocation ACK writes pass the same process fence.
- **Validation:** MemStore/PostgreSQL parity for unchanged-version restarts,
  idempotent and uncertain registration, late start/retire/ACK, missing coverage,
  independent sidecars, deletion, and promotion races. Cover guest exec ordering,
  environment spoofing, restart/retirement/reconciliation, metadata/VSOCK shape,
  Node generation propagation and sanitization. Require native x86_64 KVM main and
  sidecar lifecycle acceptance and leakcheck with no skipped required cases.
- **Rejected alternatives:** Clearing ACKs on restart without fencing writes lets
  a delayed old ACK restore success. Stamping the newest generation in the proxy
  misattributes old callers. Timestamps do not provide execution identity or
  atomic ordering. Guessing coverage for older guests weakens strict promotion.
