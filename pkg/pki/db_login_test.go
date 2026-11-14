package pki

import (
	"crypto/tls"
	"crypto/x509"
	"regexp"
	"testing"
)

// adr: 20261110210000 — slice 1c-2: a compute node's database client leaves
// name each daemon's own login on that node. PostgreSQL's cert auth requires
// the CN to equal the user name, and compute_node_db_logins accepts only
// ^faas_[a-z0-9_]{1,58}$, so the names must fit that shape. node_join.yml
// derives the same names with Ansible's hash('sha256') filter; the literal
// below pins the derivation both sides use.
func TestDBLoginLeavesNameEachDaemonsNodeLogin(t *testing.T) {
	if got := DBNodeLogin("faas_vmmd", "compute-7"); got != "faas_vmmd_2a7d26cc3d0a" {
		t.Fatalf("DBNodeLogin = %q; node_join.yml's (name | hash('sha256'))[:12] must agree", got)
	}
	ca := t.TempDir()
	caCert, _, err := EnsureCA(ca, false)
	if err != nil {
		t.Fatal(err)
	}
	if roles := FeatureRoles(FeatureDBLogin, "control-plane"); len(roles) != 0 {
		t.Fatalf("control plane carries db-login leaves: %v", roles)
	}
	if _, err := EnsureFeatureLeavesForNode(ca, t.TempDir(), FeatureDBLogin, "compute-only", ""); err == nil {
		t.Fatal("db-login leaves issued without a node name")
	}
	out := t.TempDir()
	issued, err := EnsureFeatureLeavesForNode(ca, out, FeatureDBLogin, "compute-only", "compute-7.faas")
	if err != nil || len(issued) != len(DBLoginDaemonRoles) {
		t.Fatalf("issue = %v, %v", issued, err)
	}
	shape := regexp.MustCompile(`^faas_[a-z0-9_]{1,58}$`)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	seen := map[string]bool{}
	for _, role := range issued {
		certPath, keyPath := LeafPaths(out, role)
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		want := DBNodeLogin(DBLoginDaemonRoles[role.Filename], "compute-7")
		if leaf.Subject.CommonName != want || !shape.MatchString(want) || seen[want] {
			t.Errorf("%s: CN = %q, want unique login %q", role.Filename, leaf.Subject.CommonName, want)
		}
		seen[want] = true
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			t.Errorf("%s: %v", role.Filename, err)
		}
	}
	if again, err := EnsureFeatureLeavesForNode(ca, out, FeatureDBLogin, "compute-only", "compute-7.faas"); err != nil || len(again) != 0 {
		t.Fatalf("second run reissued %v, %v", again, err)
	}
}
