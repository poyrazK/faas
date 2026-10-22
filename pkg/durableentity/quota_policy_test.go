package durableentity

import (
	"errors"
	"testing"
)

func TestQuotaPolicyEntityCapRaiseFencesOwnersAndNeverLowers(t *testing.T) {
	f := newFixture(t)
	m, err := Open(t.Context(), f.store, Options{RetainedBytesLimit: 2048, Now: f.manager.now})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RaiseQuotaEntityLimit(t.Context(), f.id, 2048); !errors.Is(err, ErrBusy) {
		t.Fatal("live owner bypassed", err)
	}
	if err = f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	if err = m.RaiseQuotaEntityLimit(t.Context(), f.id, 2048); err != nil {
		t.Fatal(err)
	}
	base, _, err := m.readManifest(t.Context(), f.id)
	if err != nil || base.StorageLimitBytes != 2048 || base.OwnerID != "" || base.Epoch <= f.claim.Epoch {
		t.Fatal(base, err)
	}
	if err = f.manager.SetStorageLimit(t.Context(), f.claim, 4096); !errors.Is(err, ErrStaleOwner) {
		t.Fatal("old owner changed cap", err)
	}
	if err = m.RaiseQuotaEntityLimit(t.Context(), f.id, 2048); err != nil {
		t.Fatal("retry not idempotent", err)
	}
	older, err := Open(t.Context(), f.store, Options{RetainedBytesLimit: 1024, Now: f.manager.now})
	if err != nil {
		t.Fatal(err)
	}
	if err = older.RaiseQuotaEntityLimit(t.Context(), f.id, 1024); !errors.Is(err, ErrConflict) {
		t.Fatal("stale policy lowered cap", err)
	}
	if err = m.RaiseQuotaEntityLimit(t.Context(), f.id, 4096); !errors.Is(err, ErrUnsupported) {
		t.Fatal("node cap mismatch allowed", err)
	}
	if err = m.RaiseQuotaEntityLimit(t.Context(), ID{AccountID: "a", AppID: "b", Namespace: "n", Key: "missing"}, 2048); !errors.Is(err, ErrNotFound) {
		t.Fatal("created missing entity", err)
	}
}
