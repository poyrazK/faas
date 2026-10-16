package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerFinishesHeldJobOnStop(t *testing.T) {
	w := newWorkload("worker", "candidate", "candidate-config")
	srv := httptest.NewServer(w)
	t.Cleanup(srv.Close)
	done := make(chan observation, 1)
	go func() {
		resp, err := srv.Client().Post(srv.URL+"/", "application/json", strings.NewReader(`{"label":"in-flight","hold":true}`))
		if err != nil {
			t.Error(err)
			done <- observation{}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		var report observation
		if resp.StatusCode != http.StatusOK {
			t.Errorf("held delivery status=%d", resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
			t.Error(err)
		}
		done <- report
	}()
	deadline := time.After(3 * time.Second)
	for w.snapshot().Active != 1 {
		select {
		case <-deadline:
			// Also unblock the handler if a failed assertion races admission.
			w.beginStop()
			t.Fatal("held delivery did not start")
		case <-time.After(time.Millisecond):
		}
	}
	if w.snapshot().Completed["in-flight"] != 0 {
		t.Fatal("held job completed before the stop signal")
	}
	if !w.beginStop() {
		t.Fatal("graceful stop refused")
	}
	select {
	case report := <-done:
		if report.Generation != "candidate" || report.Mode != "candidate-config" || report.Completed["in-flight"] != 1 || !report.Stopping {
			t.Fatalf("held delivery lost configuration or completion: %+v", report)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not release the held delivery")
	}
	resp, err := srv.Client().Post(srv.URL+"/", "application/json", strings.NewReader(`{"label":"after-stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable || w.snapshot().Received["after-stop"] != 0 {
		t.Fatal("stopping worker admitted new work")
	}
}

func TestFixtureRegressionAndIgnoredStop(t *testing.T) {
	api := newWorkload("api", "candidate", "config")
	api.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/control/regress", nil))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/critical", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatal("regression control did not fail the critical probe")
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatal("critical regression incorrectly changed startup health")
	}
	w := newWorkload("worker", "stable", "config")
	w.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/control/ignore-stop", nil))
	if w.beginStop() || w.snapshot().Stopping || !w.snapshot().IgnoreStop {
		t.Fatal("ignored-stop fixture cooperated with termination")
	}
	rec = httptest.NewRecorder()
	w.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"label":"ordinary"}`)))
	if rec.Code != http.StatusOK || w.snapshot().Completed["ordinary"] != 1 {
		t.Fatalf("ignored-stop worker ceased serving: %s", rec.Body.String())
	}
}

func TestWorkerFailedDeliveryControlIsOneShot(t *testing.T) {
	w := newWorkload("worker", "baseline", "baseline-config")
	armed := httptest.NewRecorder()
	w.ServeHTTP(armed, httptest.NewRequest(http.MethodPost, "/control/fail-next", nil))
	if armed.Code != http.StatusNoContent {
		t.Fatal("failed-delivery fault was not armed")
	}
	for index, want := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		response := httptest.NewRecorder()
		w.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"label":"real-retry"}`)))
		if response.Code != want {
			t.Fatalf("attempt %d: got %d, want %d", index, response.Code, want)
		}
	}
	if w.snapshot().Completed["real-retry"] != 1 || w.snapshot().Active != 0 {
		t.Fatal("failed delivery produced a completion or leaked work")
	}
}
