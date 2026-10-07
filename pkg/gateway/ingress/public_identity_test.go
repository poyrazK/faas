package ingress

// adr: 702

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func publicIdentityFixture() PublicEdgeIdentity {
	return PublicEdgeIdentity{SlotID: uuid.NewString(), SessionID: uuid.NewString(), ConfigSHA256: strings.Repeat("a", 64)}
}

func publicIdentityRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+PublicIdentityHost+PublicIdentityPath, nil)
	r.Header.Set(NonceHeader, uuid.NewString())
	r.Header.Set(TokenHeader, publicRequestProof(token, r.Header.Get(NonceHeader)))
	return r
}

func TestPublicIdentityReservesInfrastructureHostAndAuthenticatesWithoutForwarding(t *testing.T) {
	id := publicIdentityFixture()
	h, err := NewPublicIdentityHandler(testToken, id)
	if err != nil {
		t.Fatal(err)
	}
	var forwards atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwards.Add(1); w.WriteHeader(204) })
	wrapped := WrapPublicIdentity(next, h)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		code   int
	}{
		{"authenticated", func(*http.Request) {}, 200},
		{"customer path", func(r *http.Request) { r.Host = "app.example" }, 204},
		{"other infrastructure path", func(r *http.Request) { r.URL.Path = "/other" }, 204},
		{"wrong secret", func(r *http.Request) { r.Header.Set(TokenHeader, strings.Repeat("b", 64)) }, 404},
		{"internal protocol reflection", func(r *http.Request) { r.Header.Set(TokenHeader, requestProof(testToken, r.Header.Get(NonceHeader))) }, 404},
		{"missing nonce", func(r *http.Request) { r.Header.Del(NonceHeader) }, 404},
		{"duplicate nonce", func(r *http.Request) { r.Header.Add(NonceHeader, r.Header.Get(NonceHeader)) }, 404},
		{"duplicate proof", func(r *http.Request) { r.Header.Add(TokenHeader, r.Header.Get(TokenHeader)) }, 404},
		{"nil uuid", func(r *http.Request) { r.Header.Set(NonceHeader, uuid.Nil.String()) }, 404},
		{"query", func(r *http.Request) { r.URL.RawQuery = "x=1" }, 404},
		{"empty query", func(r *http.Request) { r.URL.ForceQuery = true }, 404},
		{"escaped path", func(r *http.Request) { r.URL.RawPath = "/v1/internal/runtime-public-edge/%69dentity" }, 404},
		{"post", func(r *http.Request) { r.Method = http.MethodPost }, 404},
		{"upgrade", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }, 404},
		{"body", func(r *http.Request) { r.ContentLength = 1 }, 404},
		{"chunked body", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := publicIdentityRequest(testToken)
			tc.mutate(r)
			w := httptest.NewRecorder()
			before := forwards.Load()
			wrapped.ServeHTTP(w, r)
			if w.Code != tc.code || (tc.code != 204 && forwards.Load() != before) {
				t.Fatal(w.Code, forwards.Load(), before)
			}
			if w.Code == 200 {
				got, err := readPublicIdentity(w.Result(), r.Header.Get(NonceHeader), testToken)
				if err != nil || got.SlotID != id.SlotID || got.SessionID != id.SessionID || got.ConfigSHA256 != id.ConfigSHA256 || strings.Contains(w.Body.String(), testToken) || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("identity not bound or secret disclosed", err)
				}
			}
		})
	}
}

func TestPublicIdentityRejectsMalformedStartupCredentials(t *testing.T) {
	for _, change := range []func(*PublicEdgeIdentity){
		func(id *PublicEdgeIdentity) { id.SlotID = uuid.Nil.String() },
		func(id *PublicEdgeIdentity) { id.SessionID = "invalid" },
		func(id *PublicEdgeIdentity) { id.ConfigSHA256 = strings.Repeat("A", 64) },
		func(id *PublicEdgeIdentity) { id.ConfigSHA256 = strings.Repeat("a", 63) },
	} {
		id := publicIdentityFixture()
		change(&id)
		if _, err := NewPublicIdentityHandler(testToken, id); err == nil {
			t.Fatal("bad startup tuple accepted")
		}
	}
	if _, err := NewPublicIdentityHandler("not a token", publicIdentityFixture()); err == nil {
		t.Fatal("bad secret accepted")
	}
}

func TestPublicIdentityProbeBindsFreshNonceAndExactStartupTuple(t *testing.T) {
	id := publicIdentityFixture()
	h, err := NewPublicIdentityHandler(testToken, id)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	address := server.Listener.Addr().String()
	if err := ProbePublicEdge(t.Context(), address, testToken, id); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*PublicEdgeIdentity){
		func(id *PublicEdgeIdentity) { id.SlotID = uuid.NewString() },
		func(id *PublicEdgeIdentity) { id.SessionID = uuid.NewString() },
		func(id *PublicEdgeIdentity) { id.ConfigSHA256 = strings.Repeat("b", 64) },
	} {
		other := id
		change(&other)
		if err := ProbePublicEdge(t.Context(), address, testToken, other); err == nil {
			t.Fatal("replaced startup tuple accepted")
		}
	}
	if err := ProbePublicEdge(t.Context(), address, strings.Repeat("f", 64), id); err == nil {
		t.Fatal("wrong secret accepted")
	}
	var replay []byte
	var replayMu sync.Mutex
	replayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replayMu.Lock()
		defer replayMu.Unlock()
		if replay == nil {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			replay = bytes.Clone(rec.Body.Bytes())
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(replay)))
		_, _ = w.Write(replay)
	}))
	defer replayed.Close()
	address = replayed.Listener.Addr().String()
	if err := ProbePublicEdge(t.Context(), address, testToken, id); err != nil {
		t.Fatal(err)
	}
	if err := ProbePublicEdge(t.Context(), address, testToken, id); err == nil {
		t.Fatal("replayed nonce accepted")
	}
}

func TestPublicIdentityResponseFailsClosedOnAmbiguityAndFraming(t *testing.T) {
	id := publicIdentityFixture()
	id.Nonce = uuid.NewString()
	id.Proof = publicResponseProof(testToken, id)
	body, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body []byte
		code int
		len  int64
		te   []string
	}{
		{"duplicate session", bytes.Replace(body, []byte(`"session_id":`), []byte(`"session_id":"`+id.SessionID+`","session_id":`), 1), 200, -2, nil},
		{"case duplicate", bytes.Replace(body, []byte(`"slot_id":`), []byte(`"SLOT_ID":"`+id.SlotID+`","slot_id":`), 1), 200, -2, nil},
		{"unknown", bytes.Replace(body, []byte(`{`), []byte(`{"extra":1,`), 1), 200, -2, nil},
		{"trailing", append(bytes.Clone(body), []byte(` {}`)...), 200, -2, nil},
		{"wrong proof", bytes.Replace(body, []byte(id.Proof), []byte(strings.Repeat("0", 64)), 1), 200, -2, nil},
		{"oversized", bytes.Repeat([]byte(" "), api.RuntimeUpgradeIngressIdentityMaxBytes+1), 200, -2, nil},
		{"no explicit length", body, 200, -1, nil},
		{"truncated", body, 200, int64(len(body) + 1), nil},
		{"chunked", body, 200, int64(len(body)), []string{"chunked"}},
		{"redirect", body, 307, -2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.len
			if n == -2 {
				n = int64(len(tc.body))
			}
			r := &http.Response{StatusCode: tc.code, ContentLength: n, TransferEncoding: tc.te, Body: io.NopCloser(bytes.NewReader(tc.body))}
			if _, err := readPublicIdentity(r, id.Nonce, testToken); err == nil {
				t.Fatal("ambiguous response accepted")
			}
		})
	}
}

func TestPublicIdentityProbeCancelsAndNeverFollowsRedirects(t *testing.T) {
	id := publicIdentityFixture()
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	if err := ProbePublicEdge(t.Context(), redirect.Listener.Addr().String(), testToken, id); err == nil || forwarded.Load() != 0 {
		t.Fatal("identity probe followed redirect", err, forwarded.Load())
	}
	started, release := make(chan struct{}), make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(started); <-release }))
	defer slow.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ProbePublicEdge(ctx, slow.Listener.Addr().String(), testToken, id) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled identity verified")
		}
	case <-time.After(time.Second):
		t.Fatal("identity cancellation did not close connection")
	}
}

func TestPublicIdentityAddressRejectsImplicitAndRemoteOrigins(t *testing.T) {
	for _, address := range []string{"localhost:8080", "192.0.2.1:8080", "unix//run/public.sock", "127.0.0.1:0", "127.0.0.1:08080", "[::ffff:127.0.0.1]:8080", "[::1%lo0]:8080", "{http.request.host}:8080", "127.0.0.1"} {
		if PublicEdgeAddress(address) {
			t.Fatal("unsupported address accepted", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if !PublicEdgeAddress(address) {
			t.Fatal(address)
		}
	}
}
