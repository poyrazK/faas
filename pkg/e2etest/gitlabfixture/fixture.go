// Package gitlabfixture provides a loopback-only GitLab.com boundary for real
// daemon acceptance. The daemon keeps its fixed HTTPS origin, TLS verification,
// OAuth unsealing, repository identity and immutable archive checks.
package gitlabfixture

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/gitarchive"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const RepositoryID int64 = 123
const Repository = "example/shop"

type Fixture struct {
	mu            sync.Mutex
	head          string
	archives      map[string][]byte
	unavailable   bool
	connections   map[net.Conn]bool
	closed        bool
	server        *httptest.Server
	proxy         *httptest.Server
	caPath        string
	identity      *age.X25519Identity
	identityPath  string
	recipientPath string
	certDirectory string
	token         string
}

func New(t *testing.T) *Fixture {
	t.Helper()
	f := &Fixture{archives: map[string][]byte{}, connections: map[net.Conn]bool{}}
	dir := t.TempDir()
	f.caPath, f.identityPath, f.recipientPath = filepath.Join(dir, "provider-ca.pem"), filepath.Join(dir, "host.age"), filepath.Join(dir, "host.age.pub")
	f.certDirectory = t.TempDir()
	var err error
	f.identity, err = age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := secretbox.WriteHostKeyAtPath(f.identityPath, f.identity); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.recipientPath, []byte(f.identity.Recipient().String()), 0o400); err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	f.token = hex.EncodeToString(token)
	certificate := f.certificate(t)
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	f.server.Config.IdleTimeout = 30 * time.Second
	f.server.StartTLS()
	f.proxy = httptest.NewServer(http.HandlerFunc(f.tunnel))
	t.Cleanup(f.close)
	return f
}

func (f *Fixture) certificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "GitLab acceptance fixture"},
		DNSNames: []string{"gitlab.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o400); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// Env scopes the local CA and proxy to the acceptance daemon, including its
// restarts. It does not add an endpoint override or weaken TLS verification.
func (f *Fixture) Env() []string {
	return []string{"HTTPS_PROXY=" + f.proxy.URL, "HTTP_PROXY=", "ALL_PROXY=", "NO_PROXY=localhost,127.0.0.1,::1",
		"SSL_CERT_FILE=" + f.caPath, "SSL_CERT_DIR=" + f.certDirectory,
		"FAAS_FLEET_AGE_RECIPIENT_PATH=", "FAAS_HOST_AGE_IDENTITY_PATH=" + f.identityPath,
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + f.recipientPath}
}

func (f *Fixture) SealedTokens(t *testing.T) ([]byte, time.Time) {
	t.Helper()
	expires := time.Now().Add(4 * time.Hour)
	body, err := json.Marshal(gitlabsource.Tokens{Access: f.token, Refresh: "fixture-refresh", Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(f.identity.Recipient(), "gitlab-gitops-oauth", body, 16384)
	if err != nil {
		t.Fatal(err)
	}
	return sealed, expires
}

func (f *Fixture) Add(t *testing.T, sha string, definition api.EnvironmentDefinition) {
	t.Helper()
	f.AddFiles(t, sha, definition, nil)
}

// AddFiles serves the exact build context through the production archive endpoint.
func (f *Fixture) AddFiles(t *testing.T, sha string, definition api.EnvironmentDefinition, files map[string][]byte) {
	t.Helper()
	archive, err := gitarchive.Create(sha, definition, files)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if previous, exists := f.archives[sha]; exists && !bytes.Equal(previous, archive) {
		t.Fatal("fixture commit cannot replace immutable archive bytes")
	}
	f.archives[sha], f.head = bytes.Clone(archive), sha
}

func (f *Fixture) SetUnavailable(value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = value
}

func (f *Fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodGet || r.Host != "gitlab.com" || r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, "fixture scope denied", http.StatusForbidden)
		return
	}
	if f.unavailable {
		http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v4/projects/" + Repository:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": RepositoryID, "path_with_namespace": Repository})
	case "/api/v4/projects/123/repository/branches/main":
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"id": f.head}})
	case "/api/v4/projects/123/repository/archive.tar.gz":
		archive, ok := f.archives[r.URL.Query().Get("sha")]
		if !ok || r.URL.Query().Get("include_lfs_blobs") != "false" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	default:
		sha, ok := strings.CutPrefix(r.URL.Path, "/api/v4/projects/123/repository/commits/")
		if _, exists := f.archives[sha]; !ok || !exists {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": sha})
	}
}

// The proxy never resolves or dials the CONNECT target. Only the fixed GitLab
// authority can reach this fixture's own loopback listener.
func (f *Fixture) tunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || r.Host != "gitlab.com:443" {
		http.Error(w, "fixture origin denied", http.StatusForbidden)
		return
	}
	upstream, err := net.DialTimeout("tcp", f.server.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		http.Error(w, "fixture unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	client, buffered, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.connections[client], f.connections[upstream] = true, true
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.connections, client); delete(f.connections, upstream); f.mu.Unlock() }()
	if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close(); close(done) }()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

func (f *Fixture) close() {
	f.mu.Lock()
	f.closed = true
	for connection := range f.connections {
		_ = connection.Close()
	}
	f.mu.Unlock()
	f.proxy.Close()
	f.server.Close()
}
