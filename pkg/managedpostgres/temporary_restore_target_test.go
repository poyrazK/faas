package managedpostgres

import (
	"errors"
	"testing"
	"time"
)

func TestRestoreWithinLimitRejectsTemporaryAdoption(t *testing.T) {
	f := newSlotFixture(t)
	source, copy := f.sourceAndCopy(t)
	request := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID, Name: copy.Name, PointInTime: copy.RestorePointInTime, WithinLimit: true}
	if _, created, err := f.service.RestoreWithResult(t.Context(), request); !errors.Is(err, ErrQuotaExceeded) || created {
		t.Fatalf("temporary copy adopted by rollback: created=%v err=%v", created, err)
	}
	// Ordinary restore retries retain their original temporary copy.
	request.WithinLimit = false
	if again, created, err := f.service.RestoreWithResult(t.Context(), request); err != nil || created || again.ID != copy.ID {
		t.Fatalf("ordinary restore retry changed: database=%+v created=%v err=%v", again, created, err)
	}
	// A reservation racing the initial name lookup must enforce the same rule.
	input := Database{ID: "another-id", AccountID: source.AccountID, Name: copy.Name, State: StateProvisioning}
	if _, _, err := f.store.Reserve(reserveWithinLimit(t.Context()), input, 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("reservation adopted a temporary copy", err)
	}
	*f.limit = 2
	if _, err := f.sweeper.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	request.WithinLimit = true
	if permanent, _, err := f.service.RestoreWithResult(t.Context(), request); err != nil || permanent.ID != copy.ID || !permanent.RestoreSlotUntil.IsZero() {
		t.Fatalf("copy did not become adoptable after room was freed: database=%+v err=%v", permanent, err)
	}
}

func TestCutoverRejectsTemporaryTargetBeforeIssuingCredentials(t *testing.T) {
	svc, store, provider, _, _, now, _, request := cutoverFixture(t)
	store.mu.Lock()
	target := store.databases[request.TargetDatabaseID]
	target.RestoreSlotUntil = now.Add(time.Hour)
	store.databases[target.ID] = target
	store.mu.Unlock()
	if _, err := svc.Prepare(t.Context(), request); !errors.Is(err, ErrConflict) || provider.issueCalls != 0 {
		t.Fatalf("temporary target staged: calls=%d err=%v", provider.issueCalls, err)
	}
	if _, err := store.GetCutover(t.Context(), request.AccountID, request.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("refused cutover left an intent", err)
	}
}
