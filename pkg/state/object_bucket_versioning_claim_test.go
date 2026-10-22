package state_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestVersioningBlockedClaimClearsExpiredLease(t *testing.T) {
	now := time.Now()
	j := state.ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{State: "waiting", DesiredStatus: "Enabled"}, Token: "expired", LeaseUntil: now.Add(-time.Minute)}
	j, err := state.ClaimObjectVersioning(j, "new", 1, false, false, false, now)
	if err != nil || j.Token != "" || !j.LeaseUntil.IsZero() || j.LastErrorCode != "unsettled_writes" {
		t.Fatal(j, err)
	}
}
