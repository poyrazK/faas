package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadTestManifestValidatesAndResolvesSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gregale-test.yaml")
	content := "version: 1\nscenarios:\n  customer-export:\n    project: export-api\n    source: .\n    command: [go, test, ./test]\n    postgres: true\n    buckets: [{name: exports, prefix: EXPORT_STORAGE}]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarios, sourceDir, err := readTestManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDir != dir || !scenarios["customer-export"].Postgres || len(scenarios["customer-export"].Command) != 3 {
		t.Fatalf("manifest = %+v, source = %q", scenarios, sourceDir)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "command:", "unknown_field: x\n    command:", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("unknown scenario field was accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, "EXPORT_STORAGE", "bad-prefix", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifest(path); err == nil {
		t.Fatal("invalid bucket binding prefix was accepted")
	}
}

func TestTestProxyPreservesRequestAndCapturesWakeEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" || r.URL.Path != "/exports" || r.Header.Get("X-Customer") != "a" {
			t.Errorf("forwarded request: host=%q path=%q customer=%q", r.Host, r.URL.Path, r.Header.Get("X-Customer"))
		}
		w.Header().Set("X-Faas-Wake", "restored")
		w.Header().Set("X-Faas-Wake-ID", "wake-123")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	request, err := http.NewRequest(http.MethodPost, proxy.URL+"/exports", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Customer", "a")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	got := recorder.snapshot()
	if got.Requests != 1 || got.Header != "restored" || got.WakeID != "wake-123" || got.Status != http.StatusAccepted {
		t.Fatalf("evidence = %+v", got)
	}
	if err := verifyTestProfile(context.Background(), api.NewClient(upstream.URL, "token"), "api", "warm", &got); err == nil {
		t.Fatal("a restored request satisfied the warm profile")
	}
}

func TestSelectedTestProfiles(t *testing.T) {
	profiles, err := selectedTestProfiles("all")
	if err != nil || strings.Join(profiles, ",") != "warm,cold,restored" {
		t.Fatalf("all profiles = %v, %v", profiles, err)
	}
	if _, err := selectedTestProfiles("coldish"); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestTestProxyRecordsFirstRequestEvenWhenItFinishesSecond(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			close(firstStarted)
			<-releaseFirst
			w.Header().Set("X-Faas-Wake", "cold")
			w.Header().Set("X-Faas-Wake-ID", "wake-first")
		} else {
			w.Header().Set("X-Faas-Wake", "hot")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy, recorder := newTestProxy(target)
	defer proxy.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		response, err := http.Get(proxy.URL + "/first")
		if err != nil {
			t.Errorf("first request: %v", err)
			return
		}
		_ = response.Body.Close()
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not start")
	}
	response, err := http.Get(proxy.URL + "/second")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	close(releaseFirst)
	wg.Wait()
	got := recorder.snapshot()
	if got.Requests != 2 || got.Header != "cold" || got.WakeID != "wake-first" {
		t.Fatalf("evidence = %+v", got)
	}
}

func TestVerifyTestProfileUsesCompletedWakeMethod(t *testing.T) {
	for _, tc := range []struct {
		profile, header, method string
	}{
		{profile: "cold", header: "cold", method: "cold_boot"},
		{profile: "restored", header: "restored", method: "restore"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/api/wakes/wake-123/timeline" {
					t.Errorf("timeline path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"wake_id":"wake-123","app_id":"api","events":[{"at":"2026-09-28T00:00:00Z","kind":"wake.boot_completed","actor":"schedd","data":{"method":"` + tc.method + `"}}]}`))
			}))
			defer server.Close()
			evidence := testWakeEvidence{Requests: 1, Header: tc.header, WakeID: "wake-123"}
			if err := verifyTestProfile(context.Background(), api.NewClient(server.URL, "token"), "api", tc.profile, &evidence); err != nil {
				t.Fatal(err)
			}
			if evidence.Method != tc.method {
				t.Fatalf("method = %q, want %q", evidence.Method, tc.method)
			}
		})
	}
}

func TestDeleteTestBucketEmptiesBeforeDelete(t *testing.T) {
	objects := map[string]bool{"exports/a": true, "exports/b": true}
	deletedBucket := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/test-app/buckets/exports" && r.URL.Path != "/v1/apps/test-app/buckets/exports/objects" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/objects"):
			if len(objects) == 0 {
				_, _ = w.Write([]byte(`{"items":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"key":"exports/a"},{"key":"exports/b"}]}`))
			}
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/objects"):
			delete(objects, r.URL.Query().Get("key"))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/test-app/buckets/exports":
			if len(objects) != 0 {
				t.Error("bucket deleted with objects remaining")
			}
			deletedBucket = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	if err := deleteTestBucket(context.Background(), api.NewClient(server.URL, "token"), "test-app", "exports"); err != nil {
		t.Fatal(err)
	}
	if !deletedBucket || len(objects) != 0 {
		t.Fatalf("cleanup incomplete: deleted=%v objects=%v", deletedBucket, objects)
	}
}
