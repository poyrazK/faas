package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type historicalSpecProvider struct {
	fakeProvider
	resolve     func(Spec) (Spec, error)
	resolutions int
}

func (p *historicalSpecProvider) ResolveRestoreSpec(_ context.Context, source RestoreSourceDefinition, _ time.Time) (Spec, error) {
	p.resolutions++
	return p.resolve(source.Spec)
}

func TestHistoricalRestoreSpecIsFrozenBeforeReservation(t *testing.T) {
	p := &historicalSpecProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}, resolve: func(s Spec) (Spec, error) { s.PostgresMajor = 16; return s, nil }}
	reg := testRegistry(t, p, nil)
	store := NewMemoryStore()
	svc := testService(t, reg, store)
	source, err := svc.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	r := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "historical", PointInTime: source.CreatedAt}
	target, err := svc.Restore(t.Context(), r)
	if err != nil || target.Spec.PostgresMajor != 16 || p.lastRestore.Spec.PostgresMajor != 16 {
		t.Fatal(target, err)
	}
	p.resolve = func(Spec) (Spec, error) { return Spec{}, ErrUnavailable }
	again, err := svc.Restore(t.Context(), r)
	if err != nil || again.ID != target.ID || p.resolutions != 1 {
		t.Fatal("replay read a new specification", again, err, p.resolutions)
	}
}

func TestHistoricalRestoreResolutionRejectsBeforeReservation(t *testing.T) {
	for _, fault := range []string{"unavailable", "storage", "major_increase", "invalid_major", "frozen_major"} {
		t.Run(fault, func(t *testing.T) {
			p := &historicalSpecProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady}}
			p.resolve = func(s Spec) (Spec, error) {
				switch fault {
				case "unavailable":
					return Spec{}, ErrUnavailable
				case "storage":
					s.StorageLimitBytes *= 2
				case "major_increase":
					s.PostgresMajor++
				case "invalid_major":
					s.PostgresMajor = 0
				case "frozen_major":
					s.PostgresMajor = 16
				}
				return s, nil
			}
			store := NewMemoryStore()
			svc := testService(t, testRegistry(t, p, nil), store)
			source, err := svc.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			r := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: "invalid", PointInTime: source.CreatedAt}
			if fault == "frozen_major" {
				r.SourceDefinition = &RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID}
			}
			if _, err := svc.Restore(t.Context(), r); err == nil {
				t.Fatal("invalid restore plan accepted")
			}
			if _, err := store.FindByName(t.Context(), source.AccountID, r.Name); !errors.Is(err, ErrNotFound) {
				t.Fatal("invalid plan reserved quota", err)
			}
			if p.restoreCalls != 0 {
				t.Fatal("invalid plan dispatched a restore")
			}
		})
	}
}
