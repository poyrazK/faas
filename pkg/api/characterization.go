package api

// CharacterizationReport is the payload guest-init ships to the host
// over AF_VSOCK STREAM at port 1026 / msgtype 3 (ADR-051 §"Wire
// constants") during the FIRST cold boot of a new deployment. The
// host (pkg/fcvm/vmm.go::WaitCharacterizationReport in PR-D) reads
// it, re-derives the authoritative workload class from the observed
// signals, calls store.SetAppWorkloadClass, and only then publishes
// the instance to the gateway target set (so a mis-classified app
// never serves customer traffic on the wrong path).
//
// The guest's `ObservedClass` is a hint, not a fact — the host
// re-derives the class from {ObservedClass, ObservedPort, ExitCode,
// ListeningAddrs, OutboundCount}. The Wire-format discriminator and
// length prefix are mirror-shaped against pkg/api/stateless_advisory_*
// (msg_type prefix + body_len prefix + JSON body) but the inner type
// belongs here so both the guest's emit-side and the host's read-
// side only depend on pkg/api, not on each other.
//
// Every JSON tag is wire-stable: changing one is a wire breaking
// change. ADR-051 §"Rejected alternatives" rules out DGRAM (silent
// drop is not acceptable for a report that gates a deploy); the
// STREAM + 1-byte ack wire is the load-bearing choice.
type CharacterizationReport struct {
	// ObservedClass is the guest's best guess. The host re-derives.
	ObservedClass string `json:"observed_class"`
	// ObservedPort is the TCP port the guest saw the app bind. 0 means no
	// socket was observed; ExitCode then distinguishes job, worker, and startup
	// failure.
	ObservedPort int `json:"observed_port"`
	// ExitCode captures the supervisor's terminal exit. -1 means the workload
	// was still running when the report was produced; 0 is a clean exit and a
	// positive value is a startup failure when ObservedPort is 0.
	ExitCode int `json:"exit_code"`
	// ListeningAddrs lists every address the guest observed the app
	// bind (with the address family — `0.0.0.0` vs `127.0.0.1` — so
	// the host can reject loopback-only binds per ADR-051 §"Failure
	// messages become specific").
	ListeningAddrs []string `json:"listening_addrs,omitempty"`
	// OutboundCount is the number of outbound connections the app
	// opened during the characterization window. Used by the host
	// to disambiguate `job` (0 outbound + exit 0) from `worker`
	// (≥1 outbound + still running).
	OutboundCount int `json:"outbound_count"`
	// LogTail carries the last N bytes of the supervisor's stdout/
	// stderr so a non-zero exit code surfaces the actual failure to
	// the deploy row (vs the existing "guest not ready after 30s"
	// opaque message). 64 KiB cap; ring-buffered in guest-init.
	LogTail string `json:"log_tail,omitempty"`
	// PortNormalizationMode records which step of the portnorm
	// ladder (none / dnat / forward) the guest activated for this
	// boot. Surfaced as `guest_port_normalization_total{mode=...}`
	// (ADR-051 §"Consequences") so the platform team can monitor
	// the DNAT-vs-userspace-forwarder mix.
	PortNormalizationMode string `json:"port_norm_mode,omitempty"`

	// OpenAPIDoc is the captured OpenAPI document body, if any.
	// Empty when the probe found no JSON document at the canonical
	// /openapi.json path, when the customer's app is not HTTP-shaped
	// (e.g. job / grpc-only), or when the discovery doc exceeds the
	// wire cap (see OpenAPIDocTruncated). Per ADR-122 §D2 this is a
	// wire-additive field — old receivers ignore it (`encoding/json`
	// skips unknown keys), and a new receiver treats OpenAPIDoc==nil
	// as "no doc captured" (the only case an old probe would produce).
	OpenAPIDoc []byte `json:"openapi_doc,omitempty"`
	// OpenAPIDocTruncated is true when the captured body was
	// truncated at VsockCharacterizationMaxBody. The guest hard-
	// truncates BEFORE json.Marshal so the receiver never sees a
	// malformed body. Receivers must surface this to the user
	// (dashboard widget + CLI) so a customer hitting the 128 KiB
	// cap can opt into the manual-upload PATCH endpoint to upload
	// a partial-doc of their choice.
	OpenAPIDocTruncated bool `json:"openapi_doc_truncated,omitempty"`
}
