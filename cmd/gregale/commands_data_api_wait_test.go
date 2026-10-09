// adr: 650 — a refresh wait proves restart completion and uncached engine readiness.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIRefreshTestServer struct {
	client      *api.Client
	restarts    atomic.Int32
	statusReads atomic.Int32
	healthReads atomic.Int32
}

func newDataAPIRefreshTestServer(t *testing.T, wakeID string, statuses []api.RuntimeConfigRestartStatusResponse, probe http.HandlerFunc) *dataAPIRefreshTestServer {
	t.Helper()
	fixture := &dataAPIRefreshTestServer{}
	var completed atomic.Bool
	health := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.healthReads.Add(1)
		if !completed.Load() {
			t.Error("readiness was probed before the accepted restart completed")
		}
		if r.URL.Path != "/healthz" || r.Header.Get("Authorization") != "" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("public readiness request must use /healthz without owner credentials or cached responses")
		}
		probe(w, r)
	}))
	t.Cleanup(health.Close)
	transport := http.DefaultTransport
	http.DefaultTransport = health.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer owner-test-token" {
			t.Error("management request must carry the account credential")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes":
			writeJSONTest(w, api.AppResponse{ID: "app", Slug: "notes", URL: health.URL})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/notes/restart" && r.URL.RawQuery == "fresh=true":
			fixture.restarts.Add(1)
			writeJSONTest(w, api.AppRestartResponse{WakeID: wakeID})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes/runtime-config-restarts/wake-1":
			index := int(fixture.statusReads.Add(1)) - 1
			if index >= len(statuses) {
				index = len(statuses) - 1
			}
			status := statuses[index]
			if status.Status == "completed" {
				completed.Store(true)
			}
			writeJSONTest(w, status)
		default:
			t.Errorf("unexpected management request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(management.Close)
	t.Setenv("FAAS_API", management.URL)
	t.Setenv("FAAS_TOKEN", "owner-test-token")
	fixture.client = api.NewClient(management.URL, "owner-test-token")
	return fixture
}

func TestDataAPIRefreshWaitJourney(t *testing.T) {
	for _, args := range [][]string{
		{"notes", "--wait", "--timeout", "5s"},
		{"--wait", "--timeout=5s", "notes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetJSONOut(t)
			var probes atomic.Int32
			fixture := newDataAPIRefreshTestServer(t, "wake-1", []api.RuntimeConfigRestartStatusResponse{
				{WakeID: "wake-1", Status: "queued"},
				{WakeID: "wake-1", Status: "completed"},
			}, func(w http.ResponseWriter, r *http.Request) {
				if probes.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				writeJSONTest(w, map[string]bool{"ready": true})
			})
			out, stderr, restore := swapIO(t)
			defer restore()
			if code := run(append([]string{"--json", "data-api", "refresh"}, args...)); code != 0 {
				t.Fatalf("refresh exit=%d: %s", code, stderr())
			}
			decoder := json.NewDecoder(out)
			var receipt dataAPIRefreshReceipt
			if err := decoder.Decode(&receipt); err != nil || receipt.WakeID != "wake-1" || receipt.Status != "completed" || !receipt.Ready {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if err := decoder.Decode(&receipt); !errors.Is(err, io.EOF) {
				t.Fatalf("extra stdout after the receipt: %v", err)
			}
			if fixture.restarts.Load() != 1 || fixture.statusReads.Load() != 2 || fixture.healthReads.Load() != 2 {
				t.Fatal("refresh must queue once, await completion, and retry temporary unreadiness")
			}
		})
	}
}

func TestDataAPIRestartWaitAcceptsRetryProgress(t *testing.T) {
	statuses := []api.RuntimeConfigRestartStatusResponse{
		{WakeID: "wake-1", Status: "queued"},
		{WakeID: "wake-1", Status: "retrying", FailureReason: "requests_active"},
		{WakeID: "wake-1", Status: "running"},
		{WakeID: "wake-1", Status: "completed"},
	}
	fixture := newDataAPIRefreshTestServer(t, "wake-1", statuses, func(http.ResponseWriter, *http.Request) { t.Error("unexpected health request") })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitDataAPIRestart(ctx, fixture.client, "notes", "wake-1", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if fixture.statusReads.Load() != 4 || fixture.restarts.Load() != 0 {
		t.Fatal("retry progress must be observed without requeuing a restart")
	}
}

func TestDataAPIRefreshWaitFailures(t *testing.T) {
	for _, tc := range []struct {
		name, wakeID, status, statusWake, reason, want string
		healthStatus                                   int
		healthBody                                     string
		blockHealth                                    bool
	}{
		{name: "failed restart", wakeID: "wake-1", status: "failed", statusWake: "wake-1", reason: "requests_active", want: "fresh restart failed (requests_active)"},
		{name: "missing receipt", status: "completed", statusWake: "wake-1", want: "did not return a wake_id"},
		{name: "different restart", wakeID: "wake-1", status: "completed", statusWake: "other", want: "different wake_id"},
		{name: "unknown state", wakeID: "wake-1", status: "unrecognized", statusWake: "wake-1", want: "unknown restart status"},
		{name: "restart deadline", wakeID: "wake-1", status: "retrying", statusWake: "wake-1", want: "timed out waiting for Data API restart completion"},
		{name: "unready engine", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 200, healthBody: `{"ready":false}`, want: "timed out waiting for Data API readiness"},
		{name: "health request deadline", wakeID: "wake-1", status: "completed", statusWake: "wake-1", blockHealth: true, want: "timed out waiting for Data API readiness"},
		{name: "ingress denied", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 403, healthBody: "private-provider-error", want: "/healthz returned HTTP 403"},
		{name: "redirect", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 302, healthBody: "private-provider-error", want: "/healthz returned HTTP 302"},
		{name: "wrong app", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 200, healthBody: "private-provider-error", want: "valid Data API readiness response"},
		{name: "missing readiness", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 200, healthBody: `{}`, want: "valid Data API readiness response"},
		{name: "oversized response", wakeID: "wake-1", status: "completed", statusWake: "wake-1", healthStatus: 200, healthBody: `{"ready":true}` + strings.Repeat(" ", 8192), want: "valid Data API readiness response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			fixture := newDataAPIRefreshTestServer(t, tc.wakeID, []api.RuntimeConfigRestartStatusResponse{
				{WakeID: tc.statusWake, Status: tc.status, FailureReason: tc.reason},
			}, func(w http.ResponseWriter, r *http.Request) {
				if tc.blockHealth {
					<-r.Context().Done()
					return
				}
				if tc.healthStatus == http.StatusFound {
					w.Header().Set("Location", "/redirect-target")
				}
				w.WriteHeader(tc.healthStatus)
				_, _ = io.WriteString(w, tc.healthBody)
			})
			_, stderr, restore := swapIO(t)
			defer restore()
			if code := cmdDataAPIRefresh([]string{"notes", "--wait", "--timeout=300ms"}); code == 0 {
				t.Fatal("unverified refresh reported success")
			}
			diagnostic := stderr()
			if !strings.Contains(diagnostic, tc.want) || strings.Contains(diagnostic, "private-provider-error") {
				t.Fatalf("diagnostic=%q, want %q without response contents", diagnostic, tc.want)
			}
			if fixture.restarts.Load() != 1 {
				t.Fatal("waiting must not requeue an accepted restart")
			}
			if tc.status != "completed" && fixture.healthReads.Load() != 0 {
				t.Fatal("readiness cannot substitute for restart completion")
			}
		})
	}
}

func TestDataAPIRefreshRejectsInvalidWaitFlagsBeforeMutation(t *testing.T) {
	for _, args := range [][]string{
		{"notes", "--timeout=1s"}, {"notes", "--wait=false", "--timeout=1s"},
		{"notes", "--wait", "--timeout=0"}, {"notes", "--wait", "--timeout=-1s"},
		{"notes", "--wait", "--timeout=2h"}, {"notes", "extra", "--wait"},
		{"INVALID", "--wait"}, {"notes", "--unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetJSONOut(t)
			f := authedFakeAPI(t, `{}`, http.StatusOK)
			if code := cmdDataAPIRefresh(args); code != 1 || f.sawMethod != "" {
				t.Fatalf("invalid flags exit=%d request=%s", code, f.sawMethod)
			}
		})
	}
}

func TestDataAPIRefreshWaitTimeoutJSON(t *testing.T) {
	resetJSONOut(t)
	fixture := newDataAPIRefreshTestServer(t, "wake-1", []api.RuntimeConfigRestartStatusResponse{{WakeID: "wake-1", Status: "queued"}}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected health request") })
	out, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"--json", "data-api", "refresh", "notes", "--wait", "--timeout=300ms"}); code == 0 {
		t.Fatal("timed out refresh reported success")
	}
	var problem api.Problem
	decoder := json.NewDecoder(strings.NewReader(stderr()))
	if err := decoder.Decode(&problem); err != nil || problem.Code != "data_api_refresh_timeout" || !strings.Contains(problem.Detail, "wake_id=wake-1") || !strings.Contains(problem.Detail, "accepted restart is not cancelled") {
		t.Fatalf("problem=%+v err=%v", problem, err)
	}
	if err := decoder.Decode(&problem); !errors.Is(err, io.EOF) || out.Len() != 0 || fixture.restarts.Load() != 1 {
		t.Fatal("a timeout must emit one diagnostic, no success receipt, and no second restart")
	}
}

func TestDataAPIReadinessURL(t *testing.T) {
	for _, address := range []string{"http://app.example", "https://user:password@app.example", "https://app.example/path", "https://app.example?token=secret", "https://app.example/#fragment", ""} {
		if _, err := dataAPIReadinessURL(api.AppResponse{URL: address}); err == nil {
			t.Fatalf("invalid app URL accepted: %q", address)
		}
	}
	address, err := dataAPIReadinessURL(api.AppResponse{URL: "https://platform.example", CanonicalURL: "https://canonical.example/"})
	if err != nil || address != "https://canonical.example/healthz" {
		t.Fatalf("canonical readiness URL=%q err=%v", address, err)
	}
}

func TestDataAPIRefreshCancellation(t *testing.T) {
	fixture := newDataAPIRefreshTestServer(t, "wake-1", []api.RuntimeConfigRestartStatusResponse{{WakeID: "wake-1", Status: "queued"}}, func(http.ResponseWriter, *http.Request) { t.Error("unexpected health request") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitDataAPIRefresh(ctx, fixture.client, "notes", "wake-1", "https://unused.example/healthz", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait error=%v", err)
	}
	if fixture.statusReads.Load() != 0 || fixture.healthReads.Load() != 0 {
		t.Fatal("cancelled wait performed network requests")
	}
}
