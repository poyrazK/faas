//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointAdoptionTestDriver struct {
	*commonCheckpointTestDriver
	adoption state.ProjectEnvironmentCloneCheckpointAdoption
	edit     func(*state.ProjectEnvironmentCloneCheckpointAdoption)
}

func (d *checkpointAdoptionTestDriver) Capture(ctx context.Context, plan cloneCommonCheckpointPlan) error {
	// Capture's original roster is isolated from the deliberately mutating base.
	sources := append([]cloneCommonCheckpointSource(nil), plan.Sources...)
	err := d.commonCheckpointTestDriver.Capture(ctx, plan)
	d.adoption = state.ProjectEnvironmentCloneCheckpointAdoption{}
	for i, source := range sources {
		if source.Kind == "managed_postgres" {
			d.adoption.Snapshots = append(d.adoption.Snapshots, state.ProjectEnvironmentCloneCheckpointSnapshot{SourceDatabaseID: source.ID, RequestStartedAt: d.receipt.CapturedAt,
				Observation: state.ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: d.receipt.Sources[i].CaptureIdentity,
					SourceDataResourceID: source.DataResourceID, CapturePoint: d.receipt.CapturedAt, CreatedAt: d.receipt.CapturedAt}})
		} else {
			versions := []state.ProjectEnvironmentCloneObjectVersion{{Key: "original.txt", VersionID: "retained-version-" + source.ID, Size: 12,
				LastModified: d.receipt.CapturedAt.Add(-time.Second), ValidUntil: d.receipt.CapturedAt.Add(24 * time.Hour)}}
			hash, hashErr := state.ProjectEnvironmentCloneObjectManifestHash(versions)
			if hashErr != nil {
				d.t.Fatal(hashErr)
			}
			d.receipt.Sources[i].CaptureIdentity = hash
			d.adoption.Manifests = append(d.adoption.Manifests, state.ProjectEnvironmentCloneCheckpointManifest{SourceBucketID: source.ID,
				Hash: hash, CapturedAt: d.receipt.CapturedAt, Objects: versions, RetainedUntil: d.receipt.CapturedAt.Add(24 * time.Hour)})
		}
	}
	d.adoption.Receipt = d.receipt
	return err
}

func (d *checkpointAdoptionTestDriver) ObserveCapturedDatasets(_ context.Context, _ cloneCommonCheckpointPlan, _ cloneCommonCheckpointReceipt, _ time.Time) (state.ProjectEnvironmentCloneCheckpointAdoption, error) {
	out := d.adoption
	out.Receipt.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), out.Receipt.Sources...)
	out.Snapshots = append([]state.ProjectEnvironmentCloneCheckpointSnapshot(nil), out.Snapshots...)
	out.Manifests = append([]state.ProjectEnvironmentCloneCheckpointManifest(nil), out.Manifests...)
	for i := range out.Manifests {
		out.Manifests[i].Objects = append([]state.ProjectEnvironmentCloneObjectVersion(nil), out.Manifests[i].Objects...)
	}
	if d.edit != nil {
		d.edit(&out)
	}
	return out, nil
}

type checkpointAdoptionLostReplyStore struct {
	*cloneCaptureBarrierFailureStore
	lose bool
}

func (s *checkpointAdoptionLostReplyStore) AdoptProjectEnvironmentCloneCheckpoint(ctx context.Context, lease state.ProjectEnvironmentCloneLease, in state.ProjectEnvironmentCloneCheckpointAdoption, limit int) (state.ProjectEnvironmentCloneOperation, error) {
	op, err := s.PgStore.AdoptProjectEnvironmentCloneCheckpoint(ctx, lease, in, limit)
	if err == nil && s.lose {
		s.lose = false
		return state.ProjectEnvironmentCloneOperation{}, objectstorage.ErrUnavailable
	}
	return op, err
}

func TestPGCloneCheckpointAdoptionRecoversOriginalJournals(t *testing.T) {
	f, base, provider, _ := cloneCaptureBarrierFixture(t)
	provider.drained = true
	store := &checkpointAdoptionLostReplyStore{cloneCaptureBarrierFailureStore: base, lose: true}
	f.srv.store = statetest.NewPg(store)
	driver := &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}}
	var err error
	f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, objectstorage.ErrUnavailable) || driver.captures != 1 {
		t.Fatal("lost adoption reply", err)
	}
	reclaimCommonCheckpointWorker(t, &f)
	resources := append([]state.ProjectEnvironmentCloneResource(nil), f.lease.Operation.Resources...)
	if len(resources) != len(driver.receipt.Sources)+3 {
		t.Fatal("adoption inventory missing", resources)
	}
	f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.captures != 1 || !reflect.DeepEqual(f.lease.Operation.Resources, resources) {
		t.Fatal("replay replaced original capture", err)
	}
	for _, snapshot := range driver.adoption.Snapshots {
		imported, err := store.PgStore.ProjectEnvironmentClonePostgresSnapshotForLease(t.Context(), f.lease, snapshot.SourceDatabaseID)
		if err != nil || imported.State != "retained" || imported.ProviderSnapshotID != snapshot.Observation.ProviderSnapshotID {
			t.Fatal("original snapshot not adopted", imported, err)
		}
		// The existing capture worker cannot acquire another dispatch claim.
		_, dispatch, err := store.PgStore.ClaimProjectEnvironmentClonePostgresSnapshotRequest(t.Context(), f.lease, snapshot.SourceDatabaseID)
		if err != nil || dispatch {
			t.Fatal("adoption authorized recapture", err)
		}
	}
	for _, captured := range driver.adoption.Manifests {
		imported, err := store.PgStore.ProjectEnvironmentCloneObjectManifest(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID, captured.SourceBucketID)
		if err != nil || imported.Hash != captured.Hash || len(imported.Objects) != len(captured.Objects) {
			t.Fatal("original manifest not adopted", err)
		}
		for i, item := range imported.Objects {
			if item.Source != captured.Objects[i] || item.CopiedAt != nil {
				t.Fatal("manifest changed or copied")
			}
		}
		var targetState string
		if err := f.pool.QueryRow(t.Context(), "select state from object_buckets where id=$1", imported.TargetBucketID).Scan(&targetState); err != nil || targetState != "provisioning" {
			t.Fatal("adoption provisioned a bucket", err)
		}
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); err != nil {
		t.Fatal("adoption released source configuration", err)
	}
	if _, err := store.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID,
		state.CloneOperationCapturing, state.CloneOperationCopying, f.lease.Operation.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("private adoption enabled publication", err)
	}
	// Loss or rewrite of an imported journal is uncertainty, never repair authority.
	snapshot := driver.adoption.Snapshots[0]
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_postgres_snapshots set provider_snapshot_id='replacement' where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, snapshot.SourceDatabaseID); err != nil {
		t.Fatal(err)
	}
	_, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if !errors.Is(err, state.ErrConflict) || driver.captures != 1 {
		t.Fatal("rewritten imported journal was repaired", err)
	}
}

func TestPGCloneCheckpointAdoptionRejectsReplacementEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*state.ProjectEnvironmentCloneCheckpointAdoption)
	}{
		{"snapshot identity", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) {
			in.Snapshots[0].Observation.ProviderSnapshotID = "replacement"
		}},
		{"snapshot expiry", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) {
			stamp := time.Now().Add(time.Hour)
			in.Snapshots[0].Observation.ExpiresAt = &stamp
		}},
		{"manifest content", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) {
			in.Manifests[0].Objects[0].VersionID = "replacement"
		}},
		{"manifest retention", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) {
			in.Manifests[0].RetainedUntil = time.Now().Add(-time.Second)
		}},
		{"missing bucket", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) { in.Manifests = in.Manifests[1:] }},
		{"duplicate bucket", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) { in.Manifests[1] = in.Manifests[0] }},
		{"receipt rewrite", func(in *state.ProjectEnvironmentCloneCheckpointAdoption) {
			in.Receipt.Sources[0].CaptureIdentity = "replacement"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, store, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			driver := &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}, edit: tc.edit}
			var err error
			f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if !errors.Is(err, state.ErrConflict) || driver.captures != 1 || len(f.lease.Operation.Resources) != 2 {
				t.Fatal("replacement accepted", err)
			}
			assertNoCheckpointAdoptionJournals(t, f)
			driver.edit = nil
			reclaimCommonCheckpointWorker(t, &f)
			f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if err != nil || driver.captures != 1 {
				t.Fatal("failed evidence required recapture", err)
			}
		})
	}
}

func assertNoCheckpointAdoptionJournals(t *testing.T, f cloneCoordinatorFixture) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(t.Context(), `select (select count(*) from project_environment_clone_postgres_snapshots where operation_id=$1)
  +(select count(*) from project_environment_clone_object_manifests where operation_id=$1)
  +(select count(*) from object_buckets where environment_clone_operation_id=$1)`, f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial adoption committed", count, err)
	}
}

func TestPGCloneCheckpointAdoptionRollsBackPrivateReservations(t *testing.T) {
	f, store, provider, _ := cloneCaptureBarrierFixture(t)
	provider.drained = true
	driver := &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}}
	var err error
	f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 1)
	if err == nil || driver.captures != 1 || len(f.lease.Operation.Resources) != 2 {
		t.Fatal("quota failure accepted", err)
	}
	assertNoCheckpointAdoptionJournals(t, f)
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil || driver.captures != 1 {
		t.Fatal("rolled back adoption required recapture", err)
	}
}

func TestPGCloneCheckpointAdoptionMissingJournalsCannotRecapture(t *testing.T) {
	for _, kind := range []string{"snapshot", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			f, store, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			driver := &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}}
			var err error
			f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "snapshot" {
				sourceID := driver.adoption.Snapshots[0].SourceDatabaseID
				if _, err := f.pool.Exec(t.Context(), "delete from project_environment_clone_postgres_snapshots where operation_id=$1 and source_database_id=$2", f.lease.Operation.ID, sourceID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.PgStore.ReserveProjectEnvironmentClonePostgresSnapshot(t.Context(), f.lease, sourceID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("missing snapshot authorized new intent", err)
				}
			} else {
				captured := driver.adoption.Manifests[0]
				original, err := store.PgStore.ProjectEnvironmentCloneObjectManifest(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID, captured.SourceBucketID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(t.Context(), "delete from project_environment_clone_object_manifests where operation_id=$1 and source_bucket_id=$2", f.lease.Operation.ID, captured.SourceBucketID); err != nil {
					t.Fatal(err)
				}
				if _, err := store.PgStore.PutProjectEnvironmentCloneObjectManifestForLease(t.Context(), f.lease, original); !errors.Is(err, state.ErrConflict) {
					t.Fatal("legacy writer replaced adopted manifest", err)
				}
				// A nil provider proves the leased capture helper rejects missing adopted
				// evidence before considering a fresh provider listing.
				if _, err := captureProjectEnvironmentObjectStorageSnapshotForLease(t.Context(), statetest.NewPg(store), nil, f.lease, captured.SourceBucketID, original.TargetBucketID, "pinned-source", captured.CapturedAt); !errors.Is(err, state.ErrConflict) {
					t.Fatal("missing manifest reached capture path", err)
				}
			}
			_, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
			if !errors.Is(err, state.ErrConflict) || driver.captures != 1 {
				t.Fatal("missing journal was repaired by recapture", err)
			}
		})
	}
}

func TestPGCloneCheckpointAdoptionKeepsConfigurationGuardClosed(t *testing.T) {
	f, store, provider, _ := cloneCaptureBarrierFixture(t)
	provider.drained = true
	driver := &checkpointAdoptionTestDriver{commonCheckpointTestDriver: &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}}
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := f.srv.capturedProjectEnvironmentObjectPlans(t.Context(), f.lease.Operation)
	if err != nil {
		t.Fatal(err)
	}
	// A live operation and source fence alone cannot bypass the guard: the
	// complete original manifest must precede a private target reservation.
	if _, _, err := store.PgStore.ReserveProjectEnvironmentCloneObjectBucket(t.Context(), f.lease, plans[0].appID, plans[0].source.ID, 10); !errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
		t.Fatal("unbacked target bypassed held configuration", err)
	}
	f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, driver, 10)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.PgStore.ProjectEnvironmentCloneObjectManifest(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID, plans[0].source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{plans[0].source.ID, manifest.TargetBucketID} {
		// The exception admits only INSERT, never source or target policy changes.
		if _, err := f.pool.Exec(t.Context(), "update object_buckets set public_read=true where id=$1", id); err == nil {
			t.Fatal("held configuration allowed bucket policy mutation")
		}
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); err != nil {
		t.Fatal("target admission reopened configuration", err)
	}
}
