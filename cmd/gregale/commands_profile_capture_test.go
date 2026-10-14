package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugCaptureQueuesPollsAndPrints(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	var mu sync.Mutex
	polls := 0
	var created api.CreateProfileCaptureRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		base := "/v1/apps/my-app/profiles/captures"
		capture := api.ProfileCapture{ID: id, Status: api.ProfileCaptureCapturing, Kinds: []string{"cpu", "heap"}, DurationSeconds: 2, Profiles: []api.ProfileCaptureProfile{}}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			capture.Status = api.ProfileCaptureQueued
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(capture)
		case r.URL.Path == base+"/"+id:
			polls++
			if polls >= 2 {
				capture.Status, capture.InstanceID, capture.Processes = api.ProfileCaptureReady, "22222222-2222-4222-8222-222222222222", 1
				capture.Profiles = []api.ProfileCaptureProfile{{Kind: "cpu", Bytes: 3}, {Kind: "heap", Bytes: 4}}
			}
			_ = json.NewEncoder(w).Encode(capture)
		case r.URL.Path == base+"/"+id+"/view":
			kind := r.URL.Query().Get("kind")
			view := api.ProfileCaptureView{Kind: kind, Unit: "nanoseconds", Total: 2e9, Functions: []api.ProfileCaptureFunction{{Name: "hotLoop", File: "app.go", Line: 9, Self: 15e8, Total: 2e9}}}
			if kind == "heap" {
				view.Unit, view.Total, view.Functions = "bytes", 3<<20, []api.ProfileCaptureFunction{{Name: "leakyCache", Self: 3 << 20, Total: 3 << 20}}
			}
			_ = json.NewEncoder(w).Encode(view)
		case r.URL.Path == base+"/"+id+"/pprof":
			_, _ = w.Write([]byte("pprof-" + r.URL.Query().Get("kind")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	oldInterval := profileCapturePollInterval
	profileCapturePollInterval = time.Millisecond
	defer func() { profileCapturePollInterval = oldInterval }()
	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	dir := t.TempDir()
	if code := cmdDebugCapture([]string{"my-app", "--type", "cpu,heap", "--duration", "2s", "--output", dir}); code != 0 {
		t.Fatalf("cmdDebugCapture() = %d", code)
	}
	if len(created.Kinds) != 2 || created.DurationSeconds != 2 {
		t.Fatalf("request = %+v", created)
	}
	for _, want := range []string{"Capture " + id + ": ready", "hotLoop", "1.5s", "leakyCache", "3.00 MiB", "app.go:9"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout.String())
		}
	}
	for _, kind := range []string{"cpu", "heap"} {
		body, err := os.ReadFile(filepath.Join(dir, "profile-"+id+"-"+kind+".pb.gz"))
		if err != nil || string(body) != "pprof-"+kind {
			t.Fatalf("%s pprof not written: %v %q", kind, err, body)
		}
	}
}

func TestPrintSlowRequestCapture(t *testing.T) {
	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()
	c := api.ProfileCapture{ID: "11111111-1111-4111-8111-111111111111", Status: api.ProfileCaptureReady, Trigger: api.ProfileCaptureTriggerSlowRequest,
		TriggerDetail: "GET /orders/{id} in flight for 12s", RequestID: "req-abc"}
	if code := printProfileCapture(c); code != 0 {
		t.Fatalf("printProfileCapture() = %d", code)
	}
	for _, want := range []string{"while a request was stuck: GET /orders/{id} in flight for 12s", "gregale debug requests show <slug> req-abc"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
	if got := profileCaptureAmount(4, "count"); got != "4 threads" {
		t.Fatalf("profileCaptureAmount(4, count) = %q", got)
	}
}

func TestCmdDebugCaptureDiff(t *testing.T) {
	const id, base = "11111111-1111-4111-8111-111111111111", "33333333-3333-4333-8333-333333333333"
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/profiles/captures/"+id+"/diff" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(api.ProfileCaptureDiff{Kind: "heap", Unit: "bytes", BaselineTotal: 1 << 20, CandidateTotal: 5 << 20, Delta: 4 << 20,
			Functions: []api.ProfileCaptureFunctionDelta{{Name: "leakyCache", File: "cache.go", Line: 12, Baseline: 1 << 20, Candidate: 5 << 20, Delta: 4 << 20}, {Name: "trim", Baseline: 2048, Delta: -2048}},
			Warnings:  []string{"The captures come from different deployments; code changes can move cost between functions."}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugCaptureDiff([]string{"my-app", id, "--base", base, "--type", "heap"}); code != 0 {
		t.Fatalf("cmdDebugCaptureDiff() = %d", code)
	}
	if query != "base="+base+"&kind=heap" {
		t.Fatalf("query = %q", query)
	}
	for _, want := range []string{"HEAP: 1.00 MiB → 5.00 MiB (+4.00 MiB)", "Note: The captures come from different deployments", "+4.00 MiB\t1.00 MiB\t5.00 MiB\tleakyCache\tcache.go:12", "-2.0 KiB"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout.String())
		}
	}
	for _, args := range [][]string{{"my-app", id}, {"my-app", "--base", base}} {
		if code := cmdDebugCaptureDiff(args); code == 0 {
			t.Fatalf("cmdDebugCaptureDiff(%v) succeeded", args)
		}
	}
}

func TestCmdDebugCaptureRejectsInvalidRequests(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"my-app", "--type", "wall"}, {"my-app", "--duration", "5m"}, {"my-app", "--instance", "nope"}, {}} {
		if code := cmdDebugCapture(args); code == 0 {
			t.Fatalf("cmdDebugCapture(%v) succeeded", args)
		}
	}
}
