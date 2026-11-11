package manifest

// HotPath holds the request- and wake-path switches of the compute nodes'
// daemons. Each one shipped default off, was proven on production by hand
// (systemd drop-ins), and is rendered here so a node rebuild or a role run
// keeps it. Every field is off by default.
type HotPath struct {
	// LocalRequestIDJournal makes gatewayd-internal journal request IDs on
	// the node instead of in the control plane's request_id_journal
	// (ADR-20261101085552 step 4; FAAS_REQUEST_ID_JOURNAL_LOCAL).
	LocalRequestIDJournal bool `yaml:"local_request_id_journal,omitempty"`
	// RequestTelemetryMinuteCollapse makes gatewayd-internal write one
	// request-telemetry row per app, route, status and minute instead of one
	// per request (ADR-20261101085552 step 1;
	// FAAS_REQUEST_TELEMETRY_MINUTE_COLLAPSE).
	RequestTelemetryMinuteCollapse bool `yaml:"request_telemetry_minute_collapse,omitempty"`
	// LocalVMMDDataSocket makes vmmd serve this node's gatewayd-internal on a
	// local unix socket and the gateway use it instead of mTLS over TCP
	// (FAAS_VMMD_LOCAL_DATA_SOCKET + FAAS_GATEWAY_LOCAL_VMMD_SOCKET). Both
	// halves render together, so the gateway never dials a socket vmmd does
	// not serve.
	LocalVMMDDataSocket bool `yaml:"local_vmmd_data_socket,omitempty"`
	// EdgeKeepAlivePerNode makes each compute edge keep connections to
	// gatewayd-internal alive per node (FAAS_INTERNAL_KEEPALIVE_PER_NODE).
	// Needs public_edge.compute_nodes.
	EdgeKeepAlivePerNode bool `yaml:"edge_keepalive_per_node,omitempty"`
	// EdgeBinaryHop makes each compute edge send the requests for its own
	// node's gatewayd-internal in binary HTTP (ADR-20261110081523;
	// FAAS_EDGE_INTERNAL_HOP=binary). Needs edge_keepalive_per_node, whose
	// transport carries it.
	EdgeBinaryHop bool `yaml:"edge_binary_hop,omitempty"`
	// FrozenTier, FrozenFastPath and OwnerCache are schedd's wake-path
	// switches (FAAS_FROZEN_TIER, FAAS_FROZEN_FAST_PATH, FAAS_OWNER_CACHE).
	// The fast path needs the tier.
	FrozenTier     bool `yaml:"frozen_tier,omitempty"`
	FrozenFastPath bool `yaml:"frozen_fast_path,omitempty"`
	OwnerCache     bool `yaml:"owner_cache,omitempty"`
}

func (h HotPath) validate(edge PublicEdge) Errors {
	var errs Errors
	if h.EdgeKeepAlivePerNode && !edge.ComputeNodes {
		errs = append(errs, Error{"hot_path.edge_keepalive_per_node", "needs public_edge.compute_nodes: only compute edges dial gatewayd-internal per node"})
	}
	if h.EdgeBinaryHop && !h.EdgeKeepAlivePerNode {
		errs = append(errs, Error{"hot_path.edge_binary_hop", "needs hot_path.edge_keepalive_per_node: the binary hop rides its per-node transport"})
	}
	if h.FrozenFastPath && !h.FrozenTier {
		errs = append(errs, Error{"hot_path.frozen_fast_path", "needs hot_path.frozen_tier: the fast path wakes frozen instances"})
	}
	return errs
}
