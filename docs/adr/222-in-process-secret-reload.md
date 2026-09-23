# ADR-222 · Opt-in in-process secret reload for single-workload apps

- **Status:** accepted
- **Date:** 2026-09-23
- **Decision:** An OCI label, `com.gregale.secret-reload-signal`, opts a main
  workload into live secret refresh and selects `SIGHUP`, `SIGUSR1`, or
  `SIGUSR2`. vmmd serves the current secret map over the instance-bound VSOCK
  channel using the live deployment's scope and positive `env_secrets`
  allowlist. guest-init atomically replaces a mode-0400 JSON projection on
  `/tmp` tmpfs and forwards the configured signal to the main process.
- **Why:** A process's environment is immutable after `exec`; rotating a stored
  secret cannot update an already-running process. Some applications can
  reload credentials without replacing the process, while others need the
  existing no-snapshot rolling `--restart` path.
- **Consequences:** The application must handle the selected signal, reread
  `FAAS_SECRETS_FILE`, and apply the values itself. Refresh is polled every 10
  seconds and does not provide an application-level reload acknowledgement.
  `secrets list` reports the guest's latest projection/signal outcome separately
  from wake-time delivery; it does not claim the app applied the new values.
  Reports are fenced to the exact secret versions and rejected if a rotation
  wins the race. The feature is
  restricted to single-workload deployments: guest-init rejects an opted-in
  deployment with sidecars, and vmmd independently denies secret refresh for
  deployments that declare sidecars. No cross-app secret sharing is added.
  The file is guest-local, app-owned, read-only, and placed on tmpfs; plaintext
  is not returned by the general metadata HTTP endpoint or persisted to the
  application disk. `--restart` remains the fallback and the default.
- **Rejected alternatives:** Mutating environment variables in a live process
  is not a portable or reliable contract. Sending secret values through the
  existing HTTP metadata endpoint would broaden access to unrelated guest
  processes and weaken its non-sensitive configuration boundary. Automatically
  restarting every workload on each secret mutation is already available via
  `--restart` but cannot provide in-process reload semantics.
