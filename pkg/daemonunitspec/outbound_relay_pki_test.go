package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pki"
)

// TestOutboundRelayTLSPathsMatchTheIssuedLeaves pins ADR-989's relay leaves
// to the paths the roles read. control_plane_service issues outboundd's
// server leaf and points outboundd at it in its relay drop-in; vmmd_service
// installs vmmd's client leaf where vmmd's relay drop-in already points. A
// path that drifts from what pkg/pki writes fails only on a live host, as a
// TLS load error that keeps the relay closed.
func TestOutboundRelayTLSPathsMatchTheIssuedLeaves(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	ca, _ := pki.CARoot(pki.DefaultRootDir)

	server := pki.FeatureRoles(pki.FeatureOutboundRelay, "control-plane")
	if len(server) != 1 {
		t.Fatalf("control-plane relay leaves = %v", server)
	}
	serverCert, serverKey := pki.LeafPaths(pki.DefaultRootDir, server[0])
	dropin, err := os.ReadFile(filepath.Join(roles, "control_plane_service", "templates", "99-faas-outbound-relay.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := os.ReadFile(filepath.Join(roles, "control_plane_service", "tasks", "outbound_relay.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FAAS_OUTBOUNDD_RELAY_TLS_CERT_PATH=" + serverCert,
		"FAAS_OUTBOUNDD_RELAY_TLS_KEY_PATH=" + serverKey,
		"FAAS_OUTBOUNDD_RELAY_TLS_CA_PATH=" + ca,
	} {
		if !strings.Contains(string(dropin), want) {
			t.Errorf("outboundd relay drop-in lacks %q", want)
		}
	}
	if !strings.Contains(string(tasks), "path: "+serverKey) {
		t.Errorf("outbound_relay.yml does not grant outboundd read access to %s", serverKey)
	}

	client := pki.FeatureRoles(pki.FeatureOutboundRelay, "compute-only")
	if len(client) != 1 {
		t.Fatalf("compute-only relay leaves = %v", client)
	}
	clientCert, clientKey := pki.LeafPaths(pki.DefaultRootDir, client[0])
	defaults := readRoleDefaults(t, filepath.Join(roles, "vmmd_service"))
	for name, want := range map[string]string{
		"vmmd_outbound_client_tls_cert_path": clientCert,
		"vmmd_outbound_client_tls_key_path":  clientKey,
		"vmmd_outbound_client_tls_ca_path":   ca,
	} {
		if got := yamlScalar(defaults[name]); got != want {
			t.Errorf("vmmd_service default %s = %q, want %q (what pkg/pki issues)", name, got, want)
		}
	}
	staged, err := os.ReadFile(filepath.Join(roles, "vmmd_service", "tasks", "outbound_relay_tls.yml"))
	if err != nil {
		t.Fatal(err)
	}
	stagedDir := "/" + client[0].Directory + "/{{ item }}"
	for _, want := range []string{"--feature, outbound-relay", stagedDir, "[" + client[0].Filename + ".crt, " + client[0].Filename + ".key]"} {
		if !strings.Contains(string(staged), want) {
			t.Errorf("outbound_relay_tls.yml lacks %q", want)
		}
	}
}
