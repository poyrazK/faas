package sched_test

// adr: 20261029012000

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestRetentionRepeatsFullBatches(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name      string
		results   []int
		err       error
		wantTotal int
		wantCalls int
	}{
		{"a short first batch stops", []int{3}, nil, 3, 1},
		{"full batches repeat until a short one", []int{10, 10, 4}, nil, 24, 3},
		{"an error stops", []int{10, 10}, boom, 20, 2},
		{"the pass is bounded", nil, nil, 10 * api.InvocationRetentionBatchesPerPass, api.InvocationRetentionBatchesPerPass},
	}
	for _, tc := range cases {
		calls := 0
		total, err := sched.XRepeatFullBatches(10, func() (int, error) {
			calls++
			if tc.results == nil {
				return 10, nil
			}
			n := tc.results[calls-1]
			if calls == len(tc.results) {
				return n, tc.err
			}
			return n, nil
		})
		if total != tc.wantTotal || calls != tc.wantCalls || !errors.Is(err, tc.err) {
			t.Errorf("%s: total=%d calls=%d err=%v; want %d, %d, %v", tc.name, total, calls, err, tc.wantTotal, tc.wantCalls, tc.err)
		}
	}
}
