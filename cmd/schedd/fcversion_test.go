package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestResolveFCVersion(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name       string
		pinned     string
		detect     func(context.Context) (string, error)
		want       string
		wantDetect bool
	}{
		{
			name:       "pin wins and skips detection",
			pinned:     "1.7.0",
			detect:     func(context.Context) (string, error) { return "9.9.9", nil },
			want:       "1.7.0",
			wantDetect: false,
		},
		{
			name:       "no pin detects",
			detect:     func(context.Context) (string, error) { return "1.7.0", nil },
			want:       "1.7.0",
			wantDetect: true,
		},
		{
			// The CI shape: no firecracker binary on PATH. Degrading to ""
			// makes every snapshot incompatible, so wakes cold-boot. That is
			// the safe direction (ADR-005) and must stay the default.
			name:       "detection failure degrades to empty",
			detect:     func(context.Context) (string, error) { return "", errors.New("exec: firecracker: not found") },
			want:       "",
			wantDetect: true,
		},
		{
			// A pin must not be silently replaced when the detector also
			// works — otherwise a metal host would ignore the override and
			// the acceptance seam would behave differently there than in CI.
			name:       "pin wins over a failing detector too",
			pinned:     "1.7.0",
			detect:     func(context.Context) (string, error) { return "", errors.New("boom") },
			want:       "1.7.0",
			wantDetect: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detected := false
			detect := func(ctx context.Context) (string, error) {
				detected = true
				return tc.detect(ctx)
			}
			got := resolveFCVersion(context.Background(), tc.pinned, detect, discard)
			if got != tc.want {
				t.Errorf("resolveFCVersion = %q, want %q", got, tc.want)
			}
			if detected != tc.wantDetect {
				t.Errorf("detector called = %v, want %v", detected, tc.wantDetect)
			}
		})
	}
}
