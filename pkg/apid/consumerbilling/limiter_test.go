package consumerbilling

import (
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A full limiter map drops idle limiters, not every limiter: a flood of
// fresh key prefixes must not reset the limit of a key in use (bug hunt H15).
func TestLimiterMapKeepsBusyKeysWhenFull(t *testing.T) {
	s := &Service{}
	for range api.ConsumerBillingBurst {
		if !s.allow("busy") {
			t.Fatal("busy key refused inside its burst")
		}
	}
	if s.allow("busy") {
		t.Fatal("busy key allowed past its burst")
	}
	for i := range limiterCap + 10 {
		s.allow("fresh-" + strconv.Itoa(i))
	}
	if len(s.limiters) > limiterCap {
		t.Fatalf("limiters = %d, want at most %d", len(s.limiters), limiterCap)
	}
	if s.allow("busy") {
		t.Fatal("a flood of new prefixes reset the busy key's limit")
	}
}
