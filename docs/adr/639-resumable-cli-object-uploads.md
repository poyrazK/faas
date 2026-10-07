# ADR-639: Resumable CLI object uploads

- **Status:** accepted
- **Date:** 2026-10-07
- **Decision:** Save private local multipart checkpoints and expose
  `bucket upload --resume <upload-id>`. Bind the endpoint, owned session,
  destination, immutable part geometry, content type, whole-file SHA-256 and
  per-part hashes. Persist each part attempt before signing and each accepted
  ETag afterward. Reconcile bounded provider-confirmed listings before resuming.
- **Why:** The server already owns durable multipart sessions and completion
  recovery, but a fresh CLI process lost its part manifest and started another
  upload. Reusing a same-size changed file or an unverified part acknowledgment
  could complete an object containing bytes from different sources.
- **Consequences:** Verified acknowledged parts are skipped. Missing or
  unacknowledged parts are staged to private temporary files and checked against
  their saved hashes before signing; explicit resume can resend an uncertain
  part after server admission. Ordered completion intent is persisted before
  submission. Completed-session replay contacts only the status API. Pending
  completion reuses the saved manifest through existing server fences. Source
  checks, OS file locks, bounded strict checkpoint decoding, atomic replacement,
  private permissions and directory synchronization protect local recovery.
  Windows uses the user configuration directory ACL and file synchronization. A killed process
  may leave one private staged part; the next resume discards it under the
  same session lock before staging fingerprint-matching bytes.
  Checkpoints survive successful completion for replay and stay separate from
  deployment cache cleanup. Each record is at most 4 MiB and each staged part
  uses at most the session part size, with the existing 5 GiB ceiling.
- **Rejected alternatives:** Trusting size or modification time as source
  identity; treating opaque multipart ETags as whole-file checksums; assuming a
  provider-confirmed part without a local ETag belongs to the original file;
  silently replacing a session after an uncertain response; adding new provider
  authority or retrying raw signed writes automatically.

This extends [ADR-158](158-provider-neutral-multipart-uploads.md) and
[ADR-531](531-s3-multipart-transfer-fencing-and-cleanup.md). It does not change
server admission, quota, cleanup, leases, version identities or provider
configuration. Lost creation responses before checkpoint publication and
single-PUT write recovery remain inspection paths.
