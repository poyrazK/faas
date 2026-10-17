// Package storagefixture observes the production S3 gateway without changing
// signing or credential authority. Faults affect only this owned test listener.
package storagefixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type connectionKey struct{}

// Observation contains no signing secret or raw authorization header. AccessKey
// is private fixture metadata and must not be copied into qualification reports.
type Observation struct {
	Connection uint64
	AccessKey  string
	Observer   bool
	Method     string
	Path       string
	Listing    bool
	Status     int
}

type ListFault struct {
	Entered   chan struct{}
	Cancelled chan struct{}
	Done      chan struct{}
	delay     time.Duration
}

type Gateway struct {
	URL string
	CA  []byte

	server  *httptest.Server
	mu      sync.Mutex
	handler http.Handler
	nextID  uint64
	next    *ListFault
	records []Observation
}

// NewGateway generates an ephemeral certificate for the explicit listener IP.
// Guests trust this public certificate through NODE_EXTRA_CA_CERTS; TLS checking
// remains enabled. A native runner owns the benchmark IP before calling this.
func NewGateway(t *testing.T, address string) *Gateway {
	t.Helper()
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("cannot bind owned storage gateway listener")
	}
	t.Cleanup(func() { _ = listener.Close() })
	ip := listener.Addr().(*net.TCPAddr).IP
	certificate, ca, err := makeCertificate(ip)
	if err != nil {
		t.Fatal("cannot create disposable storage gateway trust")
	}
	g := &Gateway{CA: ca}
	g.server = httptest.NewUnstartedServer(http.HandlerFunc(g.serve))
	_ = g.server.Listener.Close()
	g.server.Listener = listener
	g.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	g.server.Config.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		g.mu.Lock()
		g.nextID++
		id := g.nextID
		g.mu.Unlock()
		return context.WithValue(ctx, connectionKey{}, id)
	}
	g.server.StartTLS()
	g.URL = g.server.URL
	t.Cleanup(g.server.Close)
	return g
}

func (g *Gateway) Client() *http.Client { return g.server.Client() }

func (g *Gateway) SetHandler(handler http.Handler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.handler = handler
}

func (g *Gateway) Disconnect() { g.server.CloseClientConnections() }

func (g *Gateway) Observations() []Observation {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Observation(nil), g.records...)
}

// DelayNextList consumes exactly one fault, leaving cold-boot validation healthy.
// The cancellation channel proves that a timed-out validator did not continue
// through to the real gateway. Unconsumed faults can be removed with ClearFault.
func (g *Gateway) DelayNextList(delay time.Duration) (*ListFault, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if delay <= 0 || g.next != nil {
		return nil, errors.New("invalid or overlapping storage listing fault")
	}
	fault := &ListFault{Entered: make(chan struct{}), Cancelled: make(chan struct{}), Done: make(chan struct{}), delay: delay}
	g.next = fault
	return fault, nil
}

func (g *Gateway) ClearFault() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next = nil
}

func (g *Gateway) serve(w http.ResponseWriter, r *http.Request) {
	listing := r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2"
	g.mu.Lock()
	handler, fault := g.handler, (*ListFault)(nil)
	if listing && g.next != nil {
		fault, g.next = g.next, nil
	}
	g.mu.Unlock()
	id, _ := r.Context().Value(connectionKey{}).(uint64)
	observed := &responseWriter{ResponseWriter: w}
	defer func() {
		g.mu.Lock()
		g.records = append(g.records, Observation{Connection: id, AccessKey: accessKey(r), Observer: r.Header.Get("X-Gregale-Acceptance-Observer") == "1",
			Method: r.Method, Path: r.URL.Path, Listing: listing, Status: observed.status})
		g.mu.Unlock()
		if fault != nil {
			close(fault.Done)
		}
	}()
	if fault != nil {
		close(fault.Entered)
		timer := time.NewTimer(fault.delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			close(fault.Cancelled)
			return
		case <-timer.C:
		}
	}
	if handler == nil {
		observed.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(observed, r)
	if observed.status == 0 {
		observed.status = http.StatusOK
	}
}

func accessKey(r *http.Request) string {
	_, rest, found := strings.Cut(r.Header.Get("Authorization"), "Credential=")
	if !found {
		return ""
	}
	key, _, _ := strings.Cut(rest, "/")
	return key
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func makeCertificate(ip net.IP) (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "owned-storage-fixture"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(2 * time.Hour), IPAddresses: []net.IP{ip},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, ca, nil
}
