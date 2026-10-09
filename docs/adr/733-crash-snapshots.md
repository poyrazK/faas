# ADR-733 · Crash snapshots: capture a running instance when it fails

- **Status:** proposed
- **Date:** 2026-10-08
- **Builds on:** ADR-732 (production forks): a crash snapshot is opened as a
  fork, so it inherits the quarantine, access token, TTL, secrets scrub and
  never-routed rules.
- **Amends:** ADR-159 (a capture can be kept as evidence, not cache).
- **Decision:**
  - **Opt-in per app.** `crash_snapshot_settings(app_id, enabled)`, written
    only by apid. Pro and Scale only. Off by default: a crash snapshot is a
    copy of production memory, end-user data included.
  - **Triggers.**
    - **HTTP 5xx.** When gatewayd-internal forwards a request that a
      serving instance answers with 5xx, it asks for a capture of that
      instance. The insert is one SQL statement that succeeds only when the
      app has opted in, has no capture in flight, and has not captured
      within the cooldown (10 min). A refused insert is silent; the gateway
      never blocks or fails the request for it.
    - **Manual.** `POST /v1/apps/{slug}/crash-snapshots` captures the app's
      newest running instance now, under the same rules.
  - **Capture.** schedd's `CrashCaptureCoordinator` claims requested rows
    and, under the app lock, captures the still-running instance with the
    existing pause → snapshot → resume path (`WarmSnapshot`) into a fresh
    capture key under the deployment's capture directory. The serving
    instance resumes; the capture is never a `snapshots` row, so no wake can
    ever restore it (ADR-159 caches stay caches). A capture that does not
    finish within 5 min is failed.
  - **Evidence rows.** `crash_captures` holds the trigger (status code,
    route), the capture keys, Firecracker version, size, and `expires_at`
    (default 7 days, in `limits.go`). schedd writes the capture lifecycle.
    imaged, which already owns capture file deletion for snapshot rows,
    owns the capture files: encryption, staging for forks and expiry. At
    expiry it deletes every object (plaintext and encrypted) and only then
    marks the row `expired` and drops its sealed key, so a failed delete is
    retried on the next pass.
  - **Encryption at rest.** Within one imaged pass (5 s) of a capture
    becoming ready, imaged compresses (zstd; guest memory is mostly zero
    pages and ciphertext is never sparse) and encrypts each of the four
    objects with a fresh age X25519 identity, seals that identity to the
    fleet recipient in `crash_captures.sealed_key` (namespace bound to the
    capture ID, so a key copied to another row does not open), and deletes
    the plaintext. `plaintext_state` tracks the plaintext: `present` →
    `purging` → `absent`, and `absent` → `staging` → `staged` while a fork
    pinned to the capture is active (queued, restoring or running). The
    fork coordinator does not claim such a fork until its capture is
    `staged`, and the purge of a staged capture is fenced on no active
    fork, so a fork never loses the plaintext it restores from. When the
    fork ends, imaged purges the plaintext again. Encryption, staging,
    purge and expiry run on one imaged goroutine, so they never race.
    Without a host age identity imaged leaves captures `present` and logs
    an error every pass.
  - **Local storage only.** The encryption runs through the storage
    backend. On a remote backend (GCS, OCI) a node's read-through cache
    could keep a plaintext copy imaged cannot purge, so schedd fails every
    capture there with `storage_unsupported`.
  - **Open as a fork.** `POST /v1/apps/{slug}/crash-snapshots/{id}/fork`
    creates an ADR-732 fork pinned to the capture (`app_forks.
    crash_capture_id`). `Engine.RestoreFork` restores that capture instead
    of the deployment's newest one. The same scopes (deploy:write AND
    secrets:read), MFA, audit and limits apply.
  - **Gate.** Everything stays behind `FAAS_CRASH_SNAPSHOTS`.
- **Why:** the most useful moment to inspect a process is right after it
  failed. Logs and traces say what happened; the memory says why. Forks
  already give a safe way to look at a capture; this adds captures taken at
  the right moment.
- **Consequences:**
  - A capture pauses the serving instance for the snapshot (bounded by
    `SnapshotBudgetFor(ram)`), so in-flight requests on that instance see
    added latency. The cooldown and one-in-flight rule bound how often.
  - Captures use disk on the node until they expire (about the app's RAM
    each, sparse).
  - The capture is taken after the failing response, not at the instant of
    the fault; state that unwinds on error is gone. An SDK hook that asks
    for a capture from inside the error handler is a follow-up.
  - A capture sits in plaintext for up to one imaged pass after it is
    taken, and in plaintext again for as long as a fork of it is active.
  - The key is per capture, not per account: deleting a capture destroys
    its only key, and one leaked key exposes one capture.
- **Rejected alternatives:**
  - **Storing captures as `snapshots` rows with a new tier.** The partial
    unique index allows one live row per tier, GC floors and replication
    would all need exceptions, and a mistake there would let a wake restore
    a crash capture. A separate table keeps captures out of every serving
    path.
  - **Capturing on OOM or liveness failure.** By then the process is dead
    or the VM is being destroyed; there is nothing useful to capture.
- **Verification:** On 2026-10-09 (later run), with encryption at rest,
  `TestCrashSnapshotMetal` passed again on the internal test node: the ready
  capture was encrypted and its plaintext deleted from disk (`.age` objects
  only, memory compressed far below `mem_bytes`), a fork of it waited until
  imaged staged the plaintext, restored and served a token-bearing request,
  and the plaintext was purged again after the fork was cancelled. The
  PgStore state tests for the encryption lifecycle passed on the node's
  Postgres. Earlier, before encryption: `TestCrashSnapshotMetal` passed on the
  internal test node (nested KVM, Firecracker 1.7.0): a manual capture of
  the running instance completed in place (the instance kept running and no
  `snapshots` row carried the capture), and the capture opened as a fork
  that a token-bearing request reached. Also: state tests for the trigger rules (opt-in, in-flight,
  cooldown) on MemStore and Postgres; coordinator tests for capture,
  failure and expiry; engine test that a capture resumes the serving
  instance and never creates a `snapshots` row; gateway test that a 5xx
  requests a capture without affecting the response; a metal e2e that
  captures a running app manually, opens it as a fork, and reaches it.
