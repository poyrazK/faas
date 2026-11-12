package object

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A refused lifecycle scan says why instead of the generic busy-bucket
// conflict (11-04 prod test).
func TestLifecycleScanRefusalNamesTheReason(t *testing.T) {
	next := time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		rules []api.ObjectLifecycleRule
		code  string
	}{
		{"no rules", nil, "lifecycle_no_enabled_rules"},
		{"only disabled rules", []api.ObjectLifecycleRule{{ID: "off", Status: "Disabled"}}, "lifecycle_no_enabled_rules"},
		{"scanned recently", []api.ObjectLifecycleRule{{ID: "on", Status: "Enabled"}}, "lifecycle_scan_not_due"},
	} {
		p := state.ObjectLifecyclePolicy{ObjectBucketLifecycle: api.ObjectBucketLifecycle{Rules: tc.rules}, NextScanAt: next}
		if got := lifecycleScanRefusal(p); got.Code != tc.code || got.Status != 409 {
			t.Errorf("%s: problem = %+v", tc.name, got)
		}
	}
}
