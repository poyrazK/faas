package functionsql

// adr: 1577

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestControlRejectsUnauthenticatedOrMalformedWitness(t *testing.T) {
	c := NewControl(t, "127.0.0.1")
	c.Arm()
	for _, tc := range []struct {
		name, method, path, token string
		status                    int
	}{
		{"auth", "POST", "/checkpoint?pid=1&generations=1,1", "wrong", 403},
		{"method", "GET", "/checkpoint?pid=1&generations=1,1", c.Token, 403},
		{"pid", "POST", "/checkpoint?pid=0&generations=1,1", c.Token, 403},
		{"negative", "POST", "/checkpoint?pid=1&generations=1,-1", c.Token, 400},
		{"one pool", "POST", "/checkpoint?pid=1&generations=1", c.Token, 400},
		{"extra pool", "POST", "/checkpoint?pid=1&generations=1,1,1", c.Token, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status := controlRequest(t.Context(), c, tc.method, tc.path, tc.token); status != tc.status {
				t.Fatalf("status %d, want %d", status, tc.status)
			}
		})
	}
	if c.Arrivals() != 0 {
		t.Fatal("invalid witness changed arrival count")
	}
	if len(c.Calls()) != 0 {
		t.Fatal("invalid witness changed callback history")
	}
}

func TestControlHoldsIndependentPhasesAndClonesWorkers(t *testing.T) {
	c := NewControl(t, "127.0.0.1")
	c.Arm()
	c.ArmRecovery()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	checkpoint, recovery := make(chan int, 1), make(chan int, 1)
	go func() { checkpoint <- controlRequest(ctx, c, "POST", "/checkpoint?pid=9&generations=0,1", c.Token) }()
	go func() { recovery <- controlRequest(ctx, c, "POST", "/recovery?pid=9&generations=1,2", c.Token) }()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for c.Arrivals() != 1 || c.RecoveryArrivals() != 1 {
		select {
		case <-ctx.Done():
			t.Fatal("control phases did not observe both workers")
		case <-ticker.C:
		}
	}
	workers := c.CheckpointWorkers()
	workers[0].Generations[0] = 999
	if c.CheckpointWorkers()[0].Generations[0] != 0 || c.RecoveryWorkers()[0].Generations[0] != 1 {
		t.Fatal("worker observations alias mutable control state")
	}
	select {
	case <-checkpoint:
		t.Fatal("checkpoint returned before release")
	default:
	}
	c.ReleaseCheckpoint()
	select {
	case status := <-checkpoint:
		if status != http.StatusNoContent {
			t.Fatal("checkpoint did not finish successfully")
		}
	case <-ctx.Done():
		t.Fatal("checkpoint remained held")
	}
	select {
	case <-recovery:
		t.Fatal("recovery returned with checkpoint release")
	default:
	}
	c.Release()
	select {
	case status := <-recovery:
		if status != http.StatusNoContent {
			t.Fatal("recovery did not finish successfully")
		}
	case <-ctx.Done():
		t.Fatal("recovery remained held")
	}
	calls := c.Calls()
	if len(calls) != 1 || calls[0].Checkpoints != 1 || calls[0].Recoveries != 1 {
		t.Fatal("release discarded actual callback history")
	}
	calls[0].Recoveries = 999
	if status := controlRequest(ctx, c, "POST", "/recovery?pid=9&generations=2,3", c.Token); status != http.StatusNoContent {
		t.Fatal("unarmed duplicate callback was not observed")
	}
	if c.Calls()[0].Recoveries != 2 || c.RecoveryArrivals() != 0 {
		t.Fatal("duplicate callback after release was hidden or history was mutable")
	}
}

func controlRequest(ctx context.Context, c *Control, method, path, token string) int {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, strings.NewReader(""))
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
