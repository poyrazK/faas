package orgpurge_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgpurge"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeStore struct {
	before time.Time
	limit  int
}

func (f *fakeStore) PurgeDeletedOrgs(_ context.Context, before time.Time, limit int) ([]state.PurgedOrg, error) {
	f.before, f.limit = before, limit
	return []state.PurgedOrg{{ID: "o1", Slug: "acme"}}, nil
}

type audits struct{ kinds []string }

func (a *audits) Emit(_ context.Context, kind string, _ *string, _ map[string]any) {
	a.kinds = append(a.kinds, kind)
}

func TestSweepPurgesOrgsDeletedThirtyDaysAgo(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	store, audit := &fakeStore{}, &audits{}
	n := orgpurge.Sweep(context.Background(), store, audit, slog.New(slog.NewTextHandler(io.Discard, nil)), now)
	if n != 1 || len(audit.kinds) != 1 || audit.kinds[0] != "org.purged" {
		t.Fatalf("purged %d, audits %v", n, audit.kinds)
	}
	if !store.before.Equal(now.Add(-api.DeletedOrgPurgeAfter)) || store.limit != api.DeletedOrgPurgeBatch || api.DeletedOrgPurgeAfter != 30*24*time.Hour {
		t.Fatalf("cutoff %v limit %d", store.before, store.limit)
	}
}
