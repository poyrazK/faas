package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRestoreRetryAdoptsReadyCopyAfterSourceChanges(t *testing.T) {
	for _, change := range []string{"delete", "update"} {
		for _, captured := range []bool{false, true} {
			name := change
			if captured {
				name += "/captured"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				f := newSlotFixture(t)
				source, copy := f.sourceAndCopy(t)
				request := RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID,
					Name: copy.Name, PointInTime: copy.RestorePointInTime}
				if captured {
					request.SourceDefinition = &RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID,
						BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
				}
				var err error
				if change == "delete" {
					_, err = f.service.Delete(ctx, source.AccountID, source.ID)
				} else {
					// The resize journal has already moved the source to updating.
					f.store.mu.Lock()
					updating := f.store.databases[source.ID]
					updating.State = StateUpdating
					f.store.databases[source.ID] = updating
					f.store.mu.Unlock()
				}
				if err != nil {
					t.Fatal(err)
				}
				calls := f.provider.restoreCalls
				again, created, err := f.service.RestoreWithResult(ctx, request)
				if err != nil || created || again.ID != copy.ID || again.State != StateReady || f.provider.restoreCalls != calls {
					t.Fatalf("retry failed to adopt its copy: database=%+v created=%v calls=%d/%d err=%v", again, created, f.provider.restoreCalls, calls, err)
				}
				// The relaxed retry must not admit a new restore from this source.
				request.Name = "another-copy"
				if _, _, err := f.service.RestoreWithResult(ctx, request); !errors.Is(err, ErrConflict) {
					t.Fatalf("new restore from unavailable source: %v", err)
				}
				request.Name = copy.Name
				request.PointInTime = request.PointInTime.Add(time.Second)
				if _, _, err := f.service.RestoreWithResult(ctx, request); !errors.Is(err, ErrConflict) {
					t.Fatalf("adopted a copy with another restore point: %v", err)
				}
			})
		}
	}
}

// A restore the provider is still making is checked again a second later, and
// a retry before then answers with the copy in progress, not a bare conflict
// (prod 2026-11-28: 15 s to the next check and two failed attempts held an
// environment branch's data for 22 s).
func TestPendingRestoreIsCheckedSoonAndReadsAsInProgress(t *testing.T) {
	p := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusPending}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	p.provisionStatus = ProviderStatusPending
	now := source.CreatedAt.Add(10 * time.Second)
	s.now = func() time.Time { return now }
	s.pollInterval = 15 * time.Second
	request := RestoreDatabaseRequest{AccountID: "account", SourceDatabaseID: source.ID, Name: "target", PointInTime: source.CreatedAt}
	target, err := s.Restore(t.Context(), request)
	if err != nil || target.State != StateProvisioning {
		t.Fatalf("restore: %+v %v", target, err)
	}
	if wait := target.RetryAt.Sub(now); wait != restorePendingQuickPoll {
		t.Fatalf("next check of a pending restore in %s; want %s", wait, restorePendingQuickPoll)
	}
	// Waiting on the provider is not a failed attempt: quick checks must not
	// grow the backoff of a later real error.
	if target.AttemptCount != 0 || target.LastErrorCode != "" {
		t.Fatalf("pending restore attempts=%d code=%q; want 0 and no code", target.AttemptCount, target.LastErrorCode)
	}
	again, created, err := s.RestoreWithResult(t.Context(), request)
	if !errors.Is(err, ErrConflict) || created || again.ID != target.ID || again.State != StateProvisioning {
		t.Fatalf("retry before the next check = %+v created=%v err=%v; want the copy in progress with a conflict", again, created, err)
	}
}

// A restore the provider is removing is neither polled quickly nor a copy in
// progress.
func TestDeletingRestoreIsNotInProgress(t *testing.T) {
	p := &fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusDeleting}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	source, err := s.Create(t.Context(), CreateRequest{AccountID: "account", Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	p.provisionStatus = ProviderStatusDeleting
	now := source.CreatedAt.Add(10 * time.Second)
	s.now = func() time.Time { return now }
	s.pollInterval = 15 * time.Second
	request := RestoreDatabaseRequest{AccountID: "account", SourceDatabaseID: source.ID, Name: "target", PointInTime: source.CreatedAt}
	target, err := s.Restore(t.Context(), request)
	if err != nil || target.State != StateProvisioning || target.LastErrorCode != "provider_deleting" || target.RetryAt.Sub(now) != s.pollInterval {
		t.Fatalf("restore = %+v, %v; want provisioning with provider_deleting, next check on the poll interval", target, err)
	}
	again, _, err := s.RestoreWithResult(t.Context(), request)
	if !errors.Is(err, ErrConflict) || again.ID != "" {
		t.Fatalf("retry = %+v, %v; want a bare conflict", again, err)
	}
}

// A young restore the provider is still making is checked again every second,
// anything else on the poll interval.
func TestPendingPollDelay(t *testing.T) {
	now := time.Now()
	poll := 15 * time.Second
	for _, tc := range []struct {
		name     string
		database Database
		want     time.Duration
	}{
		{"young restore", Database{RestoreSourceResourceID: "src", CreatedAt: now.Add(-5 * time.Second)}, time.Second},
		{"restore past 10 s", Database{RestoreSourceResourceID: "src", CreatedAt: now.Add(-15 * time.Second)}, 2 * time.Second},
		{"old restore", Database{RestoreSourceResourceID: "src", CreatedAt: now.Add(-time.Minute)}, poll},
		{"new database", Database{CreatedAt: now.Add(-5 * time.Second)}, poll},
	} {
		if got := pendingPollDelay(tc.database, now, poll); got != tc.want {
			t.Errorf("%s: delay = %s; want %s", tc.name, got, tc.want)
		}
	}
	if got := pendingPollDelay(Database{RestoreSourceResourceID: "src", CreatedAt: now}, now, 500*time.Millisecond); got != 500*time.Millisecond {
		t.Errorf("a shorter poll interval = %s; want it kept", got)
	}
}

// A retry while another worker holds the copy's lease answers with the copy,
// still provisioning, and the conflict, so an environment branch counts it as
// in progress rather than a failed attempt (prod 2026-11-27). Any other
// conflict (a copy backing off after a failure, or being deleted) keeps the
// empty row, so it is never waited on as in progress.
func TestRestoreRetryConflictNamesOnlyACopyInProgress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		set        func(*Database)
		inProgress bool
	}{
		{"leased", func(d *Database) {
			d.State, d.LeaseToken, d.LeaseUntil = StateProvisioning, "other-worker", time.Now().Add(time.Hour)
		}, true},
		{"backing off", func(d *Database) {
			d.State, d.RetryAt, d.LastErrorCode = StateProvisioning, time.Now().Add(time.Hour), "unavailable"
		}, false},
		// The provider accepted the restore; it waits for its next check
		// (prod 2026-11-28: counted as failed attempts, 5 s + 10 s backoff).
		{"provider still restoring", func(d *Database) {
			d.State, d.RetryAt, d.LastErrorCode, d.ProviderResourceID = StateProvisioning, time.Now().Add(time.Hour), "", "provider-copy"
		}, true},
		{"not yet accepted by the provider", func(d *Database) {
			d.State, d.RetryAt, d.LastErrorCode, d.ProviderResourceID = StateProvisioning, time.Now().Add(time.Hour), "", ""
		}, false},
		{"leased while backing off", func(d *Database) {
			d.State, d.LeaseToken, d.LeaseUntil, d.RetryAt, d.LastErrorCode = StateProvisioning, "other-worker", time.Now().Add(time.Hour), time.Now().Add(time.Hour), "unavailable"
		}, false},
		{"deleting", func(d *Database) { d.State = StateDeleting }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newSlotFixture(t)
			source, copy := f.sourceAndCopy(t)
			f.store.mu.Lock()
			row := f.store.databases[copy.ID]
			tc.set(&row)
			f.store.databases[copy.ID] = row
			f.store.mu.Unlock()
			calls := f.provider.restoreCalls
			again, created, err := f.service.RestoreWithResult(ctx, RestoreDatabaseRequest{AccountID: source.AccountID, SourceDatabaseID: source.ID,
				Name: copy.Name, PointInTime: copy.RestorePointInTime})
			if !errors.Is(err, ErrConflict) || created || f.provider.restoreCalls != calls {
				t.Fatalf("retry = %+v created=%v calls=%d/%d err=%v; want a conflict", again, created, f.provider.restoreCalls, calls, err)
			}
			if tc.inProgress && (again.ID != copy.ID || again.State != StateProvisioning) {
				t.Fatalf("leased retry = %+v; want the provisioning copy", again)
			}
			if !tc.inProgress && again.ID != "" {
				t.Fatalf("%s retry named a copy in progress: %+v", tc.name, again)
			}
		})
	}
}
