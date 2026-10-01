// adr: 375
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type syntheticSecurityStore struct {
	mu     sync.Mutex
	states map[trafficrevocation.Scope]trafficrevocation.State
	err    error
}

func TestSyntheticSecurityHandoffMustMatchAdmittedOwnerAndGeneration(t *testing.T) {
	for _, kind := range []string{"valid", "legacy", "stale generation", "foreign account", "foreign app", "foreign deployment", "missing owner"} {
		t.Run(kind, func(t *testing.T) {
			store, app, dep, target := invocationDeliveryFixture(t)
			canonical := func(id string) string {
				parsed, err := uuid.Parse(id)
				if err != nil {
					t.Fatal(err)
				}
				return parsed.String()
			}
			rows := []struct {
				Kind     string `json:"kind"`
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
			}{
				{Kind: "account", ID: canonical(app.AccountID)}, {Kind: "app", ID: canonical(app.ID)}, {Kind: "deployment", ID: canonical(dep.ID)},
			}
			security := &syntheticSecurityStore{}
			switch kind {
			case "stale generation":
				security.change(trafficrevocation.Scope{Kind: "account", ID: app.AccountID}, 2, false, nil)
			case "foreign account":
				rows[0].ID = uuid.NewString()
			case "foreign app":
				rows[1].ID = uuid.NewString()
			case "foreign deployment":
				rows[2].ID = uuid.NewString()
			case "missing owner":
				rows = rows[1:]
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Kind < rows[j].Kind })
			data, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			baseline := "v1." + base64.RawURLEncoding.EncodeToString(data)
			if kind == "legacy" {
				baseline = ""
			}
			registry := trafficrevocation.New(security)
			defer registry.Close()
			forwards := 0
			adapter := &synthAdapter{store: store, trafficRevocations: registry, forward: func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
			}}
			body, err := json.Marshal(map[string]any{"invocation_id": uuid.NewString(), "app_id": app.ID, "account_id": app.AccountID, "source": state.InvocationAsyncInvoke,
				"instance_id": target.InstanceID, "node_id": target.NodeID, "deployment_id": target.DeploymentID, "security_snapshot": baseline})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			gateway.NewSynthServer("", adapter, nil).Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body)))
			if kind == "valid" || kind == "legacy" {
				if rec.Code != http.StatusOK || forwards != 1 {
					t.Fatalf("valid baseline refused: %d forwards=%d %s", rec.Code, forwards, rec.Body)
				}
			} else if rec.Code != http.StatusBadGateway || forwards != 0 {
				t.Fatalf("unsafe handoff: %d forwards=%d %s", rec.Code, forwards, rec.Body)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}

type syntheticForwardServer struct {
	vmmdpb.UnimplementedVmmdServer
	started  chan struct{}
	finished chan error
}

func (s *syntheticForwardServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
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
	close(s.started)
	<-stream.Context().Done()
	return stream.Context().Err()
}

type syntheticNodeClient struct{ client vmmdpb.VmmdClient }

func (s syntheticNodeClient) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return s.client, io.NopCloser(bytes.NewReader(nil)), true
}

func TestSyntheticSecurityCancelsActualGRPCForwarding(t *testing.T) {
	store, app, dep, target := invocationDeliveryFixture(t)
	security := &syntheticSecurityStore{}
	registry := trafficrevocation.New(security)
	defer registry.Close()
	fixture := &syntheticForwardServer{started: make(chan struct{}), finished: make(chan error, 1)}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rpc := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(rpc, fixture)
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	adapter := &synthAdapter{store: store, trafficRevocations: registry,
		forward: gateway.ForwardingReverseProxy(syntheticNodeClient{vmmdpb.NewVmmdClient(conn)}, discardLogger())}
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		out, status, err := adapter.InvokeWithTargetStatus(ctx, app.ID, state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke}, target)
		if status != 0 || len(out.Result) != 0 {
			result <- errors.New("revoked RPC published partial success")
			return
		}
		result <- err
	}()
	select {
	case <-fixture.started:
	case <-time.After(5 * time.Second):
		t.Fatal("RPC did not start")
	}
	security.change(trafficrevocation.Scope{Kind: "deployment", ID: dep.ID}, 1, true, nil)
	if err := registry.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, trafficrevocation.ErrRevoked) {
			t.Fatalf("forwarding result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("forwarding did not stop")
	}
	select {
	case err := <-fixture.finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RPC cleanup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RPC retained ownership")
	}
	assertSyntheticSecurityReleased(t, registry)
}

func (s *syntheticSecurityStore) Read(_ context.Context, scopes []trafficrevocation.Scope) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[trafficrevocation.Scope]trafficrevocation.State, len(scopes))
	for _, scope := range scopes {
		result[scope] = s.states[scope]
	}
	return result, s.err
}

func (s *syntheticSecurityStore) change(scope trafficrevocation.Scope, revision int64, revoked bool, failure error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[trafficrevocation.Scope]trafficrevocation.State)
	}
	s.states[scope] = trafficrevocation.State{Revision: revision, Revoked: revoked}
	s.err = failure
}

func assertSyntheticSecurityReleased(t *testing.T, registry *trafficrevocation.Registry) {
	t.Helper()
	if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
		t.Fatalf("synthetic security registrations leaked: %d/%d", exchanges, scopes)
	}
}

func TestSyntheticSecurityRefusesBeforeWakeAndForward(t *testing.T) {
	for _, kind := range []string{"account", "app", "deployment", "store outage"} {
		t.Run(kind, func(t *testing.T) {
			store, app, dep, target := invocationDeliveryFixture(t)
			security := &syntheticSecurityStore{}
			id := map[string]string{"account": app.AccountID, "app": app.ID, "deployment": dep.ID}[kind]
			failure := error(nil)
			if kind == "store outage" {
				failure = errors.New("store offline")
			}
			security.change(trafficrevocation.Scope{Kind: kind, ID: id}, 1, true, failure)
			registry := trafficrevocation.New(security)
			defer registry.Close()
			wakes, forwards := 0, 0
			adapter := &synthAdapter{store: store, trafficRevocations: registry,
				invokeWithStatus: func(_ context.Context, _ string, inv state.Invocation, _ state.InvocationVersion) (state.Invocation, int, error) {
					wakes++
					return inv, http.StatusOK, nil
				},
				forward: func(gateway.Target) http.Handler {
					forwards++
					return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
				},
			}
			inv := state.Invocation{AppID: app.ID, AccountID: app.AccountID, ID: uuid.NewString(), Source: state.InvocationAsyncInvoke,
				Headers: json.RawMessage(`{"X-Gregale-Revision":"` + dep.ID + `"}`)}
			want := trafficrevocation.ErrRevoked
			if failure != nil {
				want = trafficrevocation.ErrUnavailable
			}
			if _, status, err := adapter.InvokeWithStatus(t.Context(), app.ID, inv); !errors.Is(err, want) || status != 0 || wakes != 0 {
				t.Fatalf("unsafe wake: wakes=%d status=%d err=%v", wakes, status, err)
			}
			assertSyntheticSecurityReleased(t, registry)
			if _, status, err := adapter.InvokeWithTargetStatus(t.Context(), app.ID, inv, target); !errors.Is(err, want) || status != 0 || forwards != 0 {
				t.Fatalf("unsafe forward: forwards=%d status=%d err=%v", forwards, status, err)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}

func TestSyntheticSecurityFencesWakeAndForwardUntilCleanup(t *testing.T) {
	for _, phase := range []string{"wake", "forward"} {
		for _, kind := range []string{"account", "app", "deployment", "missed revoke release", "store outage"} {
			if phase == "wake" && kind == "deployment" {
				continue
			} // An unpinned target is selected by wake.
			t.Run(phase+"/"+kind, func(t *testing.T) {
				store, app, dep, target := invocationDeliveryFixture(t)
				security := &syntheticSecurityStore{}
				registry := trafficrevocation.New(security)
				defer registry.Close()
				started, canceled := make(chan struct{}), make(chan error, 1)
				cleanup := make(chan struct{})
				defer func() {
					select {
					case <-cleanup:
					default:
						close(cleanup)
					}
				}()
				block := func(ctx context.Context) {
					close(started)
					<-ctx.Done()
					canceled <- context.Cause(ctx)
					<-cleanup
				}
				adapter := &synthAdapter{store: store, trafficRevocations: registry}
				adapter.invokeWithStatus = func(ctx context.Context, _ string, inv state.Invocation, _ state.InvocationVersion) (state.Invocation, int, error) {
					if phase == "wake" {
						block(ctx)
						return inv, http.StatusOK, nil
					}
					return adapter.forwardInvocationWithStatus(ctx, target, inv)
				}
				adapter.forward = func(gateway.Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						block(r.Context())
						_, _ = w.Write([]byte(`{"ok":true}`)) // A late success cannot override the fence.
					})
				}
				result := make(chan error, 1)
				go func() {
					out, status, err := adapter.InvokeWithStatus(t.Context(), app.ID, state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke})
					if status != 0 || len(out.Result) != 0 {
						result <- errors.New("canceled invocation published a result")
						return
					}
					result <- err
				}()
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("delivery did not start")
				}
				id := map[string]string{"account": app.AccountID, "app": app.ID, "deployment": dep.ID}[kind]
				scope := trafficrevocation.Scope{Kind: kind, ID: id}
				want := trafficrevocation.ErrRevoked
				if kind == "store outage" {
					security.change(scope, 0, false, errors.New("store offline"))
					want = trafficrevocation.ErrUnavailable
				} else if kind == "missed revoke release" {
					scope = trafficrevocation.Scope{Kind: "account", ID: app.AccountID}
					security.change(scope, 2, false, nil)
				} else {
					security.change(scope, 1, true, nil)
				}
				refreshErr := registry.Refresh(t.Context())
				if kind == "store outage" && !errors.Is(refreshErr, want) {
					t.Fatalf("refresh: %v", refreshErr)
				}
				select {
				case cause := <-canceled:
					if !errors.Is(cause, want) {
						t.Fatalf("fence: %v", cause)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("active delivery was not canceled")
				}
				if exchanges, scopes := registry.Tracked(); exchanges == 0 || scopes < 2 {
					t.Fatalf("ownership released before cleanup: %d/%d", exchanges, scopes)
				}
				close(cleanup)
				select {
				case err := <-result:
					if !errors.Is(err, want) {
						t.Fatalf("late result: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("delivery did not clean up")
				}
				assertSyntheticSecurityReleased(t, registry)
			})
		}
	}
}

func TestSyntheticTriggerBatchCarriesSavedAccountToEveryRecord(t *testing.T) {
	for _, kind := range []string{"owner", "legacy account", "foreign account"} {
		t.Run(kind, func(t *testing.T) {
			store, app, _, target := invocationDeliveryFixture(t)
			account := app.AccountID
			if kind == "foreign account" {
				account = uuid.NewString()
			}
			if kind == "legacy account" {
				account = ""
			}
			security := &syntheticSecurityStore{}
			registry := trafficrevocation.New(security)
			defer registry.Close()
			wakes, forwards := 0, 0
			adapter := &synthAdapter{store: store, trafficRevocations: registry}
			adapter.invokeWithStatus = func(ctx context.Context, _ string, inv state.Invocation, _ state.InvocationVersion) (state.Invocation, int, error) {
				wakes++
				if inv.AccountID != account {
					t.Errorf("saved account lost: %q", inv.AccountID)
				}
				return adapter.forwardInvocationWithStatus(ctx, target, inv)
			}
			adapter.forward = func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
			}
			body, err := json.Marshal(map[string]any{"invocation_id": "trigger-inv", "app_id": app.ID, "account_id": account, "source": "esm", "trigger_id": "trigger-1",
				"records": []map[string]string{{"item_identifier": "a"}, {"item_identifier": "b"}}})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			gateway.NewSynthServer("", adapter, nil).Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", bytes.NewReader(body)))
			var reply struct {
				Results []struct {
					Status string `json:"status"`
					Code   string `json:"code"`
				} `json:"results"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusOK || len(reply.Results) != 2 {
				t.Fatalf("batch response: %d %s", rec.Code, rec.Body)
			}
			want, count := "succeeded", 2
			if kind == "foreign account" {
				want, count = "retry", 0
			}
			for _, result := range reply.Results {
				if result.Status != want {
					t.Fatalf("batch result: %s, want %s", rec.Body, want)
				}
				if kind == "foreign account" && result.Code != "invoke_error" {
					t.Fatalf("owner refusal code: %q", result.Code)
				}
			}
			if wakes != count || forwards != count {
				t.Fatalf("wake/forward = %d/%d, want %d", wakes, forwards, count)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}

func TestSyntheticSecurityHandoffRequiresVerifiedOwnerAndRegistry(t *testing.T) {
	for _, kind := range []string{"missing registry", "missing owner store", "invalid metadata"} {
		t.Run(kind, func(t *testing.T) {
			store, app, dep, target := invocationDeliveryFixture(t)
			states := map[trafficrevocation.Scope]trafficrevocation.State{{Kind: "account", ID: app.AccountID}: {}, {Kind: "app", ID: app.ID}: {}, {Kind: "deployment", ID: dep.ID}: {}}
			value, err := trafficrevocation.EncodeAdmittedSnapshot(states)
			if err != nil {
				t.Fatal(err)
			}
			registry := trafficrevocation.New(&syntheticSecurityStore{})
			defer registry.Close()
			forwards := 0
			adapter := &synthAdapter{store: store, trafficRevocations: registry, forward: func(gateway.Target) http.Handler {
				forwards++
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) })
			}}
			expected := http.StatusBadGateway
			if kind == "missing registry" {
				adapter.trafficRevocations = nil
			}
			if kind == "missing owner store" {
				adapter.store = nil
			}
			if kind == "invalid metadata" {
				value = "v1.invalid"
				expected = http.StatusServiceUnavailable
			}
			body, err := json.Marshal(map[string]any{"app_id": app.ID, "account_id": app.AccountID, "invocation_id": uuid.NewString(), "security_snapshot": value, "instance_id": target.InstanceID, "node_id": target.NodeID, "deployment_id": target.DeploymentID})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			gateway.NewSynthServer("", adapter, nil).Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", bytes.NewReader(body)))
			if rec.Code != expected || forwards != 0 {
				t.Fatalf("unverifiable handoff admitted: %d forwards=%d %s", rec.Code, forwards, rec.Body)
			}
			assertSyntheticSecurityReleased(t, registry)
		})
	}
}
