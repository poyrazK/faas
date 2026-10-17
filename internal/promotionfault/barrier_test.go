package promotionfault

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestBarrierRequiresAcknowledgementAndHonorsCancellation(t *testing.T) {
	for _, scenario := range []string{"continue", "refuse", "malformed", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = server.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- Wait(ctx, client, Event{SchemaVersion: 1, Point: CutoverBeforeCommit, PID: 42}) }()
			var event Event
			if err := json.NewDecoder(server).Decode(&event); err != nil || event.Point != CutoverBeforeCommit || event.PID != 42 || event.Deadline.IsZero() || !time.Now().Before(event.Deadline) {
				t.Fatal("barrier lost process/boundary attribution", err)
			}
			switch scenario {
			case "continue", "refuse":
				if err := json.NewEncoder(server).Encode(map[string]bool{"continue": scenario == "continue"}); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if _, err := server.Write([]byte("{\"unexpected\":true}\n")); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			select {
			case err := <-result:
				if (err == nil) != (scenario == "continue") {
					t.Fatal("incorrect barrier result", err)
				}
			case <-time.After(time.Second):
				t.Fatal("barrier ignored cancellation")
			}
		})
	}
}
