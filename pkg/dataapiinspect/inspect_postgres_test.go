package dataapiinspect

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

// This test only creates a disposable local cluster; it never accepts a DSN.
func TestInspectionAgainstDisposableTLSPostgres(t *testing.T) {
	if os.Getenv("GREGALE_DATA_API_INSPECT_POSTGRES_TEST") != "1" {
		t.Skip("set GREGALE_DATA_API_INSPECT_POSTGRES_TEST=1 to run a disposable local PostgreSQL cluster")
	}
	bin := os.Getenv("GREGALE_TEST_POSTGRES_BIN")
	if bin == "" {
		t.Fatal("GREGALE_TEST_POSTGRES_BIN must point to PostgreSQL binaries")
	}
	dir, err := os.MkdirTemp("", "gregale-inspection-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(filepath.Join(bin, name), args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v: %s", name, err, out)
		}
	}
	data := filepath.Join(dir, "data")
	run("initdb", "-D", data, "-U", "postgres", "--auth=trust", "--no-locale")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	certDER, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certPath, keyPath := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	options := fmt.Sprintf("-h 127.0.0.1 -p %d -c unix_socket_directories='' -c ssl=on -c ssl_cert_file=%s -c ssl_key_file=%s", port, certPath, keyPath)
	run("pg_ctl", "-D", data, "-l", filepath.Join(dir, "postgres.log"), "-o", options, "-w", "start")
	t.Cleanup(func() {
		_ = exec.Command(filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "immediate", "-w", "stop").Run()
	})
	sql := `CREATE ROLE write_fixture LOGIN; CREATE SCHEMA api; CREATE TABLE api.notes(id integer PRIMARY KEY, body text, hidden text); ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY; CREATE POLICY own_notes ON api.notes FOR ALL TO write_fixture USING (true) WITH CHECK (true); CREATE FUNCTION api.observe_note() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$; CREATE TRIGGER observe_note BEFORE UPDATE ON api.notes FOR EACH ROW EXECUTE FUNCTION api.observe_note(); GRANT USAGE ON SCHEMA api TO write_fixture; GRANT SELECT(id), INSERT(body) ON api.notes TO write_fixture; INSERT INTO api.notes VALUES (1,'synthetic','private-row-marker');`
	run("psql", "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql)
	dsn := fmt.Sprintf("postgresql://write_fixture:test-password@127.0.0.1:%d/postgres?sslmode=verify-full", port)
	connect := func(ctx context.Context, config *pgx.ConnConfig) (*pgx.Conn, error) {
		return pgx.ConnectConfig(ctx, config)
	}
	nonce := strings.Repeat("a", 64)
	report, err := runInspectionForTest(t.Context(), dsn, "notes", nonce, certPEM, connect)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := TargetDigest(nonce, "127.0.0.1", uint16(port), "postgres", "write_fixture")
	trustDigest, err := dataapiinspectioncommand.TrustBundleFingerprint(certPEM)
	if err != nil || report.TrustBundleSHA256 != trustDigest || report.TargetDigest != digest || report.Role.Name != "write_fixture" || report.Relation.Name != "notes" || !report.Relation.RLS || len(report.Columns) != 3 || !report.Columns[1].Insert || report.Columns[2].Select || len(report.Policies) != 1 || !report.Policies[0].AppliesToRole || len(report.Triggers) != 1 || report.Triggers[0].Name != "observe_note" {
		t.Fatalf("incorrect catalog observation: %+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "private-row-marker") || strings.Contains(string(encoded), "test-password") || strings.Contains(string(encoded), "127.0.0.1") {
		t.Fatal("inspection output leaked private data")
	}
	parsed, err := ParseObservation(encoded, "notes")
	if err != nil || parsed.TargetDigest != digest || parsed.TrustBundleSHA256 != trustDigest {
		t.Fatalf("real observation did not pass evidence validation: %v", err)
	}
	if _, err := Run(t.Context(), dsn, "notes", nonce, testTrust(t)); err == nil {
		t.Fatal("accepted an untrusted TLS certificate")
	}
	if _, err := runInspectionForTest(t.Context(), dsn, "missing", nonce, certPEM, connect); err == nil {
		t.Fatal("accepted a missing table")
	}
	count := exec.Command(filepath.Join(bin, "psql"), "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "-d", "postgres", "-Atc", `SELECT count(*) FROM api.notes`)
	if out, err := count.Output(); err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Fatal("inspection changed fixture rows")
	}
	changes := make([]string, 126)
	for i := range changes {
		changes[i] = fmt.Sprintf("ADD COLUMN c%d integer", i)
	}
	run("psql", "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", "ALTER TABLE api.notes "+strings.Join(changes, ", "))
	if observation, err := runInspectionForTest(t.Context(), dsn, "notes", nonce, certPEM, connect); err == nil || observation.Version != 0 {
		t.Fatal("accepted a partial catalog above the column limit")
	}
	t.Setenv("PGSSLROOTCERT", certPath)
	called := false
	if _, err := runInspectionForTest(t.Context(), dsn, "notes", nonce, certPEM, func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
		called = true
		return nil, nil
	}); err == nil || called {
		t.Fatal("accepted a CA supplied through the process environment")
	}
}

func runInspectionForTest(ctx context.Context, dsn, table, nonce string, ca []byte, connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)) (Observation, error) {
	return run(ctx, dsn, table, nonce, ca, connect)
}
