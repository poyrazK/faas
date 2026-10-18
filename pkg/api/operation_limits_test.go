// adr: 1196
package api

import "testing"

// Definition limits may only narrow what the plan already enforces: attempts
// stay under the drain's queue ceiling.
func TestOperationDefinitionLimitBoundsFitThePlan(t *testing.T) {
	for _, plan := range Plans {
		l := MustLimitsFor(plan)
		ops := l.Operations
		if !ops.Allowed {
			if ops.DeadlineSecondsMax != 0 || ops.AttemptsMax != 0 || ops.ExecutionSecondsMax != 0 {
				t.Fatalf("%s: limits declared for a plan without Operations", plan)
			}
			continue
		}
		if ops.DeadlineSecondsMax <= 0 || ops.AttemptsMax <= 0 || ops.ExecutionSecondsMax <= 0 {
			t.Fatalf("%s: unset definition limit bounds %+v", plan, ops)
		}
		if ops.AttemptsMax > l.MaxQueueAttempts {
			t.Fatalf("%s: AttemptsMax %d exceeds MaxQueueAttempts %d", plan, ops.AttemptsMax, l.MaxQueueAttempts)
		}
	}
}
