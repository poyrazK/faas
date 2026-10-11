package guestprofiling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
	"github.com/onebox-faas/faas/pkg/profiling"
)

// This exercises real Go sampling and Pyroscope merging through a local control
// bridge. VM authentication, vsock and restore require a separate native run.
func TestLiveRouteCollectorPyroscope(t *testing.T) {
	endpoint := os.Getenv("GREGALE_PROFILE_TEST_BACKEND")
	if endpoint == "" {
		t.Skip("requires private tenant-enabled Pyroscope")
	}
	backend, err := profiling.NewPyroscope(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	start := time.Now().Add(-time.Second)
	route := "GET /hot/{id}"
	principal := profiling.Principal{AccountID: uuid.NewString(), AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: uuid.NewString(), Generation: "1", Scope: api.DefaultEnvScope, Runtime: "go124", Plan: api.PlanHobby, StartedAt: start, Routes: []string{route}}
	service := profiling.NewService(backend, nil)
	uploads := make(chan profiling.Envelope, 8)
	errors := make(chan error, 8)
	var suspended atomic.Bool
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Suspended: suspended.Load(), Epoch: "11111111111111111111111111111111", WindowSeconds: 1})
		case "/control/ack":
			w.WriteHeader(http.StatusNoContent)
		case "/ingest":
			body, err := io.ReadAll(io.LimitReader(r.Body, api.ProfileMaxCompressedBytes+1))
			if err != nil {
				errors <- err
				w.WriteHeader(500)
				return
			}
			report, err := profileproto.DecodeRouteRequestHeader(r.Header.Get(profileproto.RouteRequestHeader))
			if err != nil {
				errors <- err
				w.WriteHeader(500)
				return
			}
			e := profiling.Envelope{Principal: principal, Upload: profiling.Upload{Profile: body, ProcessID: strconv.Itoa(os.Getpid()), RouteRequests: report}}
			if err := service.Push(ctx, e); err != nil {
				errors <- err
				w.WriteHeader(500)
				return
			}
			uploads <- e
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(404)
		}
	}))
	defer bridge.Close()
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, bridge.URL) }()
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		for ctx.Err() == nil {
			WithRouteRequest(ctx, route, func(ctx context.Context) {
				// A nested wrapper must not double count the request.
				WithRouteRequest(ctx, route, func(context.Context) { _ = cpuHotWork() })
			})
		}
	}()
	defer func() { cancel(); <-done; <-workDone }()
	var captures []profiling.Envelope
	for len(captures) < 2 {
		select {
		case e := <-uploads:
			captures = append(captures, e)
		case err := <-errors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("live capture timed out")
		}
	}
	suspended.Store(true)
	var count int64
	for _, e := range captures {
		if e.Upload.RouteRequests == nil || !e.Upload.RouteRequests.Complete {
			t.Fatal("missing complete route counters")
		}
		count += e.Upload.RouteRequests.Routes[route]
		// Replay must not duplicate either CPU or counter metadata.
		if err := service.Push(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if count <= 0 {
		t.Fatal("no route requests counted")
	}
	q := api.ProfileQuery{DeploymentID: principal.DeploymentID, Runtime: principal.Runtime, Start: start, End: time.Now().Add(time.Second)}
	p, err := backend.Query(ctx, principal.AccountID, principal.AppID, principal.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	view, err := profiling.View(p, q)
	if err != nil {
		t.Fatal(err)
	}
	hot := false
	for _, f := range view.Functions {
		if strings.Contains(f.Name, "[gregale-route]") {
			t.Fatalf("internal route marker exposed as application function: %+v", f)
		}
		if strings.HasSuffix(f.Name, ".cpuHotWork") {
			hot = true
		}
	}
	if !hot {
		t.Fatal("real samples did not identify workload function")
	}
	coverage, err := backend.QueryCoverage(ctx, principal.AccountID, principal.AppID, principal.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.ReceivedProfiles != 2 || coverage.LabelCountProfiles != 2 || !coverage.LabelCountsComplete {
		t.Fatalf("merged coverage: %+v", coverage)
	}
	view.Coverage = &coverage
	found := false
	for i := range view.Routes {
		if view.Routes[i].Route == route {
			view.Routes[i].Requests = &count
			found = view.Routes[i].CPUSeconds > 0
		}
	}
	if !found {
		t.Fatalf("route CPU absent: %+v", view.Routes)
	}
	profiling.AttachRouteLabelCoverage(&view)
	for _, r := range view.Routes {
		if r.Route == route && (r.LabelCoverage == nil || !r.LabelCoverage.Available || *r.LabelCoverage.LabeledRequests != count) {
			t.Fatalf("merged counters: %+v", r)
		}
	}
	q.Route = route
	filtered, err := profiling.View(p, q)
	if err != nil || filtered.CPUSeconds <= 0 || filtered.CPUSeconds > view.CPUSeconds {
		t.Fatalf("filtered profile: %+v %v", filtered, err)
	}
	foreign, err := backend.Query(ctx, uuid.NewString(), principal.AppID, principal.Scope, q)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := profiling.View(foreign, q)
	if err != nil || isolated.CPUSeconds != 0 {
		t.Fatal("route profile crossed tenants", err)
	}
	if path := os.Getenv("GREGALE_PROFILE_LIVE_REPORT"); path != "" {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("merged %d captures, %d labeled entries, %.3fs CPU; replay and tenant isolation passed", coverage.ReceivedProfiles, count, view.CPUSeconds)
}
