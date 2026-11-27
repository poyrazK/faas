package gateway

// adr: 1137

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A declared target the caller keeps not using stops being woken ahead after
// api.ServiceWakeAheadDeclaredMaxMisses unused or failed speculations, and
// comes back once a real call to it is seen (account usage audit 2026-11-21:
// frontend woke checkoutservice 20 times a day, used 0).
func TestWakeAheadLearnerDeclaredStopsAfterUnusedWakes(t *testing.T) {
	clock := &wakeAheadClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	l := NewWakeAheadLearner(clock.now, nil)
	declared := []string{"cart", "checkout"}
	for i := 0; i < api.ServiceWakeAheadDeclaredMaxMisses; i++ {
		if !l.ObserveWake("web") {
			t.Fatalf("wake %d not observed", i)
		}
		if got := l.PredictWithDeclared("web", declared); !slices.Equal(got, declared) {
			t.Fatalf("wake %d predicted %v, want %v", i, got, declared)
		}
		l.ClaimSpeculation("web", "cart")
		l.ClaimSpeculation("web", "checkout")
		// cart fails to wake; checkout wakes and is called.
		l.ReleaseSpeculation("cart")
		l.ObserveCall("web", "checkout", false)
		clock.after(api.ServiceWakeAheadFollowWindow + time.Second)
	}
	l.ObserveWake("web")
	if got := l.PredictWithDeclared("web", declared); !slices.Equal(got, []string{"checkout"}) {
		t.Fatalf("after %d misses predicted %v, want [checkout]", api.ServiceWakeAheadDeclaredMaxMisses, got)
	}
	l.ObserveCall("web", "cart", true)
	if got := l.PredictWithDeclared("web", declared); !slices.Equal(got, declared) {
		t.Fatalf("after a real call predicted %v, want %v", got, declared)
	}
}

// A speculative wake that expires unused counts as a miss too.
func TestWakeAheadLearnerDeclaredUnusedExpiryCounts(t *testing.T) {
	clock := &wakeAheadClock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	l := NewWakeAheadLearner(clock.now, nil)
	for i := 0; i < api.ServiceWakeAheadDeclaredMaxMisses; i++ {
		l.ObserveWake("web")
		l.PredictWithDeclared("web", []string{"ads"})
		l.ClaimSpeculation("web", "ads")
		clock.after(api.ServiceWakeAheadFollowWindow + time.Second)
	}
	l.ObserveWake("web") // sweeps the last expired speculation
	if got := l.PredictWithDeclared("web", []string{"ads"}); len(got) != 0 {
		t.Fatalf("predicted %v after %d unused wakes, want none", got, api.ServiceWakeAheadDeclaredMaxMisses)
	}
}
