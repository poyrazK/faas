package main

import (
	"context"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestConsumeLogStreamDrainsDegradedEventBeforeEOF(t *testing.T) {
	events := make(chan api.Event, 1)
	errs := make(chan error, 1)
	events <- api.Event{Event: "degraded", Data: `{"reason":"upstream unavailable"}`}
	close(events)
	errs <- io.EOF
	close(errs)

	visited := 0
	code, err := consumeLogStream(context.Background(), events, errs, func(event api.Event) (bool, int) {
		visited++
		if event.Event == "degraded" {
			return true, 3
		}
		return false, 0
	})
	if err != nil || code != 3 || visited != 1 {
		t.Fatalf("consumeLogStream = (%d, %v), visited=%d; want degraded exit 3 after one event", code, err, visited)
	}
}

func TestConsumeLogStreamDrainsLogFramesInOrderBeforeTransportError(t *testing.T) {
	events := make(chan api.Event, 2)
	errs := make(chan error, 1)
	events <- api.Event{Event: "log", Data: "first"}
	events <- api.Event{Event: "log", Data: "second"}
	close(events)
	wantErr := io.ErrUnexpectedEOF
	errs <- wantErr
	close(errs)

	var got []string
	code, err := consumeLogStream(context.Background(), events, errs, func(event api.Event) (bool, int) {
		got = append(got, event.Data)
		return false, 0
	})
	if code != 0 || err != wantErr || len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("consumeLogStream = (%d, %v), events=%v", code, err, got)
	}
}
