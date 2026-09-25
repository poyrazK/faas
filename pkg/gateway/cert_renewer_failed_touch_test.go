// spec: §4.1

package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// failingTouchStore serves a keyset-paged expiry listing and fails every
// renewal touch.
type failingTouchStore struct {
	state.Store
	surfaces []state.TenantSurface
	lists    int
}

func (s *failingTouchStore) ListTenantSurfacesNearingExpiry(_ context.Context, _ time.Time, limit int, afterNotAfter time.Time, afterID string) ([]state.TenantSurface, error) {
	s.lists++
	if s.lists > len(s.surfaces)/limit+3 {
		return nil, errors.New("renewer re-listed a page it had already seen")
	}
	var page []state.TenantSurface
	for _, surface := range s.surfaces {
		if afterID != "" && (surface.CertNotAfter.Before(afterNotAfter) ||
			(surface.CertNotAfter.Equal(afterNotAfter) && surface.ID <= afterID)) {
			continue
		}
		page = append(page, surface)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}

func (s *failingTouchStore) TouchTenantSurfaceForRenewal(context.Context, string) error {
	return errors.New("write outage")
}

// TestSurfaceCertRenewer_TickOnce_FailedTouchesDoNotLoop — the keyset cursor
// advanced only on a successful touch, so a full page of failed touches
// re-listed the same page forever. Failed rows must be skipped for this tick
// and the walk must still reach the end of the result set.
func TestSurfaceCertRenewer_TickOnce_FailedTouchesDoNotLoop(t *testing.T) {
	notAfter := time.Now().Add(24 * time.Hour).UTC()
	store := &failingTouchStore{}
	for i := 0; i < api.CertRenewTickBatchLimit+5; i++ {
		store.surfaces = append(store.surfaces, state.TenantSurface{
			ID: fmt.Sprintf("surface-%05d", i), AccountID: "acct", CertNotAfter: notAfter,
		})
	}
	r := NewSurfaceCertRenewer(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.SetRenewBefore(30 * 24 * time.Hour)

	if err := r.tickOnce(context.Background()); err != nil {
		t.Fatalf("tickOnce: %v (lists=%d)", err, store.lists)
	}
	if store.lists != 2 {
		t.Fatalf("lists = %d, want 2 (one full page, one short page)", store.lists)
	}
}
