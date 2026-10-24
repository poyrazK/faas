package api

// GatewaydPrivateControlPort is the compute-node port of gatewayd-internal's
// private control listener (ADR-20261022004500). The loopback control
// listener (127.0.0.1:9090) is unreachable from apid on the control plane
// when gatewayd-internal runs only on compute nodes; this listener serves the
// control routes apid calls (route observations, streaming caps, MCP replica
// probes, deployment response checks) on the node's private address, and
// nftables admits only the control plane to it.
const GatewaydPrivateControlPort = "9097"
