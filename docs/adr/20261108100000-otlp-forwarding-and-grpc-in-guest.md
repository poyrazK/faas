# ADR-20261108100000: The guest trace bridge forwards OTLP to the customer's backend and accepts OTLP/gRPC

- **Status:** accepted
- **Date:** 2026-11-08
- **Amends:** ADR-958 (zero-config guest tracing): the guest bridge also
  listens on 127.0.0.1:4317 for OTLP/gRPC and can copy every export to a
  customer endpoint.
- **Related:** ADR-127 (production debugger), ADR-213 (runtime log
  adapter)

## Context

ADR-958 points managed OpenTelemetry SDKs at a guest-local OTLP/HTTP bridge
(127.0.0.1:4318) that hands each export to vmmd and apid, which keep a
bounded summary of each span on the request it belongs to. Two gaps came
out of the 2026-11-07 OpenTelemetry review:

- **Gregale or your own backend, not both.** The debugger keeps a summary
  (32 attributes, no events, links or resource attributes, per-plan span
  caps), and there is no way to get the full spans out. An app that sets
  its own `OTEL_EXPORTER_OTLP_*` endpoint keeps its spans but loses the
  debugger, because the bridge then leaves its configuration alone. Customers
  who want both have to run their own fan-out.
- **OTLP/HTTP only.** The bridge serves OTLP/HTTP. Exporters that speak only
  gRPC, or are configured for it (the .NET default, many Collector setups,
  some Java and Rust configurations), cannot reach it.

Two options were considered for the first gap: a server-side trace drain
built on the log-drain system (full control-plane path, raw exports kept
before summarizing, platform egress), or forwarding from inside the VM. The
user chose forwarding inside the VM, and gRPC inside the VM only (no public
OTLP/gRPC endpoint).

## Decision

1. **`tracing.forward`.** `TracingConfig` gains `forward: {endpoint,
   headers_secret}`. `endpoint` is a base OTLP/HTTP URL (`http` or `https`,
   no credentials, query or fragment, at most 2048 bytes, not already ending
   in `/v1/traces` or `/v1/logs`); `headers_secret` optionally names an app
   secret holding headers in the `OTEL_EXPORTER_OTLP_HEADERS` form. Forward
   needs `tracing.enabled`, so it follows the tracing plan gate. The field
   is a value struct, so tracing configs stay comparable with `==`, and it
   is omitted from JSON, YAML and TOML when unset.
2. **The guest bridge copies each export.** Every non-empty trace or log
   export the bridge receives (OTLP/HTTP or OTLP/gRPC) is queued, as the
   SDK sent it, for `<endpoint>/v1/traces` or `/v1/logs`. Forwarding is
   independent of Gregale's answer and never changes it: the queue is
   bounded at 4 MiB and 64 exports and drops what does not fit, two workers
   post each export with a 10 s timeout and retry transport errors, 429 and
   502/503/504 up to three attempts with backoff, and any other status is
   final. The connection pool keeps idle connections for 15 s, so a
   connection opened before a snapshot is not reused after restore.
3. **No double forwarding on SDK retries.** When apid answers 429 or 503 the
   SDK retries the identical body. The forwarder remembers the SHA-256 of
   each forwarded body (per signal) for two minutes and skips a repeat. The
   set is bounded at 512 entries; past the bound it is cleared, which at
   worst forwards a duplicate.
4. **Headers come from the secrets file at send time.** The bridge starts
   before secrets are loaded and survives snapshot/restore, so it never
   holds a header value from startup. It reads `/etc/faas/secrets.env`
   (written by vmmd on every wake) and re-parses it only when the file's
   size or modification time changes. A missing secret or malformed header
   list fails the export permanently without sending it, and the reason is
   logged (never the value). `Host`, `Content-*`, `Transfer-Encoding` and
   `Connection` cannot be set.
5. **OTLP/gRPC on 127.0.0.1:4317.** OTLP Export calls are unary, so the
   bridge serves them with net/http's cleartext HTTP/2 (prior knowledge)
   instead of adding a gRPC server to guest-init. It accepts
   `TraceService/Export` and `LogsService/Export`, takes the single
   length-prefixed message, and hands the protobuf (gzip when
   `grpc-encoding: gzip`) to the same export path as OTLP/HTTP protobuf, so
   vmmd and apid see no difference. Acks map to gRPC codes: accepted → OK;
   rate limited, unavailable, transport failure and a busy bridge →
   UNAVAILABLE (retryable, as the HTTP bridge's 429/503 are); permanent
   refusal or malformed framing → INVALID_ARGUMENT; other methods →
   UNIMPLEMENTED. A bind failure (the app owns 4317) disables only the gRPC
   receiver. The environment the bridge stamps for managed SDKs is
   unchanged (`http/protobuf` to 4318).

## Consequences

- Customers get complete spans and logs in their own backend and the
  debugger at the same time, with one setting and no collector to run.
- Forwarded traffic leaves from the customer's VM and is metered as its
  outbound traffic; the platform carries no extra bandwidth or storage, and
  Gregale's export rate limit and span caps do not apply to the copy.
- The forward target is reached with the app's own network access, so it
  adds no reach the app does not already have; no server-side SSRF checks
  are involved.
- A rotated headers secret applies from the next wake (vmmd rewrites the
  secrets file then), not immediately while an instance keeps running.
- Apps that configure their own exporter still bypass the bridge and are
  not forwarded; the docs point them at `tracing.forward` instead.
- Not done: a server-side trace drain (which could also carry Gregale's own
  routing and wake spans), a public OTLP/gRPC endpoint, and forwarding of
  OTLP metrics (the bridge still has no metrics route).
