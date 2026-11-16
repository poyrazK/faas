// adr: 20261031193635 — extending an environment branch moves its database
// copies' end, never out of the branch allowance a copy sits in.
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMoveBranchExpiryKeepsACopyInItsAllowance(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	*f.limit, *f.branchAllowance = 2, 2
	source := f.source(t)
	branch, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-1", Latest: true, TTL: 2 * time.Hour})
	if err != nil || !branch.InBranchAllowance() {
		t.Fatalf("Restore = %+v, %v; want a branch in the allowance", branch, err)
	}
	later := branch.CreatedAt.Add(48 * time.Hour)
	moved, err := f.service.MoveBranchExpiry(ctx, "account-a", branch.ID, later)
	if err != nil || !moved.ExpiresAt.Equal(later) || !moved.InBranchAllowance() {
		t.Fatalf("MoveBranchExpiry = %+v, %v; want it to end at %s, still in the allowance", moved, err, later)
	}
	// Past 7 days from its creation the copy would leave the allowance.
	if _, err := f.service.MoveBranchExpiry(ctx, "account-a", branch.ID, branch.CreatedAt.Add(api.ManagedPostgresAllowanceBranchTTLMax+time.Minute)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("past the allowance = %v; want ErrQuotaExceeded", err)
	}
	if _, err := f.service.MoveBranchExpiry(ctx, "account-a", branch.ID, f.clock.Add(-time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an end in the past = %v; want ErrInvalid", err)
	}
	// A database that does not end on its own is not a branch.
	if _, err := f.service.MoveBranchExpiry(ctx, "account-a", source.ID, later); !errors.Is(err, ErrConflict) {
		t.Fatalf("the source = %v; want ErrConflict", err)
	}
	if _, err := f.service.MoveBranchExpiry(ctx, "account-b", branch.ID, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another account = %v; want ErrNotFound", err)
	}
	// A longer branch moves up to its 30-day cap.
	long, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-long", Latest: true,
		TTL: api.ManagedPostgresAllowanceBranchTTLMax + time.Hour})
	if err != nil || long.InBranchAllowance() {
		t.Fatalf("Restore long = %+v, %v", long, err)
	}
	if moved, err := f.service.MoveBranchExpiry(ctx, "account-a", long.ID, long.CreatedAt.Add(MaxBranchTTL)); err != nil || !moved.ExpiresAt.Equal(long.CreatedAt.Add(MaxBranchTTL)) {
		t.Fatalf("long to its cap = %+v, %v", moved, err)
	}
	if _, err := f.service.MoveBranchExpiry(ctx, "account-a", long.ID, long.CreatedAt.Add(MaxBranchTTL+time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long past its cap = %v; want ErrInvalid", err)
	}
}
