# ADR-346 · Customer after-restore readiness hook

- **Status:** accepted
- **Date:** 2026-09-28
- **Decision:** Request and service apps may opt into an `after_restore` HTTP
  callback in their app lifecycle configuration. The effective app manifest is
  baked into each deployment artifact. On snapshot restore, guest-init first
  completes ADR-022 entropy and clock repair, then sends one POST to the app's
  loopback listener. A 2xx response is required before the resume ACK and host
  readiness probe. Failure or timeout returns ACK 13; vmmd rejects that restore
  and the manager cold-boots from the same deployment artifact. Cold boot does
  not run the callback.
- **Bounds:** The default callback deadline is 500 ms; the customer may set at
  most 2 seconds, below the existing five-second host resume deadline. The
  request has no body, does not follow redirects, and never leaves loopback.
  Only request/service execution modes can declare it. The app must make the
  callback idempotent because a restore may be retried. The existing optional
  extension lifecycle callback remains after the ACK and best effort.
- **Security:** The guest sets `X-Faas-After-Restore: 1`. The public forwarding
  path strips inbound `x-faas-*` headers, so customer code can require this
  marker and a loopback peer address before performing hook work. The response
  body is ignored and never logged.
- **Compatibility:** Omitted configuration takes the same resume path, with no
  HTTP call. `after_restore: {}` in a PATCH clears the setting. Existing image
  artifacts retain their prior manifest; a new deployment applies a changed
  app lifecycle configuration. No database migration is required because the
  app manifest is existing JSONB.

The callback is a readiness barrier, separate from the 250 ms extension
notification contract. Reusing extension ACKs would make observability sidecars
part of app availability and would still not protect readiness because those
callbacks run after the resume ACK. A host-side HTTP callback could gate
readiness, but the guest's loopback path avoids a public route and keeps the
callback within the restored process namespace.
