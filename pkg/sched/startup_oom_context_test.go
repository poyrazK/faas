package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestStartupOOMRecorderSharedThroughDerivedContexts(t *testing.T) {
	rec := &sched.StartupOOMRecorder{}
	ctx := sched.WithStartupOOMRecorder(context.Background(), rec)
	if rec.Any() {
		t.Fatal("empty recorder reports an OOM")
	}
	// The gateway's wake leader runs on a detached copy of the request
	// context; it must still reach the request's recorder.
	sched.NoteStartupOOM(context.WithoutCancel(ctx), "i-1")
	sched.NoteStartupOOM(ctx, "")
	if !rec.Has("i-1") || !rec.Any() {
		t.Fatal("instance noted through a derived context is missing")
	}
	if rec.Has("i-2") || rec.Has("") {
		t.Fatal("unnoted instance reported")
	}
	// No recorder: a no-op, and a nil recorder reports nothing.
	sched.NoteStartupOOM(context.Background(), "i-1")
	var none *sched.StartupOOMRecorder
	if none.Has("i-1") || none.Any() {
		t.Fatal("nil recorder reported an instance")
	}
}
