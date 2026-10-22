// adr: 1259
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// verifyingRestoreProvider resolves a restore's lineage like Neon: the first
// inspections cannot prove it, a later one verifies it, and every inspection
// after that must receive the recorded verification instead of remapping.
type verifyingRestoreProvider struct {
	fakeProvider
	unproven      int
	foreign       bool
	inspections   int
	confirmations int
}

func (p *verifyingRestoreProvider) InspectRestore(ctx context.Context, id string, request RestoreRequest) (ObservedDatabase, error) {
	p.inspections++
	observed, err := p.fakeProvider.Inspect(ctx, id)
	if err != nil {
		return ObservedDatabase{}, err
	}
	if request.Verification != nil {
		if request.Verification.Evidence != "branch|parent|0/2" || request.Verification.Validate(id, request) != nil {
			return ObservedDatabase{}, ErrConflict
		}
		p.confirmations++
		verification := *request.Verification
		observed.RestoreVerification = &verification
		return observed, nil
	}
	if p.unproven > 0 {
		p.unproven--
		return ObservedDatabase{}, Reasonf(ErrLineageUnproven, "restore branch parent LSN 0/2 does not match the requested point's LSN 0/3")
	}
	point := request.PointInTime
	if p.foreign {
		point = point.Add(-time.Second)
	}
	observed.RestoreVerification = &RestoreVerification{ProviderResourceID: id, SourceResourceID: request.SourceResourceID, PointInTime: point, Evidence: "branch|parent|0/2"}
	return observed, nil
}

func TestRestoreVerificationIsRecordedOnceThenConfirmed(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "foreign_record"}[foreign], func(t *testing.T) {
			p := &verifyingRestoreProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusPending}, unproven: 1, foreign: foreign}
			store := NewMemoryStore()
			s := testService(t, testRegistry(t, p, nil), store)
			source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
			if err != nil {
				t.Fatal(err)
			}
			p.provisionStatus = ProviderStatusPending
			target, err := s.Restore(t.Context(), RestoreDatabaseRequest{AccountID: "account", SourceDatabaseID: source.ID, Name: "target", PointInTime: source.CreatedAt})
			if err != nil || target.State != StateProvisioning || target.ProviderResourceID == "" {
				t.Fatalf("restore: %+v %v", target, err)
			}
			reconcile := func() (Database, error) {
				t.Helper()
				held, _ := store.Get(t.Context(), target.AccountID, target.ID)
				now := held.RetryAt.Add(time.Second)
				s.now = func() time.Time { return now }
				return s.Reconcile(t.Context(), target.AccountID, target.ID)
			}

			// A mismatch before any record is "not proven yet": its own code
			// and a poll-interval retry, not a conflict with backoff.
			_, err = reconcile()
			held, _ := store.Get(t.Context(), target.AccountID, target.ID)
			if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrConflict) || held.LastErrorCode != "restore_lineage_unavailable" || !held.RetryAt.Equal(s.now().Add(time.Second)) {
				t.Fatalf("unproven lineage: %v code=%q retry=%s now=%s", err, held.LastErrorCode, held.RetryAt, s.now())
			}
			if LoggableReason(err) == "" {
				t.Fatal("unproven lineage lost its reason")
			}

			_, err = reconcile()
			recorded, getErr := store.GetRestoreVerification(t.Context(), target.AccountID, target.ID)
			if foreign {
				if !errors.Is(err, ErrConflict) || !errors.Is(getErr, ErrNotFound) {
					t.Fatalf("foreign verification: %v, record %+v %v", err, recorded, getErr)
				}
				return
			}
			if err != nil || getErr != nil || recorded.Evidence != "branch|parent|0/2" || recorded.ProviderResourceID != target.ProviderResourceID {
				t.Fatalf("verification not recorded: %v, %+v %v", err, recorded, getErr)
			}

			p.inspectStatus = ProviderStatusReady
			result, err := reconcile()
			if err != nil || result.State != StateReady || p.confirmations != 1 || p.inspections != 3 {
				t.Fatalf("confirmed restore: %+v %v confirmations=%d inspections=%d", result, err, p.confirmations, p.inspections)
			}
		})
	}
}

func TestMemoryRestoreVerificationIsAppendOnceUnderTheLease(t *testing.T) {
	store := NewMemoryStore()
	point := time.Date(2026, 10, 14, 12, 15, 0, 0, time.UTC)
	database := Database{ID: "target", AccountID: "account", BackendID: "primary", State: StateProvisioning, LeaseToken: "worker",
		DesiredGeneration: 1, ProviderResourceID: "project/br-target", RestoreSourceResourceID: "project/br-source", RestorePointInTime: point}
	store.databases[database.ID] = database
	v := RestoreVerification{ProviderResourceID: "project/br-target", SourceResourceID: "project/br-source", PointInTime: point, Evidence: "neon-branch-v1|project|br-target"}
	if err := store.RecordRestoreVerification(t.Context(), database, v); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRestoreVerification(t.Context(), database, v); err != nil {
		t.Fatalf("replay: %v", err)
	}
	for _, fault := range []string{"evidence", "point", "target", "lease", "state"} {
		t.Run(fault, func(t *testing.T) {
			changed, holder := v, database
			switch fault {
			case "evidence":
				changed.Evidence = "neon-branch-v1|project|br-other"
			case "point":
				changed.PointInTime = point.Add(time.Second)
			case "target":
				changed.ProviderResourceID = "project/br-other"
			case "lease":
				holder.LeaseToken = "stale-worker"
			case "state":
				store.mu.Lock()
				ready := store.databases[database.ID]
				ready.State = StateReady
				store.databases[database.ID] = ready
				store.mu.Unlock()
				defer func() {
					store.mu.Lock()
					store.databases[database.ID] = database
					store.mu.Unlock()
				}()
			}
			if err := store.RecordRestoreVerification(t.Context(), holder, changed); err == nil {
				t.Fatal("changed verification accepted")
			}
			got, err := store.GetRestoreVerification(t.Context(), database.AccountID, database.ID)
			if err != nil || !sameRestoreVerification(got, v) {
				t.Fatalf("verification replaced: %+v %v", got, err)
			}
		})
	}
	if _, err := store.GetRestoreVerification(t.Context(), "other-account", database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
}

func TestLineageRetryPollsBeforeBackingOff(t *testing.T) {
	for _, tc := range []struct {
		attempt int32
		want    time.Duration
	}{{1, 15 * time.Second}, {20, 15 * time.Second}, {21, 30 * time.Second}, {22, time.Minute}, {60, 15 * time.Minute}} {
		if got := lineageRetryDelay(15*time.Second, tc.attempt); got != tc.want {
			t.Errorf("attempt %d: delay %s, want %s", tc.attempt, got, tc.want)
		}
	}
}
