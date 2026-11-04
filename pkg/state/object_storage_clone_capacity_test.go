// adr: 20261103160000

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectCloneCapacityCountsSourceBucketsAgainstAccountLimits(t *testing.T) {
	now := time.Now().UTC()
	s := twoBucketSnapshot(now, 30, 40, now.Add(-time.Minute))
	bytes, keys, known := state.ObjectCloneSourceCapacity(s, []string{"b"})
	if !known || bytes != 40 || keys != 1 {
		t.Fatalf("source capacity = %d bytes, %d keys, known %v", bytes, keys, known)
	}
	if _, _, known := state.ObjectCloneSourceCapacity(s, []string{"b", "missing"}); known {
		t.Fatal("an unknown source bucket must not be sized as empty")
	}
	limit := planCeilingPolicy().MaxAccountBytes
	for _, tc := range []struct {
		name  string
		bytes int64
		want  error
	}{
		{name: "copy fits", bytes: limit - 70},
		{name: "copy passes the account ceiling", bytes: limit - 69, want: state.ErrObjectCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := state.CheckObjectCloneCapacity(s, planCeilingPolicy(), now, tc.bytes, 1)
			if !errors.Is(err, tc.want) && (tc.want != nil || err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			var le *state.ObjectStorageLimitError
			if tc.want != nil && (!errors.As(err, &le) || le.Kind != "account_bytes" || le.Limit != limit || le.Observed != limit+1) {
				t.Fatalf("limit error = %+v", le)
			}
		})
	}
	if err := state.CheckObjectCloneCapacity(s, planCeilingPolicy(), now, -1, 0); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("negative size = %v", err)
	}
}
