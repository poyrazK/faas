package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// A safe deploy of a quiet app holds each of the adaptive ladder's three
// stages for up to the 5-minute low-traffic bound after the release goes
// live. The default 20-minute wait covers only the build, so the CLI exited
// 3 at 10% while the server completed the rollout (Hunt 12).
func TestSafeDeployWaitTimeoutCoversTheRollout(t *testing.T) {
	for in, want := range map[int]int{1200: 1200 + 3*5*60, 60: 60 + 3*5*60} {
		if got := main.SafeDeployWaitTimeoutSeconds(in); got != want {
			t.Errorf("SafeDeployWaitTimeoutSeconds(%d) = %d, want %d", in, got, want)
		}
	}
}
