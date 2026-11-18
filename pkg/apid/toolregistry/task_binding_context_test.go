package toolregistry

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTaskBindingProbeContext(t *testing.T) {
	type key struct{}
	t.Run("request cancellation and values", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.WithValue(t.Context(), key{}, "request"))
		defer cancel()
		probe, stop := taskBindingProbeContext(parent, make(chan struct{}))
		defer stop()
		if probe.Value(key{}) != "request" {
			t.Fatal("probe lost request values")
		}
		cancel()
		select {
		case <-probe.Done():
		case <-time.After(time.Second):
			t.Fatal("probe ignored request cancellation")
		}
	})
	t.Run("executor shutdown", func(t *testing.T) {
		shutdown := make(chan struct{})
		probe, stop := taskBindingProbeContext(t.Context(), shutdown)
		defer stop()
		close(shutdown)
		select {
		case <-probe.Done():
		case <-time.After(time.Second):
			t.Fatal("probe ignored executor shutdown")
		}
		if t.Context().Err() != nil {
			t.Fatal("probe shutdown canceled its request parent")
		}
	})
	t.Run("caller cleanup", func(t *testing.T) {
		probe, stop := taskBindingProbeContext(t.Context(), make(chan struct{}))
		stop()
		stop()
		if probe.Err() != context.Canceled {
			t.Fatal("cleanup left the probe running", probe.Err())
		}
	})
	t.Run("earliest deadline", func(t *testing.T) {
		start := time.Now()
		probe, stop := taskBindingProbeContext(t.Context(), make(chan struct{}))
		defer stop()
		deadline, ok := probe.Deadline()
		if !ok || deadline.After(start.Add(api.HostedTaskBindingProbeTimeout+time.Second)) {
			t.Fatal("probe exceeded its total time budget", deadline)
		}
		parent, cancel := context.WithDeadline(t.Context(), start.Add(-time.Second))
		defer cancel()
		expired, cleanup := taskBindingProbeContext(parent, make(chan struct{}))
		defer cleanup()
		if expired.Err() != context.DeadlineExceeded {
			t.Fatal("probe renewed an expired request deadline", expired.Err())
		}
	})
}
