package guestprofiling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
)

//go:noinline
func cpuHotWork() uint64 {
	x := uint64(1)
	for i := 0; i < 1000000; i++ {
		x = x*6364136223846793005 + 1
	}
	return x
}

func TestGoCollectorCapturesCPUAndAcknowledgesCheckpoint(t *testing.T) {
	var suspended atomic.Bool
	uploads := make(chan []byte, 8)
	acks := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/control":
			_ = json.NewEncoder(w).Encode(control{Enabled: true, Suspended: suspended.Load(), Epoch: "11111111111111111111111111111111", WindowSeconds: 1})
		case "/control/ack":
			select {
			case acks <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case "/ingest":
			body, err := io.ReadAll(io.LimitReader(r.Body, api.ProfileMaxCompressedBytes+1))
			if err != nil {
				t.Error(err)
			}
			select {
			case uploads <- body:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); run(ctx, server.URL) }()
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		for ctx.Err() == nil {
			_ = cpuHotWork()
		}
	}()
	defer func() { cancel(); <-done; <-workDone }()
	var body []byte
	select {
	case body = <-uploads:
	case <-time.After(5 * time.Second):
		t.Fatal("CPU capture timed out")
	}
	p, err := profiling.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := profiling.NormalizeCPU(p); err != nil {
		t.Fatal(err)
	}
	view, err := profiling.View(p, api.ProfileQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if view.CPUSeconds <= 0 {
		t.Fatal("busy Go process reported no CPU")
	}
	found := false
	for _, f := range view.Functions {
		if f.Name == "github.com/onebox-faas/faas/pkg/guestprofiling.cpuHotWork" {
			found = true
		}
	}
	if !found {
		t.Fatal("CPU samples did not identify the hot function")
	}
	suspended.Store(true)
	select {
	case <-acks:
	case <-time.After(3 * time.Second):
		t.Fatal("checkpoint stop was not acknowledged")
	}
}
