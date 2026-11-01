package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// ComputeObjectGatewayControlPort is the control listener (readiness,
// metrics) of the S3 gateway on a compute node, the control-plane gateway's
// port bound on every address. apid's metrics discovery, the readiness probe
// in the registry and nftables (faas_object_gateway_control_port) use it.
const ComputeObjectGatewayControlPort = "9096"

// ComputeObjectGatewayConnectorMetricsPort is the metrics listener of the
// node's s3 tunnel connector, faas-cloudflared-s3 (ADR-20261031122757 §2).
// The app edge's connector holds 20241.
const ComputeObjectGatewayConnectorMetricsPort = "20242"

// UnitS3GatewayCompute is s3-gatewayd on a compute node (ADR-20261031122757).
// A compute node runs tenant code, so the unit holds less than the control
// plane's:
//
//   - no local Postgres or apid: it reads the database through
//     compute-db.env, like the other compute daemons;
//
//   - no fleet or host identity: the seal_domains role gives it the storage
//     seal-domain identity alone, in a drop-in;
//
//   - only the gateway's provider credential, from gateway-provider.env, never
//     the bucket-admin one in provider.env.
//
//   - the control listener binds every address on
//     ComputeObjectGatewayControlPort, so the control plane can scrape it;
//     nftables admits only control-plane addresses. The data listener stays
//     on loopback, for the node's s3 tunnel connector.
//
// The s3_gateway_compute_service role sets FAAS_S3_GATEWAY_ROLE=compute-only
// in a drop-in, and in that role s3-gatewayd refuses to start holding more.
func UnitS3GatewayCompute() daemonunit.Unit {
	u := UnitS3Gateway()
	u.Description = "Gregale S3 gateway on a compute node (ADR-20261031122757)"
	u.After = []string{"network-online.target", "faas-cp.slice", "faas-gatewayd-internal.service"}
	u.Wants = []string{"faas-cp.slice"}
	u.Requires = nil
	u.EnvironmentFile = "-/etc/faas/compute-db.env -/etc/faas/secrets/object-storage/gateway-provider.env -/etc/faas/otel.env"
	env := make([]daemonunit.KV, 0, len(u.Environment))
	for _, kv := range u.Environment {
		switch kv.Key {
		case "FAAS_HOST_AGE_IDENTITY_PATH":
		case "FAAS_S3_GATEWAY_CONTROL_ADDR":
			env = append(env, daemonunit.KV{Key: kv.Key, Value: "0.0.0.0:" + ComputeObjectGatewayControlPort})
		default:
			env = append(env, kv)
		}
	}
	u.Environment = env
	u.LoadCredential = nil
	return u
}
