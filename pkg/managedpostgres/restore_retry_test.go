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
