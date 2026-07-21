package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbeWakeState_Cold(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-faas-wake", "cold")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cold, err := probeWakeState(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("cold probe err = %v, want nil", err)
	}
	if !cold {
		t.Fatal("cold probe returned false, want true")
	}
}

func TestProbeWakeState_Warm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Header intentionally omitted — warm path.
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cold, err := probeWakeState(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("warm probe err = %v, want nil", err)
	}
	if cold {
		t.Fatal("warm probe returned true, want false")
	}
}

func TestProbeWakeState_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	defer srv.Close()

	// Tight deadline — server sleeps longer than 500 ms.
	cold, err := probeWakeState(srv.URL, 500*time.Millisecond)
	if err == nil {
		t.Fatal("timeout probe err = nil, want non-nil")
	}
	if cold {
		t.Fatal("timeout probe returned true, want false")
	}
}

func TestProbeWakeState_5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cold, err := probeWakeState(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("5xx probe err = %v, want nil (header absent)", err)
	}
	if cold {
		t.Fatal("5xx probe returned true, want false (no cold header on error)")
	}
}

func TestProbeWakeState_HeaderValueExactMatch(t *testing.T) {
	// Wire contract per pkg/gateway/handler_test.go is exactly "cold".
	// A misspelled or capitalized value must NOT count as cold.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-faas-wake", "Cold") // capital C
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cold, err := probeWakeState(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("probe err = %v, want nil", err)
	}
	if cold {
		t.Fatal(`probe matched "Cold"; wire contract is exact "cold" only`)
	}
}

func TestProbeWakeState_InvalidURL(t *testing.T) {
	cold, err := probeWakeState("http://[::1]:bad", 100*time.Millisecond)
	if err == nil {
		t.Fatal("invalid URL err = nil, want non-nil")
	}
	if cold {
		t.Fatal("invalid URL returned true, want false")
	}
	// Sanity-check the error message mentions the URL.
	if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "port") {
		t.Logf("note: unexpected error shape: %v", err)
	}
}
