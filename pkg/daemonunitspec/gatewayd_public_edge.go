package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// UnitGatewaydPublicEdge is gatewayd-public running as a compute node's app
// edge behind a local Cloudflare Tunnel connector (ADR-1343). It is the same
// binary and the same hardening as UnitGatewaydPublic, derived from it so the
// two cannot drift, with these differences:
//
//   - no Postgres or apid dependency: a compute node runs neither. The
//     database is remote (compute-db.env) and only read for the compute
//     pool fallback and public-bucket matching;
//   - FAAS_PUBLIC_EDGE_MODE=compute: app hosts only, node-local gateway
//     first, credential-bound routes forwarded to the control plane;
//   - the public socket is 127.0.0.1:8090 (gatewayd-internal holds 8080 on a
//     compute node) and only the local connector dials it;
//   - the control listener binds every address on 9092 so the control plane
//     can probe and scrape it; nftables admits only control-plane addresses;
//   - no object-storage credentials and no OTLP spans writer.
//
// It lives in OptionalRegistry: the manifest turns it on per cell
// (public_edge.compute_nodes), so it stays out of the core activation and
// release-health loops of compute nodes that do not run the edge.
func UnitGatewaydPublicEdge() daemonunit.Unit {
	u := UnitGatewaydPublic()
	u.Description = "onebox-faas gatewayd-public — compute-node app edge behind Cloudflare Tunnel (ADR-1343)"
	u.After = []string{"faas-cp.slice", "network-online.target", "faas-gatewayd-internal.service"}
	u.Wants = []string{"faas-cp.slice", "faas-gatewayd-internal.service"}
	u.Requires = []string{"faas-gatewayd-public-edge.socket"}
	u.EnvironmentFile = "-/etc/faas/compute-db.env -/etc/faas/otel.env"
	u.Environment = []daemonunit.KV{
		{Key: "GOMEMLIMIT", Value: "384MiB"},
		{Key: "FAAS_PUBLIC_EDGE_MODE", Value: "compute"},
		{Key: "FAAS_GATEWAYD_PUBLIC_ROLE", Value: "compute-only"},
		{Key: "FAAS_PUBLIC_LISTEN_ADDR", Value: "127.0.0.1:8090"},
		{Key: "FAAS_PUBLIC_CONTROL_ADDR", Value: "0.0.0.0:9092"},
		{Key: "FAAS_TRUSTED_INGRESS_CIDRS", Value: "127.0.0.0/8,::1/128"},
		{Key: "FAAS_OTEL_SPANS_WRITER_ENABLED", Value: "false"},
	}
	return u
}
