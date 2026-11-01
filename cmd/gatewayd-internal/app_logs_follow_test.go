package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
)

// slowFlusherRecorder is a flusherRecorder whose writes take time, like a
// client on a slow link.
type slowFlusherRecorder struct{ *flusherRecorder }

func (r slowFlusherRecorder) Write(b []byte) (int, error) {
	time.Sleep(200 * time.Microsecond)
	return r.flusherRecorder.Write(b)
}

func (r slowFlusherRecorder) Header() http.Header { return r.flusherRecorder.Header() }

// A follow stream delivers every frame of a burst to a slow client. Frames
// used to go through a one-slot channel and were dropped whenever it was
// full, so a stack trace or a wake's first lines mostly vanished.
func TestServeAppLogs_FollowBurstIsLossless(t *testing.T) {
	const burst = 300
	stream := &replayLogStream{}
	for i := 1; i <= burst; i++ {
		stream.frames = append(stream.frames, scheddgrpc.LogFrame{InstanceID: "i-1", Seq: int64(i), Stream: "stdout", Line: fmt.Sprintf("line %d", i), WrittenAt: time.Now()})
	}
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second, Backstop: 10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		ScheddFor: &fixedScheddResolver{c: &controllableScheddClient{stream: stream}},
	}
	rec := slowFlusherRecorder{newFlusherRecorder()}
	h.serveAppLogsWithIdentityFollow(context.Background(), rec, rec, "app-1", "", "app-1", 0, time.Time{}, true, "", "", "")
	if got := strings.Count(rec.body.String(), "event: log"); got != burst {
		t.Fatalf("rendered %d of %d follow frames", got, burst)
	}
}

// The follow backstop is an idle timeout: a stream that keeps delivering
// lines is not ended at a fixed total time.
func TestServeAppLogs_BackstopResetsOnEachFrame(t *testing.T) {
	stream := newControllableScheddStream()
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second, Backstop: 150 * time.Millisecond,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		ScheddFor: &fixedScheddResolver{c: &controllableScheddClient{stream: stream}},
	}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		h.serveAppLogsWithIdentityFollow(context.Background(), rec, rec, "app-1", "", "app-1", 0, time.Time{}, true, "", "", "")
		close(done)
	}()
	for i := 1; i <= 6; i++ {
		stream.pushFrame(scheddgrpc.LogFrame{InstanceID: "i-1", Seq: int64(i), Stream: "stdout", Line: "tick", WrittenAt: time.Now()})
		time.Sleep(60 * time.Millisecond)
	}
	if strings.Contains(rec.body.String(), `"reason":"timeout"`) {
		t.Fatal("an active follow stream hit the backstop")
	}
	<-done // idle now: the backstop ends it
	if !strings.Contains(rec.body.String(), `"reason":"timeout"`) {
		t.Fatalf("idle stream did not end on the backstop: %s", rec.body.String())
	}
}

// replayLogStream returns its frames, then EOF.
type replayLogStream struct{ frames []scheddgrpc.LogFrame }

func (s *replayLogStream) Recv() (scheddgrpc.LogFrame, error) {
	if len(s.frames) == 0 {
		return scheddgrpc.LogFrame{}, io.EOF
	}
	f := s.frames[0]
	s.frames = s.frames[1:]
	return f, nil
}
