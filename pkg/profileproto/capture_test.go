package profileproto

import (
	"strings"
	"testing"
)

func TestCaptureRequestValidate(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name string
		req  CaptureRequest
		ok   bool
	}{
		{"cpu window", CaptureRequest{CaptureID: id, Kinds: []string{KindCPU}, DurationMillis: 10_000}, true},
		{"heap checkpoints", CaptureRequest{CaptureID: id, Kinds: []string{KindHeap}, DurationMillis: 10_000, CheckpointMillis: 2_000}, true},
		{"checkpoint below 1s", CaptureRequest{CaptureID: id, Kinds: []string{KindHeap}, DurationMillis: 10_000, CheckpointMillis: 500}, false},
		{"checkpoint as long as the window", CaptureRequest{CaptureID: id, Kinds: []string{KindHeap}, DurationMillis: 10_000, CheckpointMillis: 10_000}, false},
		{"bad id", CaptureRequest{CaptureID: "x", Kinds: []string{KindCPU}, DurationMillis: 10_000}, false},
		{"duplicate kinds", CaptureRequest{CaptureID: id, Kinds: []string{KindCPU, KindCPU}, DurationMillis: 10_000}, false},
		{"window too long", CaptureRequest{CaptureID: id, Kinds: []string{KindCPU}, DurationMillis: 61_000}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.req.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
