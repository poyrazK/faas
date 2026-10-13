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

func TestCmdDebugCaptureRejectsInvalidRequests(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"my-app", "--type", "wall"}, {"my-app", "--duration", "5m"}, {"my-app", "--instance", "nope"}, {}} {
		if code := cmdDebugCapture(args); code == 0 {
			t.Fatalf("cmdDebugCapture(%v) succeeded", args)
		}
	}
}
