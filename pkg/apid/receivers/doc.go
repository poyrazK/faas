// Package receivers holds apid's gRPC ingest receivers: app errors, auth,
// guest and gateway spans, request telemetry, telemetry coverage and the
// realtime fleet/history/inbox services, plus the ingest gate that bounds
// their database concurrency. It was lifted out of cmd/apid by ADR-977.
//
// Each receiver is built from explicit dependencies (a state.Store, ops
// metrics, limiters); apid-only behaviour such as sealing realtime push
// registrations is passed in as a function.
package receivers
