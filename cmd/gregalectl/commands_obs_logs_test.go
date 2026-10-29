package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// obsLogsTestEnv isolates a test from the host's Loki settings.
func obsLogsTestEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"FAAS_LOKI_URL", "FAAS_LOKI_SERVER_NAME", "FAAS_LOKI_TENANT", "FAAS_LOKI_CA_FILE", "FAAS_LOKI_CERT_FILE", "FAAS_LOKI_KEY_FILE"} {
		t.Setenv(k, "")
	}
	old := lokiClientEnvFile
	lokiClientEnvFile = filepath.Join(t.TempDir(), "absent.env")
	t.Cleanup(func() { lokiClientEnvFile = old })
}

func TestObsLogsLogQL(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts obsLogsOptions
		want string
	}{
		{"no filters", obsLogsOptions{}, `{job="faas-journal"}`},
		{"wake across hosts", obsLogsOptions{wakeID: "0193a1b2-c3d4"}, `{job="faas-journal"} |= "0193a1b2-c3d4"`},
		{"short unit name", obsLogsOptions{unit: "schedd", host: "fsn-3", level: "ERROR"}, `{job="faas-journal",host="fsn-3",unit="faas-schedd.service",level="ERROR"}`},
		{"full unit name kept", obsLogsOptions{unit: "postgresql@16-main.service"}, `{job="faas-journal",unit="postgresql@16-main.service"}`},
		{"customer app records", obsLogsOptions{app: "app_1", account: "acct_2"}, `{job="faas-journal",app_id="app_1",account_id="acct_2"}`},
		{"ids and text combine", obsLogsOptions{requestID: "req-1", traceID: "abc123", grep: `say "hi"`}, `{job="faas-journal"} |= "req-1" |= "abc123" |= "say \"hi\""`},
		{"raw query wins", obsLogsOptions{query: `sum(rate({level="ERROR"}[5m]))`, host: "ignored"}, `sum(rate({level="ERROR"}[5m]))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.opts.logQL(); got != tc.want {
				t.Fatalf("logQL = %s, want %s", got, tc.want)
			}
		})
	}
}

func lokiStreamsBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "streams",
			"result": []map[string]any{
				{"stream": map[string]string{"host": "fsn-3", "unit": "faas-vmmd.service", "level": "INFO"},
					"values": [][2]string{{"1792760000200000000", `{"time":"x","level":"INFO","msg":"restore done","wake_id":"w-1","phase_ms":38}`}}},
				{"stream": map[string]string{"host": "cp", "unit": "faas-schedd.service", "level": "INFO"},
					"values": [][2]string{{"1792760000100000000", `{"level":"INFO","msg":"wake admitted","wake_id":"w-1","app":"my app"}`}}},
				{"stream": map[string]string{"host": "cp", "unit": "postgresql@16-main.service"},
					"values": [][2]string{{"1792760000300000000", "LOG:  checkpoint complete"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestCmdObsLogsPrintsOneTimelineAcrossHosts(t *testing.T) {
	obsLogsTestEnv(t)
	out, stderr := obsBundleTestStreams(t)
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write(lokiStreamsBody(t))
	}))
	defer srv.Close()

	code := cmdObsDispatch([]string{subObsLogs, "--loki-url", srv.URL, "--wake-id", "w-1",
		"--from", "2026-10-23T10:00:00Z", "--to", "2026-10-23T11:00:00Z", "--limit", "50"})
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	q := got.URL.Query()
	if got.URL.Path != "/loki/api/v1/query_range" || got.Header.Get("X-Scope-OrgID") != "gregale" ||
		q.Get("query") != `{job="faas-journal"} |= "w-1"` || q.Get("limit") != "50" || q.Get("direction") != "backward" ||
		q.Get("start") != "1792749600000000000" || q.Get("end") != "1792753200000000000" {
		t.Fatalf("request: %s %s", got.URL, got.Header)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{
		`2026-10-23T12:53:20.100Z cp schedd INFO wake admitted app="my app" wake_id=w-1`,
		`2026-10-23T12:53:20.200Z fsn-3 vmmd INFO restore done phase_ms=38 wake_id=w-1`,
		`2026-10-23T12:53:20.300Z cp postgresql@16-main LOG:  checkpoint complete`,
	}
	if len(lines) != len(want) {
		t.Fatalf("output:\n%s", out)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i, lines[i], want[i])
		}
	}

	out.Reset()
	if code := cmdObsLogs([]string{"--loki-url", srv.URL, "--forward", "--json"}); code != 0 {
		t.Fatalf("json code %d: %s", code, stderr)
	}
	if got.URL.Query().Get("direction") != "forward" {
		t.Fatalf("direction: %s", got.URL)
	}
	var doc struct {
		Query   string        `json:"query"`
		Entries []obsLogEntry `json:"entries"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Entries) != 3 || doc.Entries[0].Labels["host"] != "cp" || doc.Query != `{job="faas-journal"}` {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestCmdObsLogsReadsTheControlPlaneEnvFile(t *testing.T) {
	obsLogsTestEnv(t)
	out, stderr := obsBundleTestStreams(t)
	tenant := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Scope-OrgID")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer srv.Close()
	lokiClientEnvFile = filepath.Join(t.TempDir(), "loki-client.env")
	env := "# rendered by roles/loki\nFAAS_LOKI_URL=" + srv.URL + "\nFAAS_LOKI_TENANT=\"ops\"\n"
	if err := os.WriteFile(lokiClientEnvFile, []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := cmdObsLogs(nil); code != 0 || tenant != "ops" {
		t.Fatalf("code %d tenant %q: %s", code, tenant, stderr)
	}
	if out.Len() != 0 || !strings.Contains(stderr.String(), "no matching lines") {
		t.Fatalf("empty result: out=%q err=%q", out, stderr)
	}
}

func TestCmdObsLogsRejectsBadInputBeforeHTTP(t *testing.T) {
	obsLogsTestEnv(t)
	_, stderr := obsBundleTestStreams(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	for _, args := range [][]string{
		{"extra"},
		{"--limit", "0"},
		{"--limit", "5001"},
		{"--level", "fatal"},
		{"--wake-id", "a b"},
		{"--host", `fsn-3"}`},
		{"--since", "15d"},
		{"--since", "400h"},
		{"--from", "yesterday"},
		{"--from", "2026-10-23T11:00:00Z", "--to", "2026-10-23T10:00:00Z"},
	} {
		if code := cmdObsLogs(append([]string{"--loki-url", srv.URL}, args...)); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	if code := cmdObsLogs(nil); code != 2 || !strings.Contains(stderr.String(), "faas_obs_logs_enabled") {
		t.Errorf("no URL: code %d: %s", code, stderr)
	}
	if code := cmdObsLogs([]string{"--loki-url", "https://10.0.0.2:3100"}); code != 2 || !strings.Contains(stderr.String(), "--loki-cert") {
		t.Errorf("https without certificates: code %d: %s", code, stderr)
	}
	if calls != 0 {
		t.Fatalf("%d HTTP calls for invalid input", calls)
	}
}

func TestCmdObsLogsShowsLokiQueryErrors(t *testing.T) {
	obsLogsTestEnv(t)
	_, stderr := obsBundleTestStreams(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("parse error at line 1, col 8: syntax error"))
	}))
	defer srv.Close()
	if code := cmdObsLogs([]string{"--loki-url", srv.URL, "--query", "{job="}); code != 1 || !strings.Contains(stderr.String(), "HTTP 400: parse error at line 1") {
		t.Fatalf("code %d: %s", code, stderr)
	}
}

// TestCmdObsLogsUsesMutualTLS proves the client presents its certificate and
// verifies Loki's against the configured CA and server name, as the
// production listener requires (RequireAndVerifyClientCert).
func TestCmdObsLogsUsesMutualTLS(t *testing.T) {
	obsLogsTestEnv(t)
	_, stderr := obsBundleTestStreams(t)
	dir := t.TempDir()
	caCert, caKey := obsLogsTestCA(t)
	serverPair := obsLogsTestLeaf(t, caCert, caKey, "loki.faas", x509.ExtKeyUsageServerAuth)
	clientPair := obsLogsTestLeaf(t, caCert, caKey, "prometheus.faas", x509.ExtKeyUsageClientAuth)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "prometheus.faas" {
			t.Errorf("client certificate missing")
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{serverPair.tls}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()

	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ca := write("ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw}))
	crt, key := write("client.crt", clientPair.certPEM), write("client.key", clientPair.keyPEM)
	base := []string{"--loki-url", srv.URL, "--loki-ca", ca, "--loki-cert", crt, "--loki-key", key}

	if code := cmdObsLogs(append(base, "--loki-server-name", "loki.faas")); code != 0 {
		t.Fatalf("mTLS: code %d: %s", code, stderr)
	}
	stderr.Reset()
	if code := cmdObsLogs(append(base, "--loki-server-name", "other.faas")); code != 1 || !strings.Contains(stderr.String(), "certificate") {
		t.Fatalf("wrong server name must fail verification: code %d: %s", code, stderr)
	}
}

type obsLogsTestPair struct {
	tls             tls.Certificate
	certPEM, keyPEM []byte
}

func obsLogsTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-loki-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func obsLogsTestLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, usage x509.ExtKeyUsage) obsLogsTestPair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
	if usage == x509.ExtKeyUsageServerAuth {
		tpl.DNSNames = []string{cn}
		tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return obsLogsTestPair{tls: pair, certPEM: certPEM, keyPEM: keyPEM}
}
