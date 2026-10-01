package vmmdgrpc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNamespaceBridgeReadinessRecords(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantError   bool
	}{
		{"ready", "OK\n", false},
		{"diagnostic", "ERR dial failed\n", false},
		{"exact limit", strings.Repeat("x", api.NamespaceBridgeReadinessMaxBytes-1) + "\n", false},
		{"oversized", strings.Repeat("x", api.NamespaceBridgeReadinessMaxBytes) + "\n", true},
		{"no newline", strings.Repeat("x", api.NamespaceBridgeReadinessMaxBytes+1), true},
		{"truncated", "OK", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			done := make(chan error, 1)
			go func() { _, err := writer.Write([]byte(tc.input)); _ = writer.Close(); done <- err }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			line, err := namespaceBridgeReadiness(ctx, reader)
			if (err != nil) != tc.wantError {
				t.Fatalf("line=%q error=%v", line, err)
			}
			if !tc.wantError && line != tc.input {
				t.Fatalf("record changed: %q", line)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("descriptor remains open: %v", err)
			}
		})
	}
}

func TestNamespaceBridgeReadinessCancellation(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked", true: "pre-canceled"}[alreadyCanceled], func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			done := make(chan error, 1)
			go func() { _, err := namespaceBridgeReadiness(ctx, reader); done <- err }()
			if !alreadyCanceled {
				select {
				case err := <-done:
					t.Fatalf("silent live helper returned before cancellation: %v", err)
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not release readiness read")
			}
			if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("descriptor remains open: %v", err)
			}
		})
	}
}

func TestNamespaceBridgeReadinessDeadline(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = namespaceBridgeReadiness(ctx, reader)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("descriptor remains open: %v", err)
	}
}
