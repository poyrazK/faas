package mcpmanagement

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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Opt-in acceptance runs the actual pinned Codex binary, with an isolated
// profile and real HTTPS/OAuth bridge. Test controls never enter production
// handlers. The API fixture performs no model or deployment operations.
func TestCodexRemoteOAuthAcceptance(t *testing.T) {
	binary := os.Getenv("GREGALE_MCP_CODEX_BINARY")
	if binary == "" {
		t.Skip("set GREGALE_MCP_CODEX_BINARY for real-client remote acceptance")
	}
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "browser", StateStore: store, RedirectURIs: []string{testRedirect, "http://127.0.0.1/callback/*"}})
	certificate, ca := codexFixtureCertificate(t)
	f.server.TLS.Certificates = []tls.Certificate{certificate}
	f.client.Transport.(*http.Transport).TLSClientConfig.RootCAs.AppendCertsFromPEM(ca)
	var mu sync.Mutex
	var latest tokenResponse
	refreshes := 0
	install := func() {
		h, err := NewHTTPHandler(f.opts)
		if err != nil {
			t.Fatal(err)
		}
		f.setHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path != "/oauth/token" {
				h.ServeHTTP(w, req)
				return
			}
			if err := req.ParseForm(); err != nil {
				t.Error(err)
				return
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code == 200 {
				var tokens tokenResponse
				if err := json.Unmarshal(response.Body.Bytes(), &tokens); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				latest = tokens
				if req.PostForm.Get("grant_type") == "refresh_token" {
					refreshes++
				}
				mu.Unlock()
			}
			for k, values := range response.Header() {
				w.Header()[k] = values
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
		}))
	}
	install()
	t.Cleanup(func() { _ = f.opts.StateStore.Close() })
	controlToken, err := randomToken("fixture_")
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "POST" || req.Header.Get("Authorization") != "Bearer "+controlToken {
			w.WriteHeader(403)
			return
		}
		switch req.URL.Path {
		case "/approve":
			var input struct {
				AuthorizationURL string `json:"authorization_url"`
			}
			if json.NewDecoder(req.Body).Decode(&input) != nil || !strings.HasPrefix(input.AuthorizationURL, f.server.URL+"/oauth/authorize?") {
				w.WriteHeader(400)
				return
			}
			flow := f.beginAt(req.Context(), input.AuthorizationURL)
			for _, decision := range []string{"start-login", "check-login", "approve"} {
				response := f.consent(flow, decision, "", f.server.URL, flow.csrf, false)
				_ = response.Body.Close()
				if decision == "approve" {
					if response.StatusCode != 303 {
						t.Errorf("consent=%d", response.StatusCode)
						w.WriteHeader(500)
						return
					}
					// Deliver the real authorization code to Codex's own callback.
					callbackRequest, err := http.NewRequestWithContext(req.Context(), http.MethodGet, response.Header.Get("Location"), nil)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					callback, err := http.DefaultClient.Do(callbackRequest)
					if err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					_ = callback.Body.Close()
				} else if response.StatusCode != 200 {
					t.Errorf("sign-in=%d", response.StatusCode)
					w.WriteHeader(500)
					return
				}
			}
		case "/restart", "/expire":
			if req.URL.Path == "/expire" {
				var snapshot oauthSnapshot
				if err := f.opts.StateStore.read("oauth", &snapshot); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				for id, grant := range snapshot.Grants {
					grant.AccessExpires = time.Now().Add(-time.Minute)
					snapshot.Grants[id] = grant
				}
				if err := f.opts.StateStore.update("oauth", snapshot); err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
			}
			if err := f.opts.StateStore.Close(); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			f.opts.StateStore = next
			install()
		case "/revoke":
			var snapshot oauthSnapshot
			if err := f.opts.StateStore.read("oauth", &snapshot); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			var clientID string
			for _, grant := range snapshot.Grants {
				if grant.Active {
					clientID = grant.ClientID
				}
			}
			mu.Lock()
			refresh := latest.RefreshToken
			mu.Unlock()
			response := f.request("POST", "/oauth/revoke", strings.NewReader(url.Values{"client_id": {clientID}, "token": {refresh}}.Encode()), "application/x-www-form-urlencoded", "")
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Errorf("revoke=%d", response.StatusCode)
				w.WriteHeader(500)
				return
			}
		case "/evidence":
			mu.Lock()
			defer mu.Unlock()
			writeOAuthJSON(w, 200, map[string]int{"refreshes": refreshes})
			return
		default:
			w.WriteHeader(404)
			return
		}
		writeOAuthJSON(w, 200, map[string]bool{"ok": true})
	}))
	t.Cleanup(control.Close)
	certPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(certPath, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/ops/management-mcp-client-acceptance.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "python3", script, "--remote-fixture", "--codex-binary", binary)
	if diagnostics := os.Getenv("GREGALE_MCP_CODEX_DIAGNOSTICS"); diagnostics != "" {
		command.Args = append(command.Args, "--diagnostics-file", diagnostics)
	}
	command.Env = append(os.Environ(), "SSL_CERT_FILE="+certPath, "CODEX_CA_CERTIFICATE="+certPath, "GREGALE_MCP_TEST_RESOURCE="+f.resource, "GREGALE_MCP_TEST_CONTROL="+control.URL, "GREGALE_MCP_TEST_CONTROL_TOKEN="+controlToken)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real Codex remote acceptance failed: %v\n%s", err, output)
	}
	var report map[string]any
	if json.Unmarshal(output, &report) != nil || report["revocation"] != "passed" || report["refresh"] != "passed" || report["restart"] != "passed" {
		t.Fatal("incomplete real-client evidence")
	}
	if reportPath := os.Getenv("GREGALE_MCP_CLIENT_REPORT"); reportPath != "" {
		if err := os.WriteFile(reportPath, output, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func codexFixtureCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Gregale acceptance CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
}
