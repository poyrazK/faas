package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// A branch's database copy ends with the branch. A branch of at most 7 days
// keeps its copy inside the PostgreSQL branch allowance (ADR-20261106103654
// amendment 1), even when the branch point sits a moment before the branch's
// creation; a longer branch makes an ordinary copy that still ends on its own.
func TestEnvironmentBranchDatabaseTTL(t *testing.T) {
	created := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	allowance := api.ManagedPostgresAllowanceBranchTTLMax
	tests := []struct {
		name          string
		ttl           time.Duration
		pointBefore   time.Duration
		want          time.Duration
		wantAllowance bool
	}{
		{name: "two hours", ttl: 2 * time.Hour, want: 2 * time.Hour, wantAllowance: true},
		{name: "default seven days", ttl: 7 * 24 * time.Hour, want: allowance, wantAllowance: true},
		{name: "seven days, point before creation", ttl: 7 * 24 * time.Hour, pointBefore: time.Second, want: allowance, wantAllowance: true},
		{name: "one hour, point before creation", ttl: time.Hour, pointBefore: time.Second, want: time.Hour + time.Second, wantAllowance: true},
		{name: "eight days", ttl: 8 * 24 * time.Hour, want: 8 * 24 * time.Hour},
		{name: "thirty days, point before creation", ttl: 30 * 24 * time.Hour, pointBefore: time.Second, want: managedpostgres.MaxBranchTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch := state.ProjectEnvironmentBranch{CreatedAt: created, ExpiresAt: created.Add(tt.ttl)}
			got := apidserver.EnvironmentBranchDatabaseTTL(branch, created.Add(-tt.pointBefore))
			if got != tt.want {
				t.Fatalf("ttl = %s, want %s", got, tt.want)
			}
			if inAllowance := got <= allowance; inAllowance != tt.wantAllowance {
				t.Fatalf("in branch allowance = %v, want %v", inAllowance, tt.wantAllowance)
			}
			if got < managedpostgres.MinBranchTTL || got > managedpostgres.MaxBranchTTL {
				t.Fatalf("ttl %s outside the restore TTL range", got)
			}
		})
	}
}
