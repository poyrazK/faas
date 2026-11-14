package fcvm

import (
	"testing"
	"time"
)

// Deploy-path review P8: the readiness probe ran the app's healthcheck every
// 10 ms (~80/s) for the whole boot once the app answered.
func TestReadinessProbeDelayBacksOffOnceTheAppAnswers(t *testing.T) {
	var seen readinessObservation
	for i := 0; i < 5; i++ {
		seen.connRefused++
		if got := seen.nextProbeDelay(); got != readinessProbeMinDelay {
			t.Fatalf("delay while nothing listens = %v, want %v", got, readinessProbeMinDelay)
		}
	}
	seen.responses++
	want := []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, readinessProbeMaxDelay, readinessProbeMaxDelay}
	for i, w := range want {
		if got := seen.nextProbeDelay(); got != w {
			t.Fatalf("delay %d after the app answered = %v, want %v", i, got, w)
		}
	}
	nonHTTP := readinessObservation{nonHTTP: 1}
	nonHTTP.nextProbeDelay()
	if got := nonHTTP.nextProbeDelay(); got != readinessProbeMinDelay*2 {
		t.Fatalf("second delay after a non-HTTP answer = %v, want %v", got, readinessProbeMinDelay*2)
	}
}
