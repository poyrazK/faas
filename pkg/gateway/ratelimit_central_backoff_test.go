// adr: 570
package gateway

import (
	"fmt"
	"testing"
	"time"
)

type classifiedCentralTestError struct{ backoff bool }

func (e *classifiedCentralTestError) Error() string                  { return "central consume failed" }
func (e *classifiedCentralTestError) CanBackoffCentralConsult() bool { return e.backoff }

func TestLimiter_ClassifiedCentralFailurePreservesRecovery(t *testing.T) {
	for _, dimension := range []string{"app", "consumer"} {
		for _, backoff := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/backoff=%v", dimension, backoff), func(t *testing.T) {
				central := newFakeCentral()
				central.consumeResult = func() (int, bool, error) {
					return 0, false, fmt.Errorf("backend operation: %w", &classifiedCentralTestError{backoff: backoff})
				}
				frozen := time.Unix(1_700_000_000, 0)
				limiter := NewLimiterWithCentralAndClock(central, func() time.Time { return frozen })
				allow := func() bool {
					return limiter.AllowWithCentralParams(t.Context(), "app", 10, 20,
						"app:00000000-0000-0000-0000-000000000001:hobby")
				}
				if dimension == "consumer" {
					allow = func() bool {
						return limiter.AllowWithCentralConsumerKey(t.Context(), "rule", "header", "consumer", 10, 20, 100,
							"rule:00000000-0000-0000-0000-000000000001:hobby")
					}
				}
				if allow() {
					t.Fatal("failed shared consume admitted a local token")
				}
				central.consumeResult = nil
				if got, want := allow(), !backoff; got != want {
					t.Fatalf("immediate recovery admitted=%v, want %v", got, want)
				}
				wantCalls := int64(2)
				if backoff {
					wantCalls = 1
				}
				if got := central.consumeCalls.Load(); got != wantCalls {
					t.Fatalf("shared consumes=%d, want %d", got, wantCalls)
				}
			})
		}
	}
}
