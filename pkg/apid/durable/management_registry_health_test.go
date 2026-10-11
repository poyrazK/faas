package durable

import (
	"errors"
	"sync"
	"testing"
	"time"

	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

func TestDurableEntitySocketRegistryCleanupDiscoveryHealth(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	h := &registryCleanupHealth{}
	if h.snapshot(now, false)["state"] != "disabled" {
		t.Fatal("disabled")
	}
	h.start(now)
	h.observe(true, now, now.Add(5*time.Minute), errors.New("private provider detail"))
	h.observe(false, now, now.Add(time.Minute), nil)
	v := h.snapshot(now.Add(4*time.Minute), true)
	if v["index"].(registryCleanupLaneHealth).State != "retrying" || v["reconciliation"].(registryCleanupLaneHealth).State != "stalled" {
		t.Fatal(v)
	}
	h.observe(true, now.Add(5*time.Minute), now.Add(5*time.Minute+5*time.Second), nil)
	if h.snapshot(now.Add(5*time.Minute), true)["index"].(registryCleanupLaneHealth).Failures != 0 {
		t.Fatal("failure not reset")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				h.observe(true, now, now.Add(time.Minute), nil)
				h.snapshot(now, true)
			}
		})
	}
	wg.Wait()
	h.stop()
	if h.snapshot(now, true)["state"] != "not_running" {
		t.Fatal("shutdown hidden")
	}
	h.start(now)
	if !h.snapshot(now, true)["index"].(registryCleanupLaneHealth).LastAttempt.IsZero() {
		t.Fatal("restart retained old observations")
	}
}

func TestDurableEntitySocketRegistryCleanupProgressHealth(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	c := &entitysocket.RegistryCleanup{At: now.Add(-time.Hour), NotBefore: now.Add(-30 * time.Minute), Phase: "epoch_cleanup"}
	w := &entitysocket.RegistryCleanupWorkerStatus{LastSuccessAt: now.Add(-20 * time.Minute), NextAttemptAt: now.Add(-15 * time.Minute)}
	check := func(want string) {
		t.Helper()
		if got := registryCleanupProgress(now, c, w, false, true)["state"]; got != want {
			t.Fatal(got, want)
		}
	}
	check("stalled")
	w.NextAttemptAt = now.Add(time.Minute)
	check("retry_wait")
	w.LeaseUntil = now.Add(time.Second)
	check("in_progress")
	c.NotBefore = now.Add(time.Minute)
	check("reader_grace")
	c.Phase = "complete"
	check("complete")
	c.Phase = "epoch_cleanup"
	c.NotBefore = now.Add(-time.Minute)
	if registryCleanupProgress(now, c, nil, true, true)["state"] != "observation_unavailable" {
		t.Fatal("missing observation hidden")
	}
	if registryCleanupProgress(now, c, nil, false, true)["state"] != "awaiting_observation" {
		t.Fatal("missing observation called stalled")
	}
	if registryCleanupProgress(now, c, w, false, false)["state"] != "manual" {
		t.Fatal("disabled worker called stalled")
	}
}
