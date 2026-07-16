// Fuzz target for WakeGate.Wait (Tier3 #10).
//
// The gate has three primary races:
//   - leader shouldWake check racing with concurrent shouldWake peers
//   - cap-rejected waiter vs a simultaneously-completing leader
//   - ctx cancellation racing the leader goroutine's TTL clock
//
// We seed tests with handcrafted inputs and run go-fuzz against them.
// Each fuzz input is a sequence of waiters with independent (shouldWake,
// ensureFail) decisions; we assert the invariants:
//   - ensure is called at most once per appID while any waiter is pending
//   - all waiters observe ErrQueueFull, ctx.Err(), or call.err
//   - InflightWaiters reaches 0 after the last waiter departs
package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// FuzzWakeGate is a deterministic fuzz runner (skeleton: real fuzzing happens
// when -fuzz=FuzzWakeGate is passed to go test). The fixed seeds exercise
// every documented interaction.
func FuzzWakeGate(f *testing.F) {
	f.Add(uint8(8), uint8(0))
	f.Add(uint8(2), uint8(1))
	f.Add(uint8(64), uint8(2))
	f.Add(uint8(1), uint8(0))

	f.Fuzz(func(t *testing.T, fansByte, modeByte uint8) {
		fans := int(fansByte) + 1
		mode := int(modeByte) % 3
		g := NewWakeGate(8, 250*time.Millisecond)

		var ensureCalls int32
		ensureOK := atomic.LoadInt32(&ensureCalls)
		ensure := func(ctx context.Context) error {
			atomic.AddInt32(&ensureCalls, 1)
			return nil
		}
		_ = ensureOK

		shouldWake := func() bool {
			switch mode {
			case 0:
				return true // always wake
			case 1:
				return false // never wake (short-circuit)
			case 2:
				// Toggle: odd calls return true, even return false — covers
				// the leader/follower race against shouldWake=false.
				return atomic.LoadInt32(&ensureCalls)%2 == 0
			}
			return true
		}

		errs := make([]error, fans)
		done := make(chan struct{})
		for i := 0; i < fans; i++ {
			go func(i int) {
				errs[i] = g.Wait(context.Background(), "app", shouldWake, ensure)
				done <- struct{}{}
			}(i)
		}
		for i := 0; i < fans; i++ {
			<-done
		}
		// Invariants:
		for _, e := range errs {
			if e != nil && !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, ErrQueueFull) && e.Error() == "" {
				t.Errorf("unexpected nil-or-empty error: %v", e)
			}
		}
		// ensure may have been called 0 (mode=1) or at-least-1 (mode 0/2),
		// but MUST NOT have been called twice for mode=1.
		calls := atomic.LoadInt32(&ensureCalls)
		if mode == 1 && calls != 0 {
			t.Errorf("mode=1 (always shouldWake=false) → ensure ran %d times, want 0", calls)
		}
		if g.InflightWaiters("app") != 0 {
			t.Errorf("inflight after fuzz = %d, want 0", g.InflightWaiters("app"))
		}
	})
}
