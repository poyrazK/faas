# ADR-386 · Event and trace wire conformance

- **Status:** accepted
- **Date:** 2026-09-30
- **Decision:** Correct the existing CloudEvents, AsyncAPI 3.0, and OTLP/HTTP
  contracts and gate them with official-schema and independent SDK checks.

The previous trace ingress used ordinary protobuf JSON, whose base64 byte
encoding differs from OTLP's hexadecimal IDs. It rejected normal multi-trace
exporter batches and returned a Gregale-specific success envelope. CloudEvents
webhooks used an invalid `account_id` extension name. AsyncAPI operation
security declarations used OpenAPI syntax, which the custom unit test accepted.

Trace ingress uses the OpenTelemetry Collector's OTLP JSON decoder. It also
accepts binary protobuf, gzip within the existing 4 MiB compressed/decompressed
bound, and empty exports. Authentication, plan checks, and rate limiting precede
body reads and decompression. The whole request is shape-validated before any
trace is buffered. Each trace still passes the accumulator's account ownership
check. Contested traces produce a standard partial-success rejection count;
other traces in the batch are accepted. Ownership errors do not disclose trace
identifiers. Full success uses ExportTraceServiceResponse and failures use
google.rpc.Status in the request encoding.

The wire correction replaces the custom success-body counters with the
X-Gregale-Accepted-Spans response header. Requests without Content-Type retain
the old ordinary-protobuf JSON input profile. Properly typed OTLP JSON uses
standard hexadecimal IDs. Accumulator/flush ownership and the debugger's
slowest-span retention stay intact; acceptance remains an in-memory diagnostic
summary acknowledgement, not a durable raw-telemetry guarantee. Metrics and
OTLP/gRPC ingress remain separate future contracts.

CloudEvents output uses `accountid`, matching canonical internal envelopes.
Historical snake_case input aliases and legacy Gregale JSON webhook output
remain supported. Source URI references are validated before persistence;
unescaped spaces and malformed escapes are rejected. Consumers of the opt-in
CloudEvents format must migrate their tenancy-extension lookup.

AsyncAPI security requirements reference the declared security scheme. The
unmodified official schema is pinned with its upstream commit and license and
validated offline. The standards conformance command checks that published
schema as well as test references. `make standards-contract-check` exercises
the schema, the CloudEvents SDK, real OpenTelemetry exporter ingestion, both
HTTP encodings, gzip, and partial-success account isolation in CI. Registry
claims continue to describe scoped support rather than claiming all optional
standard surfaces or changing product maturity.

Rollback is the previous gateway/webhook implementation plus its matching
contract documents. No database migration, VM lifecycle change, service broker,
or billing export is part of this decision.
