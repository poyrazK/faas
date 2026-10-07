package ingress

// adr: 624

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func nativeIdentityRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+PublicIdentityHost+NativePublicIdentityPath, nil)
	r.Header.Set(NonceHeader, uuid.NewString())
	r.Header.Set(TokenHeader, nativePublicRequestProof(testToken, r.Header.Get(NonceHeader)))
	return r
}

func TestNativeIdentityAuthenticatesFrozenStartupWithoutInterceptingCustomerPaths(t *testing.T) {
	startup := nativeStartupFixture()
	want := startup
	h, err := newNativePublicIdentityHandler(testToken, startup)
	if err != nil {
		t.Fatal(err)
	}
	startup.Epoch.BootID = uuid.NewString()
	var customerCalls atomic.Int32
	wrapped := WrapNativePublicIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { customerCalls.Add(1); w.WriteHeader(204) }), h)
	r := nativeIdentityRequest()
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), testToken) || customerCalls.Load() != 0 {
		t.Fatal(w.Code, w.Body.String(), customerCalls.Load())
	}
	got, err := readNativePublicIdentity(w.Result(), testToken, r.Header.Get(NonceHeader))
	if err != nil || got.Startup() != want {
		t.Fatal(got, err)
	}
	copyOut := got.Envelope()
	copyOut[0] = '!'
	if !bytes.Equal(got.Envelope(), w.Body.Bytes()) {
		t.Fatal("caller changed retained proof")
	}
	for _, path := range []string{NativePublicIdentityPath, PublicIdentityPath, "/ordinary"} {
		r := httptest.NewRequest(http.MethodGet, "http://app.example"+path, nil)
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatal("customer path intercepted", path, w.Code)
		}
	}
	if customerCalls.Load() != 3 {
		t.Fatal(customerCalls.Load())
	}
}

func TestNativeIdentityRejectsUnauthenticatedAmbiguousAndLegacyRequests(t *testing.T) {
	h, err := newNativePublicIdentityHandler(testToken, nativeStartupFixture())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*http.Request){
		"host": func(r *http.Request) { r.Host = "other.example" }, "method": func(r *http.Request) { r.Method = http.MethodPost }, "path": func(r *http.Request) { r.URL.Path = PublicIdentityPath }, "query": func(r *http.Request) { r.URL.RawQuery = "x=1" }, "empty-query": func(r *http.Request) { r.URL.ForceQuery = true }, "raw-path": func(r *http.Request) { r.URL.RawPath = r.URL.Path }, "body": func(r *http.Request) { r.ContentLength = 1 }, "chunked": func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, "upgrade": func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
		"legacy-key-domain": func(r *http.Request) {
			r.Header.Set(TokenHeader, publicRequestProof(testToken, r.Header.Get(NonceHeader)))
		}, "ambiguous-upgrade": func(r *http.Request) {
			r.Header["Upgrade"] = []string{"", "websocket"}
		}, "nonce": func(r *http.Request) { r.Header.Set(NonceHeader, "bad") }, "duplicate-nonce": func(r *http.Request) { r.Header.Add(NonceHeader, uuid.NewString()) }, "duplicate-proof": func(r *http.Request) { r.Header.Add(TokenHeader, "bad") }, "wrong-key": func(r *http.Request) {
			r.Header.Set(TokenHeader, nativePublicRequestProof(strings.Repeat("f", 64), r.Header.Get(NonceHeader)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := nativeIdentityRequest()
			mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 || strings.Contains(w.Body.String(), "machine_id") {
				t.Fatal("unauthenticated request exposed identity", w.Code, w.Body.String())
			}
		})
	}
}

func TestNativeIdentityProbeRejectsEveryDifferentStartupEpochField(t *testing.T) {
	startup := nativeStartupFixture()
	h, err := newNativePublicIdentityHandler(testToken, startup)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	address := s.Listener.Addr().String()
	if got, err := ProbeNativePublicStartup(t.Context(), address, testToken, startup); err != nil || got.Startup() != startup {
		t.Fatal(got, err)
	}
	for name, mutate := range map[string]func(*NativePublicStartup){
		"slot": func(s *NativePublicStartup) { s.SlotID = uuid.NewString() }, "session": func(s *NativePublicStartup) { s.SessionID = uuid.NewString() }, "config": func(s *NativePublicStartup) { s.ConfigSHA256 = strings.Repeat("b", 64) }, "machine": func(s *NativePublicStartup) { s.Epoch.MachineID = strings.Repeat("2", 32) }, "boot": func(s *NativePublicStartup) { s.Epoch.BootID = uuid.NewString() }, "pid": func(s *NativePublicStartup) { s.Epoch.PID++ }, "start": func(s *NativePublicStartup) { s.Epoch.StartTicks++ }, "pidns": func(s *NativePublicStartup) { s.Epoch.PIDNamespace = "pid:[1]" }, "netns": func(s *NativePublicStartup) { s.Epoch.NetNamespace = "net:[1]" },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := startup
			mutate(&wrong)
			if got, err := ProbeNativePublicStartup(t.Context(), address, testToken, wrong); !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 {
				t.Fatal("foreign expected epoch accepted", got, err)
			}
		})
	}
}

func TestNativeIdentityAuthenticatesEveryFieldAndRejectsNoncanonicalFraming(t *testing.T) {
	startup := nativeStartupFixture()
	nonce := uuid.NewString()
	e := nativePublicEnvelope{Startup: startup, Nonce: nonce}
	e.Proof = nativePublicResponseProof(testToken, e)
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"machine", "boot", "pid", "start", "pidns", "netns", "config", "session", "slot", "nonce", "proof", "legacy-proof", "duplicate", "unknown", "alias", "space", "trailing", "truncated", "length", "chunked", "type", "duplicate-type", "trailers", "encoding", "status", "bound"} {
		t.Run(kind, func(t *testing.T) {
			body := bytes.Clone(raw)
			changed := e
			switch kind {
			case "machine":
				changed.Startup.Epoch.MachineID = strings.Repeat("2", 32)
			case "boot":
				changed.Startup.Epoch.BootID = uuid.NewString()
			case "pid":
				changed.Startup.Epoch.PID++
			case "start":
				changed.Startup.Epoch.StartTicks++
			case "pidns":
				changed.Startup.Epoch.PIDNamespace = "pid:[1]"
			case "netns":
				changed.Startup.Epoch.NetNamespace = "net:[1]"
			case "config":
				changed.Startup.ConfigSHA256 = strings.Repeat("b", 64)
			case "session":
				changed.Startup.SessionID = uuid.NewString()
			case "slot":
				changed.Startup.SlotID = uuid.NewString()
			case "nonce":
				changed.Nonce = uuid.NewString()
			case "proof":
				changed.Proof = "bad"
			case "legacy-proof":
				changed.Proof = publicResponseProof(testToken, PublicEdgeIdentity{SlotID: startup.SlotID, SessionID: startup.SessionID, ConfigSHA256: startup.ConfigSHA256, Nonce: nonce})
			case "duplicate":
				body = bytes.Replace(body, []byte(`"pid":1234`), []byte(`"pid":1234,"pid":1234`), 1)
			case "unknown":
				body = bytes.Replace(body, []byte(`"pid":1234`), []byte(`"pid":1234,"other":0`), 1)
			case "alias":
				body = bytes.Replace(body, []byte(`"pid":`), []byte(`"PID":`), 1)
			case "space":
				body = append(body, '\n')
			case "trailing":
				body = append(body, []byte("{}")...)
			case "truncated":
				body = body[:len(body)-1]
			case "bound":
				body = bytes.Repeat([]byte("a"), api.RuntimeUpgradeIngressIdentityMaxBytes+1)
			}
			if changed != e {
				body, err = json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
			}
			response := &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}
			switch kind {
			case "length":
				response.ContentLength++
			case "chunked":
				response.TransferEncoding = []string{"chunked"}
			case "type":
				response.Header.Set("Content-Type", "text/plain")
			case "duplicate-type":
				response.Header.Add("Content-Type", "application/json")
			case "trailers":
				response.Trailer = http.Header{"X-Proof": []string{"late"}}
			case "encoding":
				response.Header.Set("Content-Encoding", "gzip")
			case "status":
				response.StatusCode = 202
			}
			got, err := readNativePublicIdentity(response, testToken, nonce)
			if !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 {
				t.Fatal("modified response emitted proof", got, err)
			}
		})
	}
	response := &http.Response{StatusCode: 200, ContentLength: int64(len(raw)), Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw))}
	if got, err := readNativePublicIdentity(response, testToken, uuid.NewString()); !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 {
		t.Fatal("nonce replay", got, err)
	}
}

func TestNativeIdentityProbeCancellationDuringIncompleteBodyReturnsZeroProof(t *testing.T) {
	entered := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "900")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer s.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	var got NativeStartupProof
	var err error
	go func() {
		got, err = ProbeNativePublicStartup(ctx, s.Listener.Addr().String(), testToken, nativeStartupFixture())
		close(done)
	}()
	select {
	case <-entered:
	case <-done:
		t.Fatal("probe ended before incomplete response", err)
	}
	cancel()
	<-done
	if !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 {
		t.Fatal("cancelled incomplete body emitted proof", got, err)
	}
}

func TestNativeIdentityConcurrentProbesUseFreshNoncesAndConnections(t *testing.T) {
	startup := nativeStartupFixture()
	h, err := newNativePublicIdentityHandler(testToken, startup)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	nonces, addresses := make(map[string]bool), make(map[string]bool)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if nonces[r.Header.Get(NonceHeader)] || addresses[r.RemoteAddr] {
			t.Error("nonce or connection reused")
		}
		nonces[r.Header.Get(NonceHeader)], addresses[r.RemoteAddr] = true, true
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	defer s.Close()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if got, err := ProbeNativePublicStartup(t.Context(), s.Listener.Addr().String(), testToken, startup); err != nil || got.Startup() != startup {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(nonces) != 12 || len(addresses) != 12 {
		t.Fatal(len(nonces), len(addresses))
	}
}

func TestNativeIdentityProbeCancelsAndRefusesRedirectOrImplicitEndpoint(t *testing.T) {
	startup := nativeStartupFixture()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "http://127.0.0.1:1/foreign", 307)
	}))
	defer s.Close()
	if got, err := ProbeNativePublicStartup(t.Context(), s.Listener.Addr().String(), testToken, startup); !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 || calls.Load() != 1 {
		t.Fatal(got, err, calls.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProbeNativePublicStartup(ctx, s.Listener.Addr().String(), testToken, startup); !errors.Is(err, ErrNativeStartupUnverified) || calls.Load() != 1 {
		t.Fatal("cancelled probe made a connection", err, calls.Load())
	}
	for _, address := range []string{"localhost:80", "192.0.2.1:80", "unix:/run/faas.sock", "127.0.0.1:0"} {
		if got, err := ProbeNativePublicStartup(t.Context(), address, testToken, startup); !errors.Is(err, ErrNativeStartupUnverified) || len(got.Envelope()) != 0 {
			t.Fatal(address, got, err)
		}
	}
	if _, err := ProbeNativePublicStartup(nil, s.Listener.Addr().String(), testToken, startup); !errors.Is(err, ErrNativeStartupUnverified) {
		t.Fatal(err)
	}
	if len((NativeStartupProof{}).Envelope()) != 0 {
		t.Fatal("zero proof leaked bytes")
	}
}
