package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type replayRecordingLogs struct {
	mu      sync.Mutex
	since   []int64
	sinceAt []time.Time
	frames  []scheddgrpc.LogFrame
}

func (r *replayRecordingLogs) ScheddForApp(context.Context, string) (logStreamer, error) {
	return r, nil
}

func (r *replayRecordingLogs) StreamAppLogs(ctx context.Context, _ string, since int64, sinceAt time.Time, _ bool, _ string, _ string, _ string) (scheddgrpc.LogStream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.since = append(r.since, since)
	r.sinceAt = append(r.sinceAt, sinceAt)
	return &replayFrameStream{ctx: ctx, frames: append([]scheddgrpc.LogFrame(nil), r.frames...)}, nil
}

type replayFrameStream struct {
	ctx    context.Context
	frames []scheddgrpc.LogFrame
}

func (s *replayFrameStream) Recv() (scheddgrpc.LogFrame, error) {
	if len(s.frames) == 0 {
		<-s.ctx.Done()
		return scheddgrpc.LogFrame{}, s.ctx.Err()
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}

// A drain (re)attaches by replaying the instance rings from seq 1, bounded by
// its creation time, and skips what its cursor already delivered. It used to
// attach with cursor 0, "new lines only", so every reconnect silently lost
// the lines written while it was detached. A jump in an instance's sequence
// is counted as a gap instead of passing unnoticed.
func TestAppLogDrainReplaysRingsAndCountsSkippedLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	sender, err := logdrain.New(logdrain.Config{Kind: logdrain.KindHTTPJSON, TargetURL: server.URL, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-time.Hour).UTC()
	spec := state.AppLogDrain{ID: "drain-replay", AppID: "app-replay", AccountID: "acct", Kind: state.AppLogDrainKindHTTPJSON, CreatedAt: created}
	logs := &replayRecordingLogs{frames: []scheddgrpc.LogFrame{
		{InstanceID: "vm-1", Seq: 1, Line: "a", WrittenAt: time.Now()},
		{InstanceID: "vm-1", Seq: 2, Line: "b", WrittenAt: time.Now()},
		{InstanceID: "vm-1", Seq: 5, Line: "e", WrittenAt: time.Now()},
	}}
	store := &logDrainOwnerTestStore{logDrainHealthTestStore: logDrainHealthTestStore{drains: map[string]state.AppLogDrain{spec.ID: spec}}}
	m := newAppLogDrainManager(store, logs, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.streamWorker(ctx, spec, sender, nil)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.healthMu.Lock()
		gaps := m.health[spec.ID].GapsTotal
		m.healthMu.Unlock()
		if gaps > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	logs.mu.Lock()
	defer logs.mu.Unlock()
	if len(logs.since) == 0 || logs.since[0] != 1 || !logs.sinceAt[0].Equal(created) {
		t.Fatalf("drain attached with since=%v sinceAt=%v, want 1 and the drain's creation time", logs.since, logs.sinceAt)
	}
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	if m.health[spec.ID].GapsTotal != 1 {
		t.Fatalf("gaps = %d, want the 2→5 jump counted once", m.health[spec.ID].GapsTotal)
	}
}
