# ADR-506 · Startup-safe runtime secret notifications

- **Status:** accepted
- **Date:** 2026-10-03
- **Decision:** The secret reload worker owns one pending notification for the
  latest successfully published revision. It checks delivery independently of
  the 10-second fetch cadence, retries failures with exponential backoff from
  one second to one minute, coalesces newer revisions and cancels with its
  workload context. Delivery observations transition from queued or failed to
  their actual completion outcome. Each command generation records the exact
  secret map and opaque revision used to construct its environment; process
  identity becomes signalable only after successful exec and is retired on exit.
  If a new exec already received the latest values and has received no later
  reload signal, report updated/not_attempted with no error rather than sending
  a redundant signal. A new migration admits this closed combination in both
  observation constraints; the existing signal enum and API shapes remain.
- **Startup readiness:** Images may set
  `com.gregale.secret-reload-readiness="required"`, projected into the internal
  image manifest as secret_reload_readiness. Guest-init prepares a unique,
  mode-0600, workload-owned marker in the platform-owned projection directory
  for every exec and exposes its image-local path through
  FAAS_SECRETS_RELOAD_READY_FILE. After installing its reload signal handler,
  the application writes exactly `ready\n` to that existing file. Before then,
  notifications remain queued. Readiness from a prior generation cannot unlock
  a replacement process. Guest-init removes the marker after exit; its value
  never leaves the guest. The Node starter opts in, installs the listener before
  awaited database setup and serializes initial application and subsequent reloads.
- **Compatibility:** Images without the readiness label retain their existing
  startup-gate contract and must install their handler early. Their arbitrary
  application initialization cannot be proven ready by guest-init. They still
  gain coalescing, redundant-startup-signal avoidance and delivery recovery.
  No existing manifest becomes handshake-dependent. Upgrade the outcome validator and apply the observation
  migration before replacing guests with the updated guest-init; redeploy the
  starter image to opt into the readiness guarantee. Older guests ignore the
  additive image field and the helper tolerates an absent marker variable.
- **Why:** Flushing queued signals immediately after cmd.Start could invoke the
  default signal action before application initialization. A failed send was
  never retried once a fetched revision became unchanged, and queued sends
  discarded errors without correcting observations. Kernel caught-signal masks
  cannot prove application handler readiness: language runtimes can install
  generic handlers before user code has opted into notifications.
- **Consequences:** Signal completion, startup environment delivery and marker
  readiness remain distinct from application adoption. Only the version-fenced
  application acknowledgement can attest credential application. Notifications
  remain queued indefinitely when a required marker is absent; the platform
  never guesses readiness using an arbitrary delay. Retries have capped delay,
  continue while the revision is pending, and stop on completion, supersession
  or cancellation. If publishing a newer projection fails, suppress older
  delivery until a projection is successfully published again.
- **Validation:** Cover delayed handler installation with an actual child and
  production Start ordering, environment consumption, rotations and reversions,
  per-exec readiness across restarts, retry/backoff and unchanged host responses,
  revocation/coalescing, cancellation, failed publication and truthful receipt
  separation. Validate both OCI parsers and main/sidecar manifest projection,
  Node marker compatibility and sanitization, MemStore/Postgres outcome parity,
  native x86_64 KVM main/sidecar lifecycle and leakcheck.
- **Rejected alternatives:** A fixed startup sleep is unreliable for slow
  initialization. Repeatedly flushing a signal slice permits duplicate and stale
  deliveries. Treating a successful exec or signal as application adoption
  violates the separate acknowledgement contract.
