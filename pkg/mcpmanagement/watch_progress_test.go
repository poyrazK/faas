// adr: 1253
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func progressClient(t *testing.T, b *httptest.Server, opts Options, version string, httpTransport bool, onProgress func(*mcp.ProgressNotificationParams)) (*mcp.ClientSession, context.Context) {
	t.Helper()
	s, err := NewServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	var transport mcp.Transport
	if httpTransport {
		endpoint := httptest.NewServer(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return s }))
		t.Cleanup(endpoint.Close)
		transport = &mcp.StreamableClientTransport{Endpoint: endpoint.URL}
	} else {
		ct, st := mcp.NewInMemoryTransports()
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ss.Close() })
		transport = ct
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "progress-test", Version: "1"}, &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) { onProgress(req.Params) }})
	cs, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}
func progressFixture() *releaseFixture {
	return &releaseFixture{account: "account-one", app: api.AppResponse{ID: "app-one", Slug: "alpha", Type: "app", RAMMB: 256}, current: "release-base", latest: "release-base", deployments: map[string]api.DeploymentResponse{"release-base": {ID: "release-base", AppID: "app-one", Status: "live", Scope: "default", TrafficPercent: 100, RolloutState: "complete"}}}
}
func watchParams(token any, seconds int) *mcp.CallToolParams {
	p := &mcp.CallToolParams{Name: "gregale_watch_rollout", Arguments: map[string]any{"app": "alpha", "deployment_id": "release-base", "wait_seconds": seconds}}
	if token != nil {
		p.SetMeta(map[string]any{"progressToken": token})
	}
	return p
}

func TestWatchProgressProtocolsAndUnchangedSnapshots(t *testing.T) {
	for _, scenario := range []struct {
		version string
		http    bool
		token   any
	}{{"2025-11-25", false, "watch-one"}, {"2026-07-28", true, float64(0)}} {
		t.Run(scenario.version, func(t *testing.T) {
			f := progressFixture()
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			updates := make(chan *mcp.ProgressNotificationParams, 8)
			cs, ctx := progressClient(t, b, Options{APIBase: b.URL}, scenario.version, scenario.http, func(p *mcp.ProgressNotificationParams) { updates <- p })
			result, err := cs.CallTool(ctx, watchParams(scenario.token, 2))
			if err != nil || result.IsError {
				t.Fatalf("watch=%+v %v", result, err)
			}
			select {
			case p := <-updates:
				if p.ProgressToken != scenario.token || p.Progress != 1 || p.Total != 0 {
					t.Fatalf("progress=%+v", p)
				}
				var state watchProgressState
				if json.Unmarshal([]byte(p.Message), &state) != nil || !state.Live || state.Healthy || state.Completed || strings.Contains(p.Message, testToken) {
					t.Fatal("progress confused live artifact with health/completion")
				}
			default:
				t.Fatal("requested progress missing")
			}
			select {
			case p := <-updates:
				t.Fatalf("unchanged snapshot sent another update: %+v", p)
			default:
			}
			// Clients without progress support retain ordinary bounded results.
			result, err = cs.CallTool(ctx, watchParams(nil, 1))
			if err != nil || result.IsError {
				t.Fatal("watch without progress failed")
			}
			select {
			case <-updates:
				t.Fatal("unsolicited notification without token")
			default:
			}
			if f.writes != 0 {
				t.Fatal("progress changed customer intent")
			}
		})
	}
}

func TestWatchProgressChangedStateAndTerminalFailure(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	updates := make(chan *mcp.ProgressNotificationParams, 8)
	cs, ctx := progressClient(t, b, Options{APIBase: b.URL}, "2026-07-28", false, func(p *mcp.ProgressNotificationParams) {
		updates <- p
		if p.Progress == 1 {
			f.mu.Lock()
			d := f.deployments["release-base"]
			d.Status = "failed"
			d.Error = testToken
			f.deployments[d.ID] = d
			f.mu.Unlock()
		}
	})
	result, err := cs.CallTool(ctx, watchParams("changes", 3))
	if err != nil || result.IsError {
		t.Fatalf("watch=%+v %v", result, err)
	}
	for want := float64(1); want <= 2; want++ {
		select {
		case p := <-updates:
			if p.Progress != want || strings.Contains(p.Message, testToken) {
				t.Fatalf("progress=%+v", p)
			}
			var state watchProgressState
			if json.Unmarshal([]byte(p.Message), &state) != nil || state.Failed != (want == 2) {
				t.Fatal("terminal state missing")
			}
		// Progress notifications arrive asynchronously and can trail the
		// tool result on a loaded runner (rc.264 release gate).
		case <-time.After(5 * time.Second):
			t.Fatal("state transition missing")
		}
	}
	if f.writes != 0 {
		t.Fatal("observation mutated intent")
	}
}

func TestWatchProgressCancellationCancelsAPIReadOnly(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		http, progress bool
	}{{"session", false, true}, {"http_progress", true, true}, {"http_no_progress", true, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			var token any
			if scenario.progress {
				token = "cancel-observation"
			}

			f := progressFixture()
			entered, cancelled := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/deployments/release-base" && reads.Add(1) == 2 {
					close(entered)
					<-r.Context().Done()
					close(cancelled)
					return
				}
				f.serveHTTP(w, r)
			}))
			t.Cleanup(b.Close)
			cs, ctx := progressClient(t, b, Options{APIBase: b.URL}, "2026-07-28", scenario.http, func(*mcp.ProgressNotificationParams) {})
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := cs.CallTool(callCtx, watchParams(token, 5)); done <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("second API read did not start")
			}
			cancelStart := time.Now()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel=%v", err)
				}
			case <-ctx.Done():
				t.Fatal("client call did not cancel")
			}
			select {
			case <-cancelled:
			case <-ctx.Done():
				t.Fatal("API read did not inherit cancellation")
			}
			if time.Since(cancelStart) > time.Second {
				t.Fatal("cancellation waited for watch timeout")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.deployments["release-base"].Status != "live" {
				t.Fatal("cancelling a watch cancelled the deployment")
			}

		})
	}
}

func TestWatchProgressConsentRecreatedEnvironment(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	var count atomic.Int32
	cs, ctx := progressClient(t, b, opts, "2026-07-28", false, func(*mcp.ProgressNotificationParams) {
		if count.Add(1) == 1 {
			f.mu.Lock()
			f.envID = "recreated"
			f.mu.Unlock()
		}
	})
	p := &mcp.CallToolParams{Name: "gregale_watch_rollout", Arguments: map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "deployment_id": "stage-base", "wait_seconds": 3}}
	p.SetProgressToken("scoped")
	result, err := cs.CallTool(ctx, p)
	if err != nil || !result.IsError || count.Load() != 1 {
		t.Fatalf("recreated watch=%+v %v count=%d", result, err, count.Load())
	}
	if f.writes != 0 {
		t.Fatal("progress changed environment intent")
	}
}

func TestWatchProgressBoundsAndNoRawDiagnostics(t *testing.T) {
	manager := &management{}
	var messages []string
	p := &watchProgress{management: manager, args: appArgs{}, token: "bounds", notify: func(_ context.Context, n *mcp.ProgressNotificationParams) error {
		messages = append(messages, n.Message)
		return nil
	}}
	ctx := context.WithValue(t.Context(), watchProgressKey{}, p)
	out := watchRolloutResult{Build: &api.BuildResponse{Status: testToken}, Deployment: deploymentView{Error: testToken, ServiceRolloutHandoff: &api.ServiceRolloutHandoffResponse{Phase: testToken, Action: testToken, MissingGateways: []string{testToken}, LastError: testToken}}, Health: api.AppHealthResponse{Summary: testToken, Capacity: api.AppHealthCapacity{Known: true}}}
	for i := 0; i < 100; i++ {
		out.Health.Capacity.Ready = i
		if err := reportWatchProgress(ctx, out); err != nil {
			t.Fatal(err)
		}
	}
	if len(messages) != maxWatchProgressUpdates {
		t.Fatalf("unbounded progress: %d", len(messages))
	}
	for _, message := range messages {
		if strings.Contains(message, testToken) || len(message) > 1024 {
			t.Fatal("progress leaked diagnostics or exceeded bound")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	p.sent = 0
	if err := reportWatchProgress(cancelled, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled send=%v", err)
	}
	if len(messages) != maxWatchProgressUpdates {
		t.Fatal("notification sent after cancellation")
	}
}

func TestWatchProgressRejectsInvalidTokenWithoutNotification(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	var count atomic.Int32
	cs, ctx := progressClient(t, b, Options{APIBase: b.URL}, "2026-07-28", false, func(*mcp.ProgressNotificationParams) { count.Add(1) })
	for _, token := range []any{"", strings.Repeat("x", 129), map[string]any{"private": testToken}} {
		result, err := cs.CallTool(ctx, watchParams(token, 1))
		if err != nil || !result.IsError {
			t.Fatalf("invalid token accepted: %+v %v", result, err)
		}
	}
	if count.Load() != 0 || f.writes != 0 {
		t.Fatal("invalid token emitted progress or wrote intent")
	}
}

func TestWatchProgressWorkerConvergenceNamesExactTarget(t *testing.T) {
	out := watchRolloutResult{Deployment: deploymentView{ID: "target"}, Worker: &api.WorkerRolloutObservation{GenerationConverged: true, SelectedDeploymentID: "neighbor", Running: 1}}
	if rolloutProgressState(out).WorkerConverged {
		t.Fatal("neighbor residency reported target convergence")
	}
	out.Worker.SelectedDeploymentID = "target"
	if !rolloutProgressState(out).WorkerConverged {
		t.Fatal("target convergence missing")
	}
}
