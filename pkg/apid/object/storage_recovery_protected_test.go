package object

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// TestObjectRetryPolicyWaitsForKeptRetention pins ADR-20261111123000
// decision 8: a cleanup that kept COMPLIANCE versions retries when the
// earliest one ends, not hourly forever.
func TestObjectRetryPolicyWaitsForKeptRetention(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		err  error
		min  time.Duration
		max  time.Duration
	}{
		{"legacy protected", objectstorage.ErrObjectProtected, time.Hour, time.Hour},
		{"held", &objectstorage.ProtectedUntilError{Kept: 1, Undated: true}, api.ObjectOwnedCleanupProtectedRetryMin, api.ObjectOwnedCleanupProtectedRetryMin},
		{"two days", &objectstorage.ProtectedUntilError{Kept: 1, Until: now.Add(48 * time.Hour)}, 47 * time.Hour, 49 * time.Hour},
		{"seven years", fmt.Errorf("cleanup: %w", &objectstorage.ProtectedUntilError{Kept: 1, Until: now.AddDate(7, 0, 0)}), api.ObjectOwnedCleanupProtectedRetryMax, api.ObjectOwnedCleanupProtectedRetryMax},
		{"minutes", &objectstorage.ProtectedUntilError{Kept: 1, Until: now.Add(time.Minute)}, api.ObjectOwnedCleanupProtectedRetryMin, api.ObjectOwnedCleanupProtectedRetryMin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, delay := ObjectRetryPolicy(tc.err, 1)
			if code != "protected" || delay < tc.min || delay > tc.max {
				t.Fatalf("policy = %s %v, want protected in [%v, %v]", code, delay, tc.min, tc.max)
			}
		})
	}
}
