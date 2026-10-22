//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointSourceCompensationTestStore struct {
	*checkpointReleaseLostReplyStore
	loseReserve, loseSourceClaim, loseSourceFinish bool
}

func (s *checkpointSourceCompensationTestStore) ReserveProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease, p state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, p)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return state.ProjectEnvironmentCloneCheckpointSourceCompensation{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointSourceCompensationTestStore) ClaimProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneCheckpointSourceCompensation, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l)
	if err == nil && s.loseSourceClaim {
		s.loseSourceClaim = false
		return state.ProjectEnvironmentCloneCheckpointSourceCompensation{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointSourceCompensationTestStore) FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx context.Context, l state.ProjectEnvironmentCloneLease, o state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, o)
	if err == nil && s.loseSourceFinish {
		s.loseSourceFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointSourceCompensationTestDriver struct {
	t             *testing.T
	f             *cloneCoordinatorFixture
	calls         int
	loseReply     bool
	proof         state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation
	beforeObserve func()
}

func checkpointSourceCompensationTestProof(p state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation {
	c := p.Capture
	at := time.Now().UTC().Truncate(time.Microsecond)
	o := state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation{RequestID: state.ProjectEnvironmentCloneCheckpointSourceCompensationRequestID(c.OperationID), PlanSHA256: cloneCheckpointDigest(p), OwnerToken: c.OperationID, CaptureRequestID: c.OperationID, CapturePlanSHA256: cloneCheckpointDigest(c), CapturePermanentlyFenced: true, CompensatedAt: at}
	if p.Release != nil {
		o.ReleaseRequestID = state.ProjectEnvironmentCloneCheckpointReleaseRequestID(c.OperationID)
		o.ReleasePlanSHA256 = cloneCheckpointDigest(p.Release.Plan)
		o.ReleasePermanentlyFenced = true
	}
	for n, s := range c.Sources {
		resource, identity := s.ProviderResourceID, ""
		if s.Kind == "managed_postgres" {
			resource = s.DataResourceID
		}
		if p.Receipt != nil {
			identity = p.Receipt.Sources[n].CaptureIdentity
		}
		o.Sources = append(o.Sources, state.ProjectEnvironmentCloneCheckpointCompensatedSource{Kind: s.Kind, ID: s.ID, SourceResourceID: resource, CaptureIdentity: identity, OwnerToken: c.OperationID, TerminalState: "abandoned", NoNativeHolds: true, SourceAccessRestored: true, SourceUnchanged: true, ReleasedAt: at})
	}
	return o
}
func (d *checkpointSourceCompensationTestDriver) CompensateCheckpointSources(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_source_compensations where operation_id=$1", p.Capture.OperationID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("native cleanup preceded committed claim", status, err)
	}
	if d.calls != 0 {
		d.t.Fatal("source cleanup redispatched")
	}
	d.calls++
	d.proof = checkpointSourceCompensationTestProof(p)
	p.Capture.Sources[0].ID = "mutated-input"
	if p.Receipt != nil {
		p.Receipt.Sources[0].CaptureIdentity = "mutated-input"
	}
	if p.Release != nil {
		p.Release.Plan.Sources[0].ID = "mutated-input"
	}
	if d.loseReply {
		d.loseReply = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointSourceCompensationTestDriver) ObserveCheckpointSourceCompensation(_ context.Context, p state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation, error) {
	if d.proof.RequestID == "" {
		return d.proof, objectstorage.ErrNotFound
	}
	if d.proof.PlanSHA256 != cloneCheckpointDigest(p) {
		return d.proof, state.ErrConflict
	}
	if d.beforeObserve != nil {
		d.beforeObserve()
	}
	o := d.proof
	o.Sources = append([]state.ProjectEnvironmentCloneCheckpointCompensatedSource(nil), o.Sources...)
	return o, nil
}

func checkpointSourceCompensationFixture(t *testing.T, phase string) (*cloneCoordinatorFixture, *checkpointSourceCompensationTestStore, *checkpointSourceCompensationTestDriver, state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan, []state.ObjectBucket) {
	t.Helper()
	f, base, capture, buckets := checkpointReleaseFixture(t)
	store := &checkpointSourceCompensationTestStore{checkpointReleaseLostReplyStore: base}
	f.srv.store = statetest.NewPg(store)
	var err error
	if phase == "planned" {
		f.lease, _, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
		if err != nil {
			t.Fatal(err)
		}
		p, _, _, err := f.srv.cloneCommonCheckpointPlan(t.Context(), f.lease, statetest.NewPg(store))
		if err != nil {
			t.Fatal(err)
		}
		f.lease, err = f.srv.advanceCloneCommonCheckpointIntent(t.Context(), statetest.NewPg(store), f.lease, []state.ProjectEnvironmentCloneResource{{Kind: "data_checkpoint", Name: p.SourceEnvironment, SourceID: p.OperationID, SourceVersion: cloneCheckpointDigest(p), Status: "planned"}})
		if err != nil {
			t.Fatal(err)
		}
	} else if phase == "unknown-capture" || phase == "captured" {
		capture.loseCapture = phase == "unknown-capture"
		f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, capture)
		if phase == "unknown-capture" && errors.Is(err, objectstorage.ErrUnavailable) {
			err = nil
		}
	} else if phase == "released" || phase == "unknown-release" {
		capture.loseRelease = phase == "unknown-release"
		f.lease, err = f.srv.releaseProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, capture, 10)
		if phase == "unknown-release" && errors.Is(err, objectstorage.ErrUnavailable) {
			err = nil
		}
	} else {
		f.lease, err = f.srv.adoptProjectEnvironmentCloneCheckpoint(t.Context(), f.lease, capture, 10)
	}
	if err != nil {
		t.Fatal(phase, err)
	}
	p := state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan{Intent: f.lease.Operation.Resources[0]}
	if phase == "released" {
		r, err := store.PgStore.ProjectEnvironmentCloneCheckpointReleaseForLease(t.Context(), f.lease)
		if err != nil {
			t.Fatal(err)
		}
		p.Capture = r.Plan
		p.Release = &r
	} else {
		p.Capture, _, _, err = f.srv.cloneCommonCheckpointPlan(t.Context(), f.lease, statetest.NewPg(store))
		if err != nil {
			t.Fatal(err)
		}
		if phase == "release-planned" || phase == "release-requested" {
			r, err := store.PgStore.ReserveProjectEnvironmentCloneCheckpointRelease(t.Context(), f.lease, p.Capture, capture.receipt)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "release-requested" {
				r, _, err = store.ClaimProjectEnvironmentCloneCheckpointRelease(t.Context(), f.lease)
				if err != nil {
					t.Fatal(err)
				}
			}
			p.Release = &r
		} else if phase == "unknown-release" {
			r, err := store.PgStore.ProjectEnvironmentCloneCheckpointReleaseForLease(t.Context(), f.lease)
			if err != nil {
				t.Fatal(err)
			}
			p.Release = &r
		}
	}
	if p.Intent.Status == "captured" {
		r := capture.receipt
		r.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), r.Sources...)
		p.Receipt = &r
	}
	compensateCheckpointObjectFixture(t, &f)
	return &f, store, &checkpointSourceCompensationTestDriver{t: t, f: &f}, p, buckets
}

func assertCheckpointSourceCompensationJournal(t *testing.T, f *cloneCoordinatorFixture, store *checkpointSourceCompensationTestStore, want string) {
	t.Helper()
	in, err := store.PgStore.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(t.Context(), f.lease)
	if err != nil || in.State != want {
		t.Fatal("source cleanup journal", in.State, err)
	}
	var held int
	if err = f.pool.QueryRow(t.Context(), `select
 (select count(*) from project_environment_clone_configuration_guards where operation_id=$1 and state='held')+
 (select count(*) from project_environment_clone_postgres_write_fences where operation_id=$1 and state<>'released')+
 (select count(*) from object_bucket_write_fences where clone_operation_id=$1)`, f.lease.Operation.ID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if want == "compensated" && held != 0 || want != "compensated" && held != 4 {
		t.Fatal("partial local hold release", want, held)
	}
}

func TestPGCloneCheckpointSourceCompensationOriginalStates(t *testing.T) {
	for _, phase := range []string{"planned", "unknown-capture", "captured", "adopted", "release-planned", "release-requested", "unknown-release", "released"} {
		t.Run(phase, func(t *testing.T) {
			f, store, d, p, buckets := checkpointSourceCompensationFixture(t, phase)
			resources := append([]state.ProjectEnvironmentCloneResource(nil), f.lease.Operation.Resources...)
			var err error
			f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, d)
			if err != nil {
				t.Fatal("source compensation", err)
			}
			assertCheckpointSourceCompensationJournal(t, f, store, "compensated")
			if !reflect.DeepEqual(resources, f.lease.Operation.Resources) {
				t.Fatal("rewrote original resources")
			}
			for _, bucket := range buckets {
				m, err := store.PgStore.BeginObjectBucketMutation(t.Context(), bucket, state.ObjectBucketMutationRequest)
				if err != nil {
					t.Fatal("source access not restored", err)
				}
				if err = store.PgStore.FinishObjectBucketMutation(t.Context(), m); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "adopted" {
				for _, s := range p.Capture.Sources {
					if s.Kind == "object_storage" {
						if err = store.PgStore.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(t.Context(), f.lease, s.ID); err != nil {
							t.Fatal("target cleanup lost original plan after source reopening", err)
						}
					}
				}
			}
			stale := f.lease
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, nil, d)
			if err != nil || d.calls != 1 {
				t.Fatal("handoff redispatched cleanup", d.calls, err)
			}
			if err = store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), stale, d.proof); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale worker finished", err)
			}
		})
	}
}

func TestPGCloneCheckpointSourceCompensationLostAcknowledgements(t *testing.T) {
	for _, fault := range []string{"reserve", "claim", "native", "finish"} {
		t.Run(fault, func(t *testing.T) {
			f, store, d, p, buckets := checkpointSourceCompensationFixture(t, "unknown-release")
			store.loseReserve = fault == "reserve"
			store.loseSourceClaim = fault == "claim"
			store.loseSourceFinish = fault == "finish"
			d.loseReply = fault == "native"
			var err error
			f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, d)
			if err == nil {
				t.Fatal("lost acknowledgement was ignored")
			}
			want := "requested"
			if fault == "reserve" {
				want = "planned"
			}
			if fault == "finish" {
				want = "compensated"
			}
			assertCheckpointSourceCompensationJournal(t, f, store, want)
			if fault != "finish" {
				assertCheckpointReleaseHeld(t, *f, store.checkpointReleaseLostReplyStore, buckets)
			} else {
				if err = store.PgStore.UpsertAppEnvInScope(t.Context(), p.Capture.AccountID, f.apps[0].ID, "production", "CAPTURED", "changed"); err != nil {
					t.Fatal("source configuration not reopened", err)
				}
			}
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, nil, d)
			if fault == "claim" {
				if !errors.Is(err, objectstorage.ErrNotFound) || d.calls != 0 {
					t.Fatal("lost claim authorized another dispatch", d.calls, err)
				}
				assertCheckpointSourceCompensationJournal(t, f, store, "requested")
			} else if err != nil || d.calls != 1 {
				t.Fatal("original compensation recovery", d.calls, err)
			}
		})
	}
}

func TestPGCloneCheckpointSourceCompensationRejectsProofAndBypasses(t *testing.T) {
	f, store, d, p, buckets := checkpointSourceCompensationFixture(t, "unknown-release")
	if _, err := store.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease, p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	proof := checkpointSourceCompensationTestProof(p)
	for _, mutate := range []func(*state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation){
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.CapturePermanentlyFenced = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.ReleasePermanentlyFenced = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources = o.Sources[:1]
		},
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].SourceResourceID = "foreign"
		},
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].NoNativeHolds = false
		},
		func(o *state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation) {
			o.Sources[0].SourceAccessRestored = false
		},
	} {
		o := proof
		o.Sources = append([]state.ProjectEnvironmentCloneCheckpointCompensatedSource(nil), proof.Sources...)
		mutate(&o)
		if err := store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease, o); !errors.Is(err, state.ErrConflict) {
			t.Fatal("accepted incomplete source proof", err)
		}
		assertCheckpointSourceCompensationJournal(t, f, store, "requested")
	}
	if err := store.PgStore.AbandonProjectEnvironmentCloneConfigurationFence(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("configuration abandonment bypass", err)
	}
	if err := store.PgStore.AbandonProjectEnvironmentCloneObjectWriteFences(t.Context(), f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatal("object abandonment bypass", err)
	}
	if _, err := store.PgStore.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(t.Context(), f.lease, p.Capture.Sources[0].ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("postgres abandonment bypass", err)
	}
	assertCheckpointReleaseHeld(t, *f, store.checkpointReleaseLostReplyStore, buckets)
	altered, _ := cloneCheckpointSourceCompensationPlanCopy(p)
	altered.Release.State = "planned"
	altered.Release.RequestedAt = time.Time{}
	if _, err := store.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease, altered); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed saved compensation plan", err)
	}
	// A cancelled or superseded worker cannot commit independently valid proof.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx, f.lease, proof); err == nil {
		t.Fatal("cancelled worker finished")
	}
	d.proof = proof
	stale := f.lease
	reclaimCommonCheckpointWorker(t, f)
	if err := store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), stale, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("superseded worker finished", err)
	}
	if err := store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease, proof); err != nil {
		t.Fatal(err)
	}
	changed := proof
	changed.CompensatedAt = changed.CompensatedAt.Add(time.Microsecond)
	if err := store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(t.Context(), f.lease, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed completed proof", err)
	}
}

func TestPGCloneCheckpointSourceCompensationAtomicFinishAndMigration(t *testing.T) {
	f, store, d, p, buckets := checkpointSourceCompensationFixture(t, "adopted")
	raw, err := migrations.FS.ReadFile("20261011232006377_clone_checkpoint_source_compensation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	roundTripTx := tx
	defer func() { _ = roundTripTx.Rollback(context.WithoutCancel(t.Context())) }()
	// Remove empty descendants in reverse dependency order, using their
	// guarded DOWN paths rather than bypassing retained-evidence checks.
	for _, filename := range []string{
		"20261011232006413_clone_terminal_compensation_receipts.sql",
		"20261011232006411_clone_deployment_cleanup_receipts.sql",
		"20261011232006408_clone_environment_metadata_cleanups.sql",
		"20261011232006400_clone_workload_configuration_cleanups.sql",
		"20261011232006397_clone_configuration_cleanup_receipts.sql",
		"20261011232006392_clone_checkpoint_workload_retirements.sql",
	} {
		child, err := migrations.FS.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), strings.SplitN(string(child), "-- +goose Down", 2)[1]); err != nil {
			t.Fatal("empty descendant downgrade", filename, err)
		}
	}
	// Also remove the independent newer capture-retirement journals.
	// Its FK deliberately protects the original source-compensation evidence.
	catalogueRaw, err := migrations.FS.ReadFile("20261011232006385_clone_checkpoint_capture_catalogue_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(catalogueRaw), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal("empty newer catalogue downgrade", err)
	}
	retirementRaw, err := migrations.FS.ReadFile("20261011232006383_clone_checkpoint_capture_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	retirementParts := strings.SplitN(string(retirementRaw), "-- +goose Down", 2)
	if _, err = tx.Exec(t.Context(), retirementParts[1]); err != nil {
		t.Fatal("empty newer retirement downgrade", err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty migration round trip", err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `CREATE FUNCTION reject_source_compensation_finish() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF OLD.state='held' AND NEW.state='open' THEN RAISE EXCEPTION 'injected final source failure' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_source_compensation_finish BEFORE UPDATE ON project_environment_clone_configuration_guards FOR EACH ROW EXECUTE FUNCTION reject_source_compensation_finish();`); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, &p, d)
	if err == nil {
		t.Fatal("final source failure ignored")
	}
	assertCheckpointSourceCompensationJournal(t, f, store, "requested")
	assertCheckpointReleaseHeld(t, *f, store.checkpointReleaseLostReplyStore, buckets)
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_source_compensations_down_no_ownership" {
		t.Fatal("downgrade erased requested authority", err)
	}
	if _, err = f.pool.Exec(t.Context(), "DROP TRIGGER reject_source_compensation_finish ON project_environment_clone_configuration_guards; DROP FUNCTION reject_source_compensation_finish();"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, nil, d)
	if err != nil || d.calls != 1 {
		t.Fatal("rollback redispatched native cleanup", err)
	}
	assertCheckpointSourceCompensationJournal(t, f, store, "compensated")
	// A different capture may now own fresh holds. Replay cannot clear them.
	later := uuid.NewString()
	if _, err = f.pool.Exec(t.Context(), `insert into project_environment_clone_operations(id,account_id,project_id,source_environment,target_environment,idempotency_key,source_revision_hash,status)
 select $2,account_id,project_id,source_environment,'later-stage','later-stage',source_revision_hash,'capturing' from project_environment_clone_operations where id=$1`, f.lease.Operation.ID, later); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `update project_environment_clone_configuration_guards set generation=generation+1,state='held',operation_id=$1,source_environment='production',source_revision_hash=$2,held_at=clock_timestamp() where project_id=$3`, later, p.Capture.SourceRevisionHash, p.Capture.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `insert into object_bucket_write_fences(bucket_id,token,backend_id,backend_fingerprint,physical_name,clone_operation_id)
        select id,$1,backend_id,backend_fingerprint,physical_name,$1 from object_buckets where id=any($2::uuid[])`, later, []string{buckets[0].ID, buckets[1].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `insert into project_environment_clone_postgres_write_fences(operation_id,source_database_id,source_version,backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id)
        select $2,source_database_id,source_version,backend_id,backend_fingerprint,source_provider_resource_id,source_data_resource_id from project_environment_clone_postgres_write_fences where operation_id=$1`, f.lease.Operation.ID, later); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.compensateCheckpointSources(t.Context(), f.lease, nil, d)
	if err != nil {
		t.Fatal("completed replay depended on later capture", err)
	}
	var laterHeld int
	if err = f.pool.QueryRow(t.Context(), `select
       (select count(*) from object_bucket_write_fences where clone_operation_id=$1)+
       (select count(*) from project_environment_clone_postgres_write_fences where operation_id=$1 and state='held')`, later).Scan(&laterHeld); err != nil || laterHeld != 3 {
		t.Fatal("cleared later data holds", laterHeld, err)
	}

	var owner string
	if err = f.pool.QueryRow(t.Context(), "select operation_id::text from project_environment_clone_configuration_guards where project_id=$1 and state='held'", p.Capture.ProjectID).Scan(&owner); err != nil || owner != later {
		t.Fatal("cleared later source guard", owner, err)
	}
	tx, err = f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), parts[1])
	_ = tx.Rollback(t.Context())
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_source_compensations_down_no_ownership" {
		t.Fatal("downgrade erased completed evidence", err)
	}
}
