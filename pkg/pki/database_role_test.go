// adr: 1027
package pki

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A database node runs vmmd (database VM RPCs, under the node identity) and
// pgproxyd (schedd and vmmd client); nothing of the compute data path.
func TestRolesForDatabaseNode(t *testing.T) {
	var got []string
	for _, role := range RolesForBox("database") {
		got = append(got, role.Directory+"/"+role.Filename)
	}
	for _, want := range []string{"vmmd/server", "pgproxyd/schedd-client", "pgproxyd/vmmd-client"} {
		if !slices.Contains(got, want) {
			t.Errorf("database leaves %v lack %s", got, want)
		}
	}
	for _, leaf := range got {
		for _, forbidden := range []string{"gatewayd/", "builderd/", "imaged/", "egress/", "schedd/", "apid/"} {
			if len(leaf) >= len(forbidden) && leaf[:len(forbidden)] == forbidden {
				t.Errorf("database node would receive %s", leaf)
			}
		}
	}
	for _, role := range Roles() {
		if role.Directory == "pgproxyd" && (role.CommonName != "pgproxyd.faas" || role.Kind != KindClient) {
			t.Errorf("pgproxyd leaf %s: CN %s kind %v", role.Filename, role.CommonName, role.Kind)
		}
	}
	if !UsesNodeIdentity("database") || !UsesNodeIdentity("compute-only") || UsesNodeIdentity("control-plane") {
		t.Fatal("node identity applies to compute and database nodes only")
	}
}

func TestIssueTrustBundleForDatabaseNode(t *testing.T) {
	t.Parallel()
	issuer := t.TempDir()
	if _, _, err := EnsureCA(issuer, false); err != nil {
		t.Fatal(err)
	}
	if err := IssueTrustBundle(issuer, filepath.Join(t.TempDir(), "anonymous"), "database", "", AltNames{}); err == nil {
		t.Fatal("a database bundle without a node identity was issued")
	}
	san := AltNames{IPAddresses: []net.IP{net.ParseIP("100.64.7.1")}}
	bundle := filepath.Join(t.TempDir(), "db-1")
	if err := IssueTrustBundle(issuer, bundle, "database", "db-1.faas", san); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTrustBundleForNode(bundle, "database", san, "db-1.faas"); err != nil {
		t.Fatal(err)
	}
	_, caKey := CARoot(bundle)
	if _, err := os.Stat(caKey); !os.IsNotExist(err) {
		t.Fatal("database bundle contains the CA private key")
	}
	for _, role := range RolesForBox("database") {
		certPath, _ := LeafPaths(bundle, role)
		body, err := os.ReadFile(certPath)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(body)
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		want := role.CommonName
		if RoleUsesNodeIdentity(role) {
			want = "db-1.faas"
		}
		if cert.Subject.CommonName != want {
			t.Errorf("%s/%s CN = %s, want %s", role.Directory, role.Filename, cert.Subject.CommonName, want)
		}
	}
}
