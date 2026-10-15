// adr: 732
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type forkBackend struct {
	*fakeBackend
	target Target
	token  string
}

func (b *forkBackend) ResolveForkTarget(_ context.Context, appID, forkID, token string) (Target, bool, error) {
	if appID != b.app.ID || forkID != "fork-1" || token != b.token {
		return Target{}, false, nil
	}
	return b.target, true, nil
}

func newForkTestHandler(t *testing.T) (*Handler, *forkBackend, *http.Request) {
	t.Helper()
	b := &forkBackend{
		fakeBackend: &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: "jane-api.apps.dom"},
		target:      Target{AppID: "app-1", InstanceID: "fork-instance", NodeID: "fork-node:8080"},
		token:       "gfk_secret",
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	return h, b, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/debug", nil)
}

// A fork request reaches the fork's node with the fork headers stripped,
// and never admits (wakes) the app.
func TestServeForkRoutesToTheForkWithoutWaking(t *testing.T) {
	h, b, req := newForkTestHandler(t)
	var gotAddr string
	var gotHeaders http.Header
	h.proxyFor = func(addr string, _ int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAddr, gotHeaders = addr, r.Header.Clone()
			_, _ = io.WriteString(w, "from fork")
		})
	}
	req.Header.Set(api.ForkHeader, "fork-1")
	req.Header.Set(api.ForkTokenHeader, "gfk_secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "from fork" || rec.Header().Get("X-Gregale-Fork-Served") != "1" {
		t.Fatalf("fork request = %d %q", rec.Code, rec.Body.String())
	}
	if gotAddr != "fork-node:8080" {
		t.Errorf("proxied to %q, want the fork's node", gotAddr)
	}
	if gotHeaders.Get(api.ForkHeader) != "" || gotHeaders.Get(api.ForkTokenHeader) != "" {
		t.Error("fork headers reached the guest")
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Errorf("fork request admitted %d wakes, want 0", got)
	}
}

func TestServeForkWrongTokenIsANotFound(t *testing.T) {
	for name, edit := range map[string]func(*http.Request){
		"wrong token":  func(r *http.Request) { r.Header.Set(api.ForkTokenHeader, "gfk_guess") },
		"no token":     func(r *http.Request) { r.Header.Del(api.ForkTokenHeader) },
		"unknown fork": func(r *http.Request) { r.Header.Set(api.ForkHeader, "fork-2") },
	} {
		t.Run(name, func(t *testing.T) {
			h, b, req := newForkTestHandler(t)
			h.proxyFor = func(string, int64) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("proxied a refused fork request") })
			}
			req.Header.Set(api.ForkHeader, "fork-1")
			req.Header.Set(api.ForkTokenHeader, "gfk_secret")
			edit(req)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if got := atomic.LoadInt32(b.Admits()); got != 0 {
				t.Errorf("refused fork request admitted %d wakes", got)
			}
		})
	}
}

func TestForkTargetFromState(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	instanceID := "fork-instance"
	fork := state.AppFork{
		AppID: "app-1", Status: state.AppForkRunning, ExpiresAt: now.Add(time.Hour), InstanceID: &instanceID,
		AccessTokenHash: api.AppForkAccessTokenHash("gfk_ok"),
	}
	instance := state.Instance{ID: instanceID, AppID: "app-1", Mode: string(state.InstanceModeFork),
		State: string(state.StateRunning), NodeID: "node-1", DeploymentID: "dep-1"}
	if target, ok := ForkTargetFromState(fork, instance, 8080, "gfk_ok", now); !ok || target.InstanceID != instanceID || target.Port != 8080 {
		t.Fatalf("valid fork = %+v, %v", target, ok)
	}
	for name, tc := range map[string]struct {
		edit  func(*state.AppFork, *state.Instance)
		token string
	}{
		"wrong token":       {func(*state.AppFork, *state.Instance) {}, "gfk_bad"},
		"expired":           {func(f *state.AppFork, _ *state.Instance) { f.ExpiresAt = now }, "gfk_ok"},
		"not running":       {func(f *state.AppFork, _ *state.Instance) { f.Status = state.AppForkRestoring }, "gfk_ok"},
		"no token hash":     {func(f *state.AppFork, _ *state.Instance) { f.AccessTokenHash = nil }, "gfk_ok"},
		"serving instance":  {func(_ *state.AppFork, i *state.Instance) { i.Mode = string(state.InstanceModeNormal) }, "gfk_ok"},
		"other app":         {func(_ *state.AppFork, i *state.Instance) { i.AppID = "app-2" }, "gfk_ok"},
		"instance stopped":  {func(_ *state.AppFork, i *state.Instance) { i.State = string(state.StateStopped) }, "gfk_ok"},
		"instance mismatch": {func(_ *state.AppFork, i *state.Instance) { i.ID = "other" }, "gfk_ok"},
	} {
		f, i := fork, instance
		tc.edit(&f, &i)
		if _, ok := ForkTargetFromState(f, i, 8080, tc.token, now); ok {
			t.Errorf("%s: routed, want refused", name)
		}
	}
}
