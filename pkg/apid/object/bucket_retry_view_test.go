package object

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBucketRetryShowsOnlyRecordedFailures(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state, code string
		want              bool
	}{
		{"ready bucket", "ready", "temporary", false},
		{"fresh claim", "provisioning", "", false},
		{"failed create", "provisioning", "temporary", true},
		{"protected delete", "deleting", "protected", true},
		{"deleted bucket", "deleted", "configuration", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := viewBucket(state.ObjectBucket{State: tc.state, LastErrorCode: tc.code, AttemptCount: 3, RetryAt: at}).Retry
			if (got != nil) != tc.want {
				t.Fatalf("retry = %+v, want shown=%v", got, tc.want)
			}
			if got != nil && (got.Reason != tc.code || got.Attempts != 3 || !got.RetryAt.Equal(at) || got.Detail != api.ObjectBucketRetryDetail(tc.code)) {
				t.Fatalf("retry = %+v", *got)
			}
		})
	}
	for _, code := range []string{api.ObjectBucketRetryTemporary, api.ObjectBucketRetryConflict, api.ObjectBucketRetryConfiguration, api.ObjectBucketRetryInvalid, api.ObjectBucketRetryProtected, api.ObjectBucketRetryCleanupPending} {
		if api.ObjectBucketRetryDetail(code) == api.ObjectBucketRetryDetail("unknown") {
			t.Fatalf("reason %q has no sentence of its own", code)
		}
	}
}
