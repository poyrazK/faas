package runtimefence

// adr: 623

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type deliveryPKI struct {
	ca        *x509.Certificate
	caKey     ed25519.PrivateKey
	roots     []byte
	server    tls.Certificate
	clientPEM []byte
	clientKey []byte
	serial    int64
}

func newDeliveryPKI(t *testing.T) *deliveryPKI {
	t.Helper()
	public, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-only receipt CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(nil, ca, ca, public, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p := &deliveryPKI{ca: ca, caKey: key, roots: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), serial: 1}
	p.server, _, _ = p.issue(t, &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	_, p.clientPEM, p.clientKey = p.issue(t, &x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return p
}

func (p *deliveryPKI) issue(t *testing.T, template *x509.Certificate) (tls.Certificate, []byte, []byte) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	p.serial++
	template.SerialNumber = big.NewInt(p.serial)
	template.Subject = pkix.Name{CommonName: "private test credential"}
	template.KeyUsage = x509.KeyUsageDigitalSignature
	if template.NotBefore.IsZero() {
		template.NotBefore, template.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	}
	der, err := x509.CreateCertificate(nil, template, p.ca, public, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM, keyPEM
}

func (p *deliveryPKI) serverConfig() *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(p.ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, Certificates: []tls.Certificate{p.server}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
}

func (p *deliveryPKI) clientConfig(endpoint string, intent Intent, key ed25519.PrivateKey) ReceiptClientConfig {
	hash := sha256.Sum256(p.server.Leaf.RawSubjectPublicKeyInfo)
	return ReceiptClientConfig{Endpoint: endpoint + ReceiptLookupPath, AuthorityID: intent.AuthorityID, AuthorityPublicKey: key.Public().(ed25519.PublicKey), ServerSPKISHA256: hex.EncodeToString(hash[:]), ServerRootsPEM: slices.Clone(p.roots), ClientCertificatePEM: slices.Clone(p.clientPEM), ClientPrivateKeyPEM: slices.Clone(p.clientKey)}
}

func deliveryServer(t *testing.T, config *tls.Config, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = config
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func deliveryClient(t *testing.T, config ReceiptClientConfig) *ReceiptClient {
	t.Helper()
	c, err := NewReceiptClient(config)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeDelivery(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", ReceiptMediaType)
	_, _ = w.Write(raw)
}

func TestReceiptDeliveryAuthenticatedIntentAndOwnedCredentials(t *testing.T) {
	_, intent, claim, key, _ := fenceFixture(t)
	raw := signedEnvelope(t, claim, key, SignatureDomain)
	pki := newDeliveryPKI(t)
	var calls atomic.Int32
	s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		got, err := io.ReadAll(r.Body)
		want, _ := json.Marshal(intent)
		if err != nil || !bytes.Equal(got, want) || r.Method != http.MethodPost || r.URL.String() != ReceiptLookupPath || r.Header.Get("Content-Type") != IntentMediaType || r.Header.Get("Accept") != ReceiptMediaType || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Accept-Encoding") != "" || r.Header.Get("Cookie") != "" || r.TLS.Version != tls.VersionTLS13 || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) != 1 {
			t.Error("lookup did not preserve exact intent and authenticated transport", err)
		}
		writeDelivery(w, raw)
	})
	config := pki.clientConfig(s.URL, intent, key)
	c := deliveryClient(t, config)
	for _, input := range [][]byte{config.AuthorityPublicKey, config.ServerRootsPEM, config.ClientCertificatePEM, config.ClientPrivateKeyPEM} {
		clear(input)
	}
	for range 2 {
		delivery, err := c.Lookup(context.Background(), intent)
		if err != nil || !bytes.Equal(delivery.Envelope(), raw) {
			t.Fatal("lookup failed after caller changed constructor inputs", err)
		}
		copyOut := delivery.Envelope()
		copyOut[0] = '!'
		if !bytes.Equal(delivery.Envelope(), raw) {
			t.Fatal("caller changed retained delivery")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected retry", calls.Load())
	}
}

func TestReceiptDeliveryRejectsInvalidConfiguration(t *testing.T) {
	_, intent, _, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	base := pki.clientConfig("https://authority.test", intent, key)
	cases := map[string]func(*ReceiptClientConfig){
		"HTTP":           func(c *ReceiptClientConfig) { c.Endpoint = "http://authority.test" + ReceiptLookupPath },
		"userinfo":       func(c *ReceiptClientConfig) { c.Endpoint = "https://secret@authority.test" + ReceiptLookupPath },
		"query":          func(c *ReceiptClientConfig) { c.Endpoint += "?secret=value" },
		"empty-query":    func(c *ReceiptClientConfig) { c.Endpoint += "?" },
		"fragment":       func(c *ReceiptClientConfig) { c.Endpoint += "#secret" },
		"empty-fragment": func(c *ReceiptClientConfig) { c.Endpoint += "#" },
		"wrong-route":    func(c *ReceiptClientConfig) { c.Endpoint += ":terminate" },
		"encoded-route": func(c *ReceiptClientConfig) {
			c.Endpoint = strings.Replace(c.Endpoint, "runtime-upgrade", "%72untime-upgrade", 1)
		},
		"upper-host":   func(c *ReceiptClientConfig) { c.Endpoint = strings.Replace(c.Endpoint, "authority", "AUTHORITY", 1) },
		"host-dot":     func(c *ReceiptClientConfig) { c.Endpoint = "https://authority.test." + ReceiptLookupPath },
		"zero-port":    func(c *ReceiptClientConfig) { c.Endpoint = "https://authority.test:0" + ReceiptLookupPath },
		"leading-port": func(c *ReceiptClientConfig) { c.Endpoint = "https://authority.test:0443" + ReceiptLookupPath },
		"empty-port":   func(c *ReceiptClientConfig) { c.Endpoint = "https://authority.test:" + ReceiptLookupPath },
		"large-port":   func(c *ReceiptClientConfig) { c.Endpoint = "https://authority.test:65536" + ReceiptLookupPath },
		"IPv6-zone":    func(c *ReceiptClientConfig) { c.Endpoint = "https://[fe80::1%25en0]" + ReceiptLookupPath },
		"mapped-IP":    func(c *ReceiptClientConfig) { c.Endpoint = "https://[::ffff:127.0.0.1]" + ReceiptLookupPath },
		"unspecified":  func(c *ReceiptClientConfig) { c.Endpoint = "https://0.0.0.0" + ReceiptLookupPath },
		"endpoint-bound": func(c *ReceiptClientConfig) {
			c.Endpoint = strings.Repeat("a", api.RuntimeUpgradeExternalFenceEndpointMaxBytes+1)
		},
		"authority":   func(c *ReceiptClientConfig) { c.AuthorityID = "bad" },
		"receipt-key": func(c *ReceiptClientConfig) { c.AuthorityPublicKey = make([]byte, 32) },
		"no-pin":      func(c *ReceiptClientConfig) { c.ServerSPKISHA256 = "" },
		"upper-pin":   func(c *ReceiptClientConfig) { c.ServerSPKISHA256 = strings.Repeat("A", 64) },
		"no-roots":    func(c *ReceiptClientConfig) { c.ServerRootsPEM = nil },
		"junk-roots": func(c *ReceiptClientConfig) {
			c.ServerRootsPEM = append(slices.Clone(c.ServerRootsPEM), []byte("junk")...)
		},
		"non-CA-root": func(c *ReceiptClientConfig) { c.ServerRootsPEM = slices.Clone(pki.clientPEM) },
		"roots-bound": func(c *ReceiptClientConfig) {
			c.ServerRootsPEM = bytes.Repeat([]byte("a"), api.RuntimeUpgradeExternalFenceTLSMaterialMaxBytes+1)
		},
		"no-client-cert": func(c *ReceiptClientConfig) { c.ClientCertificatePEM = nil },
		"cert-bound": func(c *ReceiptClientConfig) {
			c.ClientCertificatePEM = bytes.Repeat([]byte("a"), api.RuntimeUpgradeExternalFenceTLSMaterialMaxBytes+1)
		},
		"no-client-key":  func(c *ReceiptClientConfig) { c.ClientPrivateKeyPEM = nil },
		"bad-client-key": func(c *ReceiptClientConfig) { c.ClientPrivateKeyPEM = []byte("secret key value") },
		"key-bound": func(c *ReceiptClientConfig) {
			c.ClientPrivateKeyPEM = bytes.Repeat([]byte("a"), api.RuntimeUpgradeExternalFenceTLSMaterialMaxBytes+1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			c, err := NewReceiptClient(config)
			if c != nil || !errors.Is(err, ErrDelivery) || strings.Contains(err.Error(), "secret") {
				t.Fatal(c, err)
			}
		})
	}
	for _, host := range []string{"authority.test", "127.0.0.1:443", "[::1]:8443"} {
		config := base
		config.Endpoint = "https://" + host + ReceiptLookupPath
		if _, err := NewReceiptClient(config); err != nil {
			t.Fatal("canonical endpoint", host, err)
		}
	}
}

func TestReceiptDeliveryRefusesUntrustedOrUnauthenticatedTLS(t *testing.T) {
	for _, name := range []string{"wrong-pin", "untrusted-CA", "wrong-hostname", "expired-server", "rotated-server-key", "missing-client-auth", "untrusted-client", "expired-client", "TLS12"} {
		t.Run(name, func(t *testing.T) {
			_, intent, claim, key, _ := fenceFixture(t)
			pki := newDeliveryPKI(t)
			serverConfig := pki.serverConfig()
			var calls atomic.Int32
			if name == "missing-client-auth" {
				serverConfig.ClientAuth = tls.NoClientCert
			}
			if name == "untrusted-client" {
				serverConfig.ClientCAs = x509.NewCertPool()
				serverConfig.ClientCAs.AddCert(newDeliveryPKI(t).ca)
			}
			if name == "TLS12" {
				serverConfig.MinVersion, serverConfig.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			}
			if name == "wrong-hostname" || name == "expired-server" || name == "rotated-server-key" {
				template := &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
				if name == "wrong-hostname" {
					template.IPAddresses = []net.IP{net.ParseIP("192.0.2.1")}
				}
				if name == "expired-server" {
					template.NotBefore, template.NotAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
				}
				serverConfig.Certificates[0], _, _ = pki.issue(t, template)
			}
			s := deliveryServer(t, serverConfig, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writeDelivery(w, signedEnvelope(t, claim, key, SignatureDomain))
			})
			config := pki.clientConfig(s.URL, intent, key)
			if name == "wrong-hostname" || name == "expired-server" {
				hash := sha256.Sum256(serverConfig.Certificates[0].Leaf.RawSubjectPublicKeyInfo)
				config.ServerSPKISHA256 = hex.EncodeToString(hash[:])
			}
			if name == "wrong-pin" {
				config.ServerSPKISHA256 = strings.Repeat("0", 64)
			}
			if name == "untrusted-CA" {
				config.ServerRootsPEM = newDeliveryPKI(t).roots
			}
			if name == "expired-client" {
				_, config.ClientCertificatePEM, config.ClientPrivateKeyPEM = pki.issue(t, &x509.Certificate{NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: time.Now().Add(-time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
			}
			got, err := deliveryClient(t, config).Lookup(context.Background(), intent)
			if !errors.Is(err, ErrDelivery) || len(got.Envelope()) != 0 || calls.Load() != 0 || strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "private test credential") {
				t.Fatal("untrusted handshake transmitted intent or exposed peer details", err, calls.Load())
			}
		})
	}
}

func TestReceiptDeliveryRejectsInvalidOrUnboundedResponse(t *testing.T) {
	for _, name := range []string{"wrong-type", "duplicate-type", "compressed", "empty-encoding", "empty", "large-length", "large-chunked", "header-bound", "truncated", "trailer", "signature", "other-intent", "temporary-contract", "noncanonical"} {
		t.Run(name, func(t *testing.T) {
			_, intent, claim, key, _ := fenceFixture(t)
			pki := newDeliveryPKI(t)
			raw := signedEnvelope(t, claim, key, SignatureDomain)
			s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ReceiptMediaType)
				switch name {
				case "wrong-type":
					w.Header().Set("Content-Type", "application/json")
				case "duplicate-type":
					w.Header().Add("Content-Type", ReceiptMediaType)
				case "compressed":
					w.Header().Set("Content-Encoding", "gzip")
				case "empty-encoding":
					w.Header().Set("Content-Encoding", "")
				case "empty":
					raw = nil
				case "large-length":
					w.Header().Set("Content-Length", strconv.Itoa(api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes+1))
				case "large-chunked":
					w.(http.Flusher).Flush()
					raw = bytes.Repeat([]byte("a"), api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes+1)
				case "header-bound":
					w.Header().Set("X-Too-Large", strings.Repeat("a", api.RuntimeUpgradeExternalFenceHeaderMaxBytes+1))
				case "truncated":
					w.Header().Set("Content-Length", strconv.Itoa(len(raw)+1))
				case "trailer":
					w.Header().Set("Trailer", "X-After")
					w.Header().Set("X-After", "changed")
				case "signature":
					_, other, err := ed25519.GenerateKey(nil)
					if err != nil {
						t.Error(err)
						return
					}
					raw = signedEnvelope(t, claim, other, SignatureDomain)
				case "other-intent":
					claim.IntentID = uuid.NewString()
					raw = signedEnvelope(t, claim, key, SignatureDomain)
				case "temporary-contract":
					claim.Contract = "host_powered_off"
					raw = signedEnvelope(t, claim, key, SignatureDomain)
				case "noncanonical":
					raw = append(raw, '\n')
				}
				_, _ = io.ReadAll(r.Body)
				_, _ = w.Write(raw)
			})
			got, err := deliveryClient(t, pki.clientConfig(s.URL, intent, key)).Lookup(context.Background(), intent)
			if !errors.Is(err, ErrDelivery) || len(got.Envelope()) != 0 {
				t.Fatal("invalid response supplied evidence", err)
			}
		})
	}
}

func TestReceiptDeliveryPendingAndErrorsNeverSupplyEvidence(t *testing.T) {
	for _, status := range []int{202, 404, 204, 401, 403, 409, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			_, intent, claim, key, _ := fenceFixture(t)
			pki := newDeliveryPKI(t)
			s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				writeDelivery(w, signedEnvelope(t, claim, key, SignatureDomain))
			})
			got, err := deliveryClient(t, pki.clientConfig(s.URL, intent, key)).Lookup(context.Background(), intent)
			want := ErrDelivery
			if status == 202 || status == 404 {
				want = ErrReceiptPending
			}
			if !errors.Is(err, want) || len(got.Envelope()) != 0 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestReceiptDeliveryNeverFollowsRedirects(t *testing.T) {
	_, intent, _, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/secret", status) })
			got, err := deliveryClient(t, pki.clientConfig(s.URL, intent, key)).Lookup(context.Background(), intent)
			if !errors.Is(err, ErrDelivery) || len(got.Envelope()) != 0 || followed.Load() != 0 {
				t.Fatal("redirect transmitted review", err, followed.Load())
			}
		})
	}
}

func TestReceiptDeliveryCallerRetryPreservesOriginalProofAfterDisconnect(t *testing.T) {
	_, intent, claim, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	raw := signedEnvelope(t, claim, key, SignatureDomain)
	var calls atomic.Int32
	s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer connection.Close()
			_, _ = fmt.Fprintf(connection, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s", ReceiptMediaType, len(raw), raw[:len(raw)/2])
			return
		}
		writeDelivery(w, raw)
	})
	c := deliveryClient(t, pki.clientConfig(s.URL, intent, key))
	got, err := c.Lookup(context.Background(), intent)
	if !errors.Is(err, ErrDelivery) || len(got.Envelope()) != 0 || calls.Load() != 1 {
		t.Fatal("partial evidence or implicit retry", err, calls.Load())
	}
	// A fresh client represents restart with the same reviewed immutable inputs.
	c = deliveryClient(t, pki.clientConfig(s.URL, intent, key))
	got, err = c.Lookup(context.Background(), intent)
	if err != nil || !bytes.Equal(got.Envelope(), raw) || calls.Load() != 2 {
		t.Fatal("restart replaced the original receipt", err, calls.Load())
	}
}

func TestReceiptDeliveryHistoricalAndFutureBytesCannotBypassFirstAcceptance(t *testing.T) {
	for _, kind := range []string{"historical", "future"} {
		t.Run(kind, func(t *testing.T) {
			verifier, intent, claim, key, now := fenceFixture(t)
			if kind == "historical" {
				claim.EnforcedAtMicros = intent.CreatedAtMicros
				claim.IssuedAtMicros = now.Add(-api.RuntimeUpgradeExternalFenceMaxAge - time.Second).UnixMicro()
			} else {
				claim.IssuedAtMicros = now.Add(time.Minute).UnixMicro()
			}
			raw := signedEnvelope(t, claim, key, SignatureDomain)
			pki := newDeliveryPKI(t)
			s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, _ *http.Request) { writeDelivery(w, raw) })
			got, err := deliveryClient(t, pki.clientConfig(s.URL, intent, key)).Lookup(context.Background(), intent)
			if err != nil || !bytes.Equal(got.Envelope(), raw) {
				t.Fatal("lookup refreshed or discarded historical bytes", err)
			}
			if accepted, err := verifier.Verify(intent, now, got.Envelope()); !errors.Is(err, ErrUnverified) || len(accepted.Envelope()) != 0 {
				t.Fatal("delivery authorized first acceptance without database clock", err)
			}
		})
	}
}

func TestReceiptDeliveryConcurrentRequestsEachAuthenticateFreshConnection(t *testing.T) {
	_, intent, claim, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	raw := signedEnvelope(t, claim, key, SignatureDomain)
	var mu sync.Mutex
	connections := make(map[string]bool)
	s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if connections[r.RemoteAddr] || r.TLS.DidResume || len(r.TLS.VerifiedChains) == 0 {
			t.Error("connection authentication was reused or missing")
		}
		connections[r.RemoteAddr] = true
		mu.Unlock()
		writeDelivery(w, raw)
	})
	c := deliveryClient(t, pki.clientConfig(s.URL, intent, key))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			got, err := c.Lookup(context.Background(), intent)
			if err != nil || !bytes.Equal(got.Envelope(), raw) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(connections) != 12 {
		t.Fatal("requests did not use twelve distinct authenticated connections", len(connections))
	}
}

func TestReceiptDeliveryCancellationBeforeNetworkAndDuringResponse(t *testing.T) {
	for _, kind := range []string{"before", "headers", "body"} {
		t.Run(kind, func(t *testing.T) {
			_, intent, claim, key, _ := fenceFixture(t)
			pki := newDeliveryPKI(t)
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.ReadAll(r.Body)
				if kind == "body" {
					w.Header().Set("Content-Type", ReceiptMediaType)
					w.(http.Flusher).Flush()
					_, _ = w.Write([]byte("{"))
					w.(http.Flusher).Flush()
				}
				close(started)
				<-release
				writeDelivery(w, signedEnvelope(t, claim, key, SignatureDomain))
			})
			defer close(release)
			c := deliveryClient(t, pki.clientConfig(s.URL, intent, key))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "before" {
				cancel()
			}
			result := make(chan error, 1)
			go func() {
				got, err := c.Lookup(ctx, intent)
				if len(got.Envelope()) != 0 {
					t.Error("cancellation supplied evidence")
				}
				result <- err
			}()
			if kind != "before" {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("request did not start")
				}
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, ErrDelivery) || !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost its cause", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled request stayed blocked")
			}
			if kind == "before" && calls.Load() != 0 {
				t.Fatal("cancelled context transmitted intent")
			}
		})
	}
}

func TestReceiptDeliveryRejectsInvalidIntentBeforeNetwork(t *testing.T) {
	_, intent, _, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	var calls atomic.Int32
	s := deliveryServer(t, pki.serverConfig(), func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	c := deliveryClient(t, pki.clientConfig(s.URL, intent, key))
	for _, mutate := range []func(*Intent){func(i *Intent) { i.AuthorityID = uuid.NewString() }, func(i *Intent) { i.SessionID = "bad" }, func(i *Intent) { i.ResourceID = "secret value" }} {
		bad := intent
		mutate(&bad)
		got, err := c.Lookup(context.Background(), bad)
		if !errors.Is(err, ErrDelivery) || len(got.Envelope()) != 0 || calls.Load() != 0 {
			t.Fatal("invalid intent transmitted", err)
		}
	}
	for _, bad := range []*ReceiptClient{nil, {}} {
		if _, err := bad.Lookup(context.Background(), intent); !errors.Is(err, ErrDelivery) {
			t.Fatal(err)
		}
	}
	if _, err := c.Lookup(nil, intent); !errors.Is(err, ErrDelivery) {
		t.Fatal(err)
	}
}

func TestReceiptDeliveryBoundedConnectionsAndQueuedDeadline(t *testing.T) {
	_, intent, claim, key, _ := fenceFixture(t)
	pki := newDeliveryPKI(t)
	raw := signedEnvelope(t, claim, key, SignatureDomain)
	started, release := make(chan struct{}, api.RuntimeUpgradeExternalFenceDeliveryConnections), make(chan struct{})
	var calls atomic.Int32
	s := deliveryServer(t, pki.serverConfig(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		calls.Add(1)
		started <- struct{}{}
		<-release
		writeDelivery(w, raw)
	})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	c := deliveryClient(t, pki.clientConfig(s.URL, intent, key))
	results := make(chan error, api.RuntimeUpgradeExternalFenceDeliveryConnections)
	for range api.RuntimeUpgradeExternalFenceDeliveryConnections {
		go func() {
			_, err := c.Lookup(context.Background(), intent)
			results <- err
		}()
	}
	for range api.RuntimeUpgradeExternalFenceDeliveryConnections {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("authenticated requests did not reach server")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got, err := c.Lookup(ctx, intent)
	if !errors.Is(err, ErrDelivery) || !errors.Is(err, context.DeadlineExceeded) || len(got.Envelope()) != 0 || calls.Load() != api.RuntimeUpgradeExternalFenceDeliveryConnections {
		t.Fatal("queued request exceeded connection budget or lost deadline", err, calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	for range api.RuntimeUpgradeExternalFenceDeliveryConnections {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != api.RuntimeUpgradeExternalFenceDeliveryConnections {
		t.Fatal("expired queued request was transmitted later")
	}
}
