package sched

import (
	"context"
	"testing"
)

func TestStartupOOMRecorderSharedThroughDerivedContexts(t *testing.T) {
	rec := &StartupOOMRecorder{}
	ctx := WithStartupOOMRecorder(context.Background(), rec)
	// The gateway's wake leader runs on a detached copy of the request
	// context; it must still reach the request's recorder.
	NoteStartupOOM(context.WithoutCancel(ctx), "i-1")
	NoteStartupOOM(ctx, "")
	if !rec.Has("i-1") {
		t.Fatal("instance noted through a derived context is missing")
	}
	if rec.Has("i-2") || rec.Has("") {
		t.Fatal("unnoted instance reported")
	}
	// No recorder: a no-op, and a nil recorder reports nothing.
	NoteStartupOOM(context.Background(), "i-1")
	var none *StartupOOMRecorder
	if none.Has("i-1") {
		t.Fatal("nil recorder reported an instance")
	}
}
