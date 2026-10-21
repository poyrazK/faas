package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestPollFailureLogOncePerErrorAndRecovery(t *testing.T) {
	var l pollFailureLog
	steps := []struct {
		key, err string
		ok       bool
		want     bool
	}{
		{key: "i-1/rx", err: "veth missing", want: true},
		{key: "i-1/rx", err: "veth missing", want: false},
		{key: "i-1/rx", err: "permission denied", want: true},
		{key: "i-1/rx", ok: true, want: true},
		{key: "i-1/rx", ok: true, want: false},
		{key: "i-1/rx", err: "veth missing", want: true},
	}
	for i, step := range steps {
		var got bool
		if step.ok {
			got = l.succeeded(step.key)
		} else {
			got = l.failed(step.key, step.err)
		}
		if got != step.want {
			t.Fatalf("step %d (%+v) = %v, want %v", i, step, got, step.want)
		}
	}
	l.retain(func(string) bool { return false })
	if !l.failed("i-1/rx", "veth missing") {
		t.Fatal("a pruned key must log again")
	}
}

// H10-60: a netns that keeps failing is logged once, not on every tick.
func TestEgressNetnsPollerLogsAPersistentFailureOnce(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	pop := func(context.Context, string) (map[string]uint64, error) { return nil, errors.New("netns gone") }
	p := newEgressNetnsPoller(newRecordedEgress(), wire.NewOpsMetrics("vmmd"), pop, log)
	live := map[string]fcvm.LiveEgressInstance{"i-1": {AppID: "app-1", Netns: "fc-i-1"}}
	start := time.Unix(1_700_000_000, 0)
	for tick := 0; tick < 240; tick++ {
		p.tick(context.Background(), start.Add(time.Duration(tick)*250*time.Millisecond), live)
	}
	if n := strings.Count(out.String(), "per-instance egress poll failed"); n != 1 {
		t.Fatalf("logged %d failure lines over 240 ticks, want 1:\n%s", n, out.String())
	}
}
