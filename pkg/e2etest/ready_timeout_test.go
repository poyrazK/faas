package e2etest

import (
	"testing"
	"time"
)

func TestReadyTimeout(t *testing.T) {
	for _, tt := range []struct {
		env  string
		want time.Duration
	}{
		{"", 10 * time.Second},
		{"1", 10 * time.Second},
		{"5", 50 * time.Second},
		{" 20 ", 200 * time.Second},
		{"21", 10 * time.Second},
		{"0", 10 * time.Second},
		{"-2", 10 * time.Second},
		{"1.5", 10 * time.Second},
		{"many", 10 * time.Second},
	} {
		t.Setenv(ReadyTimeoutScaleEnv, tt.env)
		if got := readyTimeout(10 * time.Second); got != tt.want {
			t.Errorf("%s=%q: readyTimeout(10s) = %s, want %s", ReadyTimeoutScaleEnv, tt.env, got, tt.want)
		}
	}
}
