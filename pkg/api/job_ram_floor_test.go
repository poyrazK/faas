package api

import (
	"net/http"
	"testing"
)

// Hunt 15: below JobMinRAMMB a job or image run cannot boot its guest.
func TestValidateJobRAMMB(t *testing.T) {
	for _, ramMB := range []int{JobMinRAMMB, JobDefaultRAMMB, JobRAMMB[len(JobRAMMB)-1]} {
		if prob := ValidateJobRAMMB(ramMB); prob != nil {
			t.Fatalf("ValidateJobRAMMB(%d) = %+v; want accepted", ramMB, prob)
		}
	}
	for _, ramMB := range []int{-1, 0, 1, JobMinRAMMB - 1} {
		prob := ValidateJobRAMMB(ramMB)
		if prob == nil || prob.Status != http.StatusBadRequest || prob.Limit == nil || *prob.Limit != JobMinRAMMB ||
			prob.Observed == nil || *prob.Observed != int64(ramMB) || prob.DocsURL == "" {
			t.Fatalf("ValidateJobRAMMB(%d) = %+v; want 400 with limit %d, the observed value and docs", ramMB, prob, JobMinRAMMB)
		}
	}
}
