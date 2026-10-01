// adr: 375
package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"google.golang.org/grpc"
)

type gatewaySecurityStore struct {
	mu     sync.Mutex
	states map[trafficrevocation.Scope]trafficrevocation.State
	err    error
}

func (s *gatewaySecurityStore) Read(ctx context.Context, scopes []trafficrevocation.Scope) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[trafficrevocation.Scope]trafficrevocation.State, len(scopes))
	for _, scope := range scopes {
		result[scope] = s.states[scope]
	}
	return result, s.err
}

func (s *gatewaySecurityStore) set(scope trafficrevocation.Scope, revision int64, revoked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[trafficrevocation.Scope]trafficrevocation.State)
	}
	s.states[scope] = trafficrevocation.State{Revision: revision, Revoked: revoked}
}

func TestTrafficRevocationRefusesBeforeWake(t *testing.T) {
	for _, tc := range []struct {
		name, kind, id, code string
		storeErr             error
		hot                  bool
	}{
		{name: "account", kind: "account", id: "acct-1", code: api.CodeTrafficRevoked},
		{name: "app", kind: "app", id: "app-1", code: api.CodeTrafficRevoked},
		{name: "store-outage", storeErr: errors.New("store offline"), code: api.CodeTrafficRevocationUnavailable},
		{name: "missing-deployment", hot: true, code: api.CodeTrafficRevocationUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			if tc.hot {
				backend.setLegacyHot()
			}
			store := &gatewaySecurityStore{err: tc.storeErr}
			if tc.kind != "" {
				store.set(trafficrevocation.Scope{Kind: tc.kind, ID: tc.id}, 1, true)
			}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			h.WithTrafficRevocations(registry)
			forwarded := false
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			status := http.StatusServiceUnavailable
			if tc.code == api.CodeTrafficRevoked {
				status = http.StatusForbidden
			}
			if rec.Code != status || !strings.Contains(rec.Body.String(), tc.code) || forwarded || backend.admits != 0 {
				t.Fatalf("status=%d body=%q forwarded=%v admits=%d", rec.Code, rec.Body, forwarded, backend.admits)
			}
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Fatalf("refused request leaked registrations: %d/%d", exchanges, scopes)
			}
		})
	}
}

func TestTrafficRevocationCancelsPublicExchangeUntilForwardCleanup(t *testing.T) {
	for _, kind := range []string{"account", "app", "deployment", "store-outage", "missed-revoke-release"} {
		t.Run(kind, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			backend.setLegacyHot()
			backend.targets[0].DeploymentID = "dep-1"
			store := &gatewaySecurityStore{}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			h.WithTrafficRevocations(registry)
			started := make(chan context.Context, 1)
			canceled := make(chan error, 1)
			cleanup := make(chan struct{})
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					started <- r.Context()
					<-r.Context().Done()
					canceled <- trafficRevocationCause(r.Context())
					<-cleanup // models forwarding ownership still being cleaned up
					handleForwardRequestCancellation(w, r, true)
				})
			})
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
				result <- rec
			}()
			var ctx context.Context
			select {
			case ctx = <-started:
			case <-time.After(time.Second):
				t.Fatal("forward did not start")
			}
			if err := registry.Refresh(t.Context()); err != nil || ctx.Err() != nil {
				t.Fatalf("unchanged generation canceled exchange: %v/%v", err, ctx.Err())
			}
			scope := trafficrevocation.Scope{Kind: kind, ID: map[string]string{"account": "acct-1", "app": "app-1", "deployment": "dep-1"}[kind]}
			wantCause, wantStatus, wantCode := trafficrevocation.ErrRevoked, http.StatusForbidden, api.CodeTrafficRevoked
			if kind == "store-outage" {
				store.mu.Lock()
				store.err = errors.New("store offline")
				store.mu.Unlock()
				wantCause, wantStatus, wantCode = trafficrevocation.ErrUnavailable, http.StatusServiceUnavailable, api.CodeTrafficRevocationUnavailable
			} else if kind == "missed-revoke-release" {
				scope = trafficrevocation.Scope{Kind: "account", ID: "acct-1"}
				store.set(scope, 2, false)
			} else {
				store.set(scope, 1, true)
			}
			_ = registry.Refresh(t.Context())
			select {
			case cause := <-canceled:
				if !errors.Is(cause, wantCause) {
					t.Fatalf("cause=%v", cause)
				}
			case <-time.After(time.Second):
				t.Fatal("exchange survived revocation")
			}
			if exchanges, scopes := registry.Tracked(); exchanges == 0 || scopes == 0 {
				t.Fatal("cancellation released forwarding ownership early")
			}
			close(cleanup)
			select {
			case rec := <-result:
				if rec.Code != wantStatus || !strings.Contains(rec.Body.String(), wantCode) {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
				}
			case <-time.After(time.Second):
				t.Fatal("cleanup retained request")
			}
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Fatalf("completed request retained registrations: %d/%d", exchanges, scopes)
			}
			store.mu.Lock()
			store.err = nil
			store.mu.Unlock()
			if kind != "store-outage" {
				store.set(scope, 2, false)
			}
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
			})
			fresh := httptest.NewRecorder()
			h.ServeHTTP(fresh, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if fresh.Code != http.StatusNoContent {
				t.Fatalf("fresh generation did not recover: %d/%s", fresh.Code, fresh.Body)
			}
		})
	}
}

type revocationForwardServer struct {
	vmmdpb.UnimplementedVmmdServer
	finished chan error
}

func (s *revocationForwardServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	defer func() { s.finished <- stream.Context().Err() }()
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: http.StatusOK}}}); err != nil {
		return err
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("partial")}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func TestTrafficRevocationCancelsDetachedRealGRPCResponse(t *testing.T) {
	for _, protocol := range []string{"explicit-stream", "grpc"} {
		for _, h2 := range []bool{false, true} {
			t.Run(protocol+"/h2="+strconv.FormatBool(h2), func(t *testing.T) {
				store := &gatewaySecurityStore{}
				registry := trafficrevocation.New(store)
				defer registry.Close()
				fixture := &revocationForwardServer{finished: make(chan error, 1)}
				nodes := singleClientLookup{cli: newDeadlineForwardClient(t, fixture)}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				done := make(chan struct{})
				decisions := make(chan trafficDecisionSnapshot, 1)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(done)
					defer func() { cancelStampedRequestBudget(r.Context()) }() //nolint:contextcheck // cleanup reads the final rebound request context.
					ctx, cancel, _ := reqbudget.WithStarted(withTrafficDecision(r.Context(), false), time.Now(), 100*time.Millisecond, api.RequestBudgetMax, "forward", "stream")
					defer cancel()
					defer func() { decisions <- trafficDecisionEvidence(r.Context(), http.StatusOK, true) }() //nolint:contextcheck // observe the final request after enrollment attaches its lifetime fence.
					r = r.WithContext(ctx)
					if enrollTrafficScopes(w, r, registry, trafficrevocation.Scope{Kind: "deployment", ID: "dep-1"}) {
						return
					}
					if protocol == "grpc" {
						r.Header.Set("x-faas-protocol", "grpc")
					} else {
						r.Header.Set("x-faas-stream", "true")
					}
					fwdOnceWithEvents(flushDeadlineWriter{w}, r, nodes, log, Target{NodeID: "node"}, nil)
				}))
				server.EnableHTTP2 = h2
				if h2 {
					server.StartTLS()
				} else {
					server.Start()
				}
				t.Cleanup(server.Close)
				client := server.Client()
				client.Timeout = 2 * time.Second
				resp, err := client.Get(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				time.Sleep(150 * time.Millisecond) // real handshake budget expires after detachment
				select {
				case err := <-fixture.finished:
					t.Fatalf("detached RPC ended on handshake budget: %v", err)
				default:
				}
				store.set(trafficrevocation.Scope{Kind: "deployment", ID: "dep-1"}, 1, true)
				if err := registry.Refresh(t.Context()); err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				if string(body) != "partial" || err == nil {
					t.Fatalf("revoked body ended cleanly: %q/%v", body, err)
				}
				select {
				case err := <-fixture.finished:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("RPC cleanup=%v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("revoked RPC retained ownership")
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("handler retained revoked response")
				}
				if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
					t.Fatalf("long response leaked registrations: %d/%d", exchanges, scopes)
				}
				evidence := <-decisions // Handler completion above guarantees publication.
				if !evidence.streamDetached || evidence.outcome != "refused" || evidence.refusal != "security_revoked" {
					t.Fatalf("revoked wire response lost its lifetime reason: %+v", evidence)
				}
			})
		}
	}
}

func TestTrafficRevocationRemovesUploadSpoolBeforeWake(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	h, backend, _ := newTestHandler(t)
	store := &gatewaySecurityStore{}
	registry := trafficrevocation.New(store)
	defer registry.Close()
	h.WithTrafficRevocations(registry)
	reader, writer := io.Pipe()
	defer writer.Close()
	prefixWritten := make(chan struct{})
	go func() {
		_, _ = writer.Write(bytes.Repeat([]byte("x"), int(requestBodyMemoryThreshold)+1))
		close(prefixWritten)
	}()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "http://"+backend.host+"/", reader)
		r.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		result <- rec
	}()
	select {
	case <-prefixWritten:
	case <-time.After(time.Second):
		t.Fatal("upload was not admitted")
	}
	store.set(trafficrevocation.Scope{Kind: "account", ID: "acct-1"}, 1, true)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-result:
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeTrafficRevoked) {
			t.Fatalf("upload revoke=%d/%s", rec.Code, rec.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked upload retained ownership")
	}
	if backend.admits != 0 {
		t.Fatal("revoked upload reached wake")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("revoked upload retained spool: %v/%v", entries, err)
	}
}

type securityWakeBackend struct {
	*fakeBackend
	started chan struct{}
	release chan struct{}
}

func (b *securityWakeBackend) Admit(ctx context.Context, _ string, _ string, _ string, _ string, _ int) (string, WakeMethod, bool, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return "", WakeMethodUnspecified, false, errors.New("wake fixture released")
}

func TestTrafficRevocationDuringWakeDeliversHTTPProblem(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(strconv.FormatBool(h2), func(t *testing.T) {
			base := &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "app.test"}
			backend := &securityWakeBackend{fakeBackend: base, started: make(chan struct{}), release: make(chan struct{})}
			defer close(backend.release)
			store := &gatewaySecurityStore{}
			registry := trafficrevocation.New(store)
			defer registry.Close()
			h := NewHandlerWith(backend, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil))).WithTrafficRevocations(registry)
			server := httptest.NewUnstartedServer(h)
			server.EnableHTTP2 = h2
			if h2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			server.Client().Timeout = 2 * time.Second
			result := make(chan error, 1)
			go func() {
				r, _ := http.NewRequest(http.MethodGet, server.URL, nil)
				r.Host = base.host
				resp, err := server.Client().Do(r)
				if err == nil {
					defer resp.Body.Close()
					body, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						err = readErr
					} else if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), api.CodeTrafficRevoked) {
						err = errors.New("revocation did not deliver a 403 problem")
					}
				}
				result <- err
			}()
			select {
			case <-backend.started:
			case <-time.After(time.Second):
				t.Fatal("wake did not begin")
			}
			store.set(trafficrevocation.Scope{Kind: "app", ID: "app-1"}, 1, true)
			if err := registry.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("revoked wake waiter retained request")
			}
		})
	}
}

func TestTrafficRevocationReleasesBlockedResponseWrite(t *testing.T) {
	for _, long := range []bool{false, true} {
		for _, h2 := range []bool{false, true} {
			t.Run("long="+strconv.FormatBool(long)+"/h2="+strconv.FormatBool(h2), func(t *testing.T) {
				store := &gatewaySecurityStore{}
				registry := trafficrevocation.New(store)
				defer registry.Close()
				h, backend, _ := newTestHandler(t)
				backend.setLegacyHot()
				backend.targets[0].DeploymentID = "dep-1"
				if long {
					backend.app.StreamingEnabled = true
					h.WithStreamingEnabled(true)
				}
				h.WithTrafficRevocations(registry)
				fixture := &deadlineFloodServer{finished: make(chan error, 1)}
				nodes := singleClientLookup{cli: newDeadlineForwardClient(t, fixture)}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				h.WithForwarding(func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fwdOnceWithEvents(w, r, nodes, log, target, nil) })
				})
				done := make(chan struct{})
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); h.ServeHTTP(w, r) }))
				server.EnableHTTP2 = h2
				if h2 {
					server.StartTLS()
				} else {
					server.Start()
				}
				t.Cleanup(server.Close)
				resp, closeClient := openSlowResponseReader(t, server, backend.host, h2, false)
				defer func() { closeClient(); _ = resp.Body.Close() }()
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("response=%d", resp.StatusCode)
				}
				// Fill the downstream socket/window before changing security state.
				time.Sleep(100 * time.Millisecond)
				store.set(trafficrevocation.Scope{Kind: "deployment", ID: "dep-1"}, 1, true)
				if err := registry.Refresh(t.Context()); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("revocation retained a blocked writer")
				}
				select {
				case err := <-fixture.finished:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("RPC was not canceled: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("revocation retained the RPC")
				}
				if sent := fixture.sent.Load(); sent <= 0 || sent >= slowReaderBodySize {
					t.Fatalf("fixture missed backpressure: %d", sent)
				}
				if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
					t.Fatalf("blocked response leaked registrations: %d/%d", exchanges, scopes)
				}
			})
		}
	}
}
