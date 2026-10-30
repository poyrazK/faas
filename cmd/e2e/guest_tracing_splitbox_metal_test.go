//go:build metal

// guest_tracing_splitbox_metal_test.go — the split-box half of the ADR-958
// acceptance. A split-box compute node's vmmd sends guest exports to apid's
// private mTLS listener with its node-identity apid-client leaf (CN =
// compute_nodes.name), and apid's node verifier binds each frame to an
// instance on that node (ADR-958 amendment 2026-10-28). On one host the two
// daemons talk over loopback TCP with real certificates; only the network
// distance differs from two machines.

package e2e_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// splitBoxIngest is the PKI and listener a split-box layout gives the
// guest export path.
type splitBoxIngest struct {
	target                  string // tcp://127.0.0.1:<port>, apid's private mTLS listener
	caPath                  string
	apidCert, apidKey       string
	vmmdCert, vmmdKey       string // CN = the harness's compute node
	foreignCert, foreignKey string // same CA, a CN no compute node has
}

func newSplitBoxIngest(t *testing.T, dir string) *splitBoxIngest {
	t.Helper()
	root := filepath.Join(dir, "pki")
	caCert, caKey, err := pki.EnsureCA(root, false)
	if err != nil {
		t.Fatalf("split-box CA: %v", err)
	}
	apidRole := splitBoxRole(t, "apid", "advisory")
	if err := pki.EnsureLeafWithSANs(root, apidRole, caCert, caKey, false, pki.LocalDevSANs()); err != nil {
		t.Fatalf("apid listener leaf: %v", err)
	}
	vmmdRole := splitBoxRole(t, "vmmd", "apid-client")
	if err := pki.EnsureLeafWithCNAndSANs(root, vmmdRole, state.DefaultLocalNodeName, caCert, caKey, false, pki.AltNames{}); err != nil {
		t.Fatalf("vmmd apid-client leaf: %v", err)
	}
	foreignRoot := filepath.Join(dir, "pki-foreign")
	if err := pki.EnsureLeafWithCNAndSANs(foreignRoot, vmmdRole, "unregistered-node", caCert, caKey, false, pki.AltNames{}); err != nil {
		t.Fatalf("unregistered node leaf: %v", err)
	}
	s := &splitBoxIngest{target: "tcp://" + freeLoopbackAddr(t)}
	s.caPath, _ = pki.CARoot(root)
	s.apidCert, s.apidKey = pki.LeafPaths(root, apidRole)
	s.vmmdCert, s.vmmdKey = pki.LeafPaths(root, vmmdRole)
	s.foreignCert, s.foreignKey = pki.LeafPaths(foreignRoot, vmmdRole)
	return s
}

func splitBoxRole(t *testing.T, directory, filename string) pki.Role {
	t.Helper()
	for _, role := range pki.Roles() {
		if role.Directory == directory && role.Filename == filename {
			return role
		}
	}
	t.Fatalf("pki has no %s/%s role", directory, filename)
	return pki.Role{}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

// apidEnv opens apid's private mTLS listener with the node verifier on.
// Only apid gets these: gatewayd-internal reads the same TLS variable names
// as its client leaf.
func (s *splitBoxIngest) apidEnv() []string {
	return []string{
		"FAAS_APID_APP_ERRORS_TARGET=" + s.target,
		"FAAS_APID_APP_ERRORS_TLS_CERT_PATH=" + s.apidCert,
		"FAAS_APID_APP_ERRORS_TLS_KEY_PATH=" + s.apidKey,
		"FAAS_APID_APP_ERRORS_TLS_CA_PATH=" + s.caPath,
		// A named apid builds the node verifier that maps a leaf CN to its
		// compute node.
		"FAAS_NODE_NAME=" + state.DefaultLocalNodeName,
	}
}

// vmmdEnv points vmmd's guest exports at the mTLS listener. App errors stay
// off, so gatewayd-internal keeps using its Unix sockets.
func (s *splitBoxIngest) vmmdEnv() []string {
	return []string{
		"FAAS_APP_ERRORS_ENABLED=false",
		"FAAS_VMMD_SPANS_WRITER_TARGET=" + s.target,
		"FAAS_VMMD_APID_CLIENT_TLS_CERT_PATH=" + s.vmmdCert,
		"FAAS_VMMD_APID_CLIENT_TLS_KEY_PATH=" + s.vmmdKey,
		"FAAS_VMMD_APID_CLIENT_TLS_CA_PATH=" + s.caPath,
	}
}

// probeBinding sends frames with real certificates, as a compromised or
// misconfigured node would: the node's own leaf naming an instance it does
// not run, its running instance under another account, and a leaf whose CN
// names no compute node.
func (s *splitBoxIngest) probeBinding(t *testing.T, pool *pgxpool.Pool, appID, deploymentID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var accountID, instanceID string
	if err := pool.QueryRow(ctx, `SELECT a.account_id::text, i.id::text FROM apps a JOIN instances i ON i.app_id = a.id
		 WHERE a.id = $1 AND i.state = 'running' LIMIT 1`, appID).Scan(&accountID, &instanceID); err != nil {
		t.Fatalf("read the app's running instance: %v", err)
	}
	frame := func(account, instance string) *apidpb.IngestGuestSpansRequest {
		return &apidpb.IngestGuestSpansRequest{
			AccountId: account, AppId: appID, DeploymentId: deploymentID, InstanceId: instance,
			Codec: uint32(api.TraceCodecProtobuf), Payload: []byte{0x0a, 0x00},
		}
	}
	node := s.dial(t, s.vmmdCert, s.vmmdKey)
	for _, probe := range []struct {
		name string
		req  *apidpb.IngestGuestSpansRequest
	}{
		{"an instance its node does not run", frame(accountID, uuid.NewString())},
		{"its running instance under another account", frame(uuid.NewString(), instanceID)},
	} {
		spans, err := node.IngestGuestSpans(ctx, probe.req)
		if err != nil || spans.GetOutcome() != api.TraceIngestInvalid {
			t.Fatalf("spans for %s: %+v, %v; want %q", probe.name, spans, err, api.TraceIngestInvalid)
		}
		logs, err := node.IngestGuestLogs(ctx, probe.req)
		if err != nil || logs.GetOutcome() != api.TraceIngestInvalid {
			t.Fatalf("logs for %s: %+v, %v; want %q", probe.name, logs, err, api.TraceIngestInvalid)
		}
	}
	if resp, err := s.dial(t, s.foreignCert, s.foreignKey).IngestGuestSpans(ctx, frame(accountID, instanceID)); err == nil {
		t.Fatalf("a leaf naming no compute node was accepted: %+v", resp)
	}
	t.Logf("SPLIT_BOX guest span arrived over %s; foreign instance, foreign account and unregistered node refused", s.target)
}

func (s *splitBoxIngest) dial(t *testing.T, cert, key string) *apidgrpc.GuestSpansClientImpl {
	t.Helper()
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("vmmd_apid_client_", cert, key, s.caPath)
	if err != nil {
		t.Fatalf("client TLS %s: %v", cert, err)
	}
	client, err := apidgrpc.DialGuestSpans(context.Background(), s.target, tlsCfg)
	if err != nil {
		t.Fatalf("dial %s: %v", s.target, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
