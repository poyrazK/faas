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
			d.State, d.RetryAt = StateProvisioning, time.Now().Add(time.Hour)
		}, false},
		{"leased while backing off", func(d *Database) {
			d.State, d.LeaseToken, d.LeaseUntil, d.RetryAt = StateProvisioning, "other-worker", time.Now().Add(time.Hour), time.Now().Add(time.Hour)
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
