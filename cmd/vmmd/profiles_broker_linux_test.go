//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/profiling"
)

func profileFrame(t *testing.T, upload profiling.Upload) []byte {
	t.Helper()
	body, err := json.Marshal(upload)
	if err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	_ = binary.Write(&frame, binary.BigEndian, uint32(len(body)))
	frame.Write(body)
	return frame.Bytes()
}

// The broker holds one stream per instance, checks the frame length before a
// node slot, and resolves the principal from the database once per lifetime
// rather than on every upload (profiling review round 4, R4-3).
func TestProfileBrokerBoundsUntrustedStreams(t *testing.T) {
	resolves := 0
	generation := "1-1"
	b := newProfileBrokerWith(func(string) (fcvm.ProfileInstanceIdentity, error) {
		return fcvm.ProfileInstanceIdentity{Generation: generation}, nil
	}, func(_ context.Context, instance string) (profiling.Principal, error) {
		resolves++
		return profiling.Principal{InstanceID: instance, Generation: generation}, nil
	})
	sent := 0
	send := func(context.Context, profiling.Envelope) error { sent++; return nil }
	frame := profileFrame(t, profiling.Upload{})

	for range 3 {
		if kind, err := b.serve(t.Context(), bytes.NewReader(frame), "i-1", send); err != nil {
			t.Fatalf("upload: %s %v", kind, err)
		}
	}
	if resolves != 1 || sent != 3 {
		t.Fatalf("resolves = %d sent = %d, want 1 and 3", resolves, sent)
	}
	generation = "2-2" // a new lifetime never reuses the old principal
	if _, err := b.serve(t.Context(), bytes.NewReader(frame), "i-1", send); err != nil || resolves != 2 {
		t.Fatalf("new generation: resolves = %d err = %v", resolves, err)
	}

	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], api.ProfileMaxFrameBytes+1)
	if kind, _ := b.serve(t.Context(), bytes.NewReader(oversized[:]), "i-2", send); kind != "protocol" || len(b.slots) != 0 {
		t.Fatalf("oversized frame: kind = %s slots held = %d", kind, len(b.slots))
	}

	// A second stream from an instance with one in flight is refused before
	// it reads anything or takes a node slot.
	stalled, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		_, _ = b.serve(t.Context(), stalled, "i-3", send)
		close(done)
	}()
	_, _ = writer.Write(frame[:4]) // the length is read; the body stalls
	if kind, err := b.serve(t.Context(), bytes.NewReader(frame), "i-3", send); kind != "overload" || err == nil {
		t.Fatalf("second stream: kind = %s err = %v", kind, err)
	}
	_ = writer.Close()
	<-done
}
