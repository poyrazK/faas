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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointObjectRetirementTestStore struct {
	*checkpointObjectVerificationTestStore
	loseReserve, loseRetirementClaim, loseRetirementFinish bool
}

func (s *checkpointObjectRetirementTestStore) ReserveProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return state.ProjectEnvironmentCloneCheckpointObjectRetirement{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointObjectRetirementTestStore) ClaimProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneCheckpointObjectRetirement, bool, error) {
	in, dispatch, err := s.PgStore.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id)
	if err == nil && s.loseRetirementClaim {
		s.loseRetirementClaim = false
		return state.ProjectEnvironmentCloneCheckpointObjectRetirement{}, false, objectstorage.ErrUnavailable
	}
	return in, dispatch, err
}
func (s *checkpointObjectRetirementTestStore) FinishProjectEnvironmentCloneCheckpointObjectRetirement(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, o state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error {
	err := s.PgStore.FinishProjectEnvironmentCloneCheckpointObjectRetirement(ctx, l, id, o)
	if err == nil && s.loseRetirementFinish {
		s.loseRetirementFinish = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type checkpointObjectRetirementTestDriver struct {
	t             *testing.T
	f             *cloneCoordinatorFixture
	calls         int
	loseReply     bool
	proofs        map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation
	edit          func(*state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation)
	beforeObserve func()
}

func checkpointObjectRetirementTestProof(p state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan) state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation {
	c := p.Creation.Plan
	o := state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{RequestID: state.ProjectEnvironmentCloneCheckpointObjectRetirementRequestID(p), PlanSHA256: state.ProjectEnvironmentCloneCheckpointObjectRetirementPlanSHA256(p), OwnerToken: c.OperationID, NativeResourceID: c.PhysicalName, SourceResourceID: p.SourceResourceID, CreationRequestID: state.ProjectEnvironmentCloneCheckpointObjectTargetRequestID(c), CreationPlanSHA256: state.ProjectEnvironmentCloneCheckpointObjectTargetPlanSHA256(c), CreationPermanentlyFenced: true, AllWritersPermanentlyClosed: true, TargetAbsent: true, NoNativeGrants: true, SourceUnchanged: true, RetiredAt: time.Now().UTC().Truncate(time.Microsecond)}
	if p.Copy != nil {
		o.CopyRequestID = state.ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p.Copy.Plan)
		o.CopyPlanSHA256 = state.ProjectEnvironmentCloneCheckpointObjectCopyPlanSHA256(p.Copy.Plan)
		o.CopyPermanentlyFenced = true
	}
	return o
}
func (d *checkpointObjectRetirementTestDriver) RetireCheckpointObjectTarget(ctx context.Context, p state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan) error {
	var status string
	if err := d.f.pool.QueryRow(ctx, "select state from project_environment_clone_checkpoint_object_retirements where operation_id=$1 and source_bucket_id=$2", p.Creation.Plan.OperationID, p.Creation.Plan.SourceBucketID).Scan(&status); err != nil || status != "requested" {
		d.t.Fatal("native retirement before committed claim", status, err)
	}
	var active int
	if err := d.f.pool.QueryRow(ctx, "select count(*) from object_storage_s3_credentials where bucket_id=$1 and status<>'revoked'", p.Creation.Plan.TargetBucketID).Scan(&active); err != nil || active != 0 {
		d.t.Fatal("native retirement before withdrawal", active, err)
	}
	if _, ok := d.proofs[p.Creation.Plan.TargetBucketID]; ok {
		d.t.Fatal("native retirement redispatched")
	}
	d.calls++
	d.proofs[p.Creation.Plan.TargetBucketID] = checkpointObjectRetirementTestProof(p)
	if d.loseReply {
		d.loseReply = false
		return objectstorage.ErrUnavailable
	}
	return nil
}
func (d *checkpointObjectRetirementTestDriver) ObserveCheckpointObjectRetirement(_ context.Context, p state.ProjectEnvironmentCloneCheckpointObjectRetirementPlan) (state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation, error) {
	o, ok := d.proofs[p.Creation.Plan.TargetBucketID]
	if !ok {
		return o, objectstorage.ErrNotFound
	}
	if d.edit != nil {
		d.edit(&o)
	}
	if d.beforeObserve != nil {
		d.beforeObserve()
	}
	return o, nil
}
func checkpointObjectRetirementFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointObjectRetirementTestStore, *checkpointObjectRetirementTestDriver, state.ProjectEnvironmentCloneCheckpointObjectPublicationPlan) {
	t.Helper()
	f, base, plan := checkpointObjectWithdrawalFixture(t)
	store := &checkpointObjectRetirementTestStore{checkpointObjectVerificationTestStore: base}
	f.srv.store = statetest.NewPg(store)
	d := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
	return f, store, d, plan
}
func assertCheckpointObjectRetirementState(t *testing.T, f *cloneCoordinatorFixture, id, journal, bucket string) {
	t.Helper()
	var js, bs string
	if err := f.pool.QueryRow(t.Context(), "select r.state,b.state from project_environment_clone_checkpoint_object_retirements r join project_environment_clone_checkpoint_object_targets c using(operation_id,source_bucket_id) join object_buckets b on b.id=c.target_bucket_id where r.operation_id=$1 and r.source_bucket_id=$2", f.lease.Operation.ID, id).Scan(&js, &bs); err != nil || js != journal || bs != bucket {
		t.Fatal("retirement not atomic", js, bs, err)
	}
}

func TestPGCloneCheckpointObjectRetirementRecovery(t *testing.T) {
	for _, lost := range []string{"reserve", "claim", "native", "finish"} {
		t.Run(lost, func(t *testing.T) {
			f, store, d, plan := checkpointObjectRetirementFixture(t)
			store.loseReserve = lost == "reserve"
			store.loseRetirementClaim = lost == "claim"
			store.loseRetirementFinish = lost == "finish"
			d.loseReply = lost == "native"
			var err error
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			expected := objectstorage.ErrUnavailable
			if lost == "native" {
				expected = errCloneCheckpointUnavailable
			}
			if !errors.Is(err, expected) {
				t.Fatal("lost reply", err)
			}
			old := f.lease
			assertCheckpointObjectWithdrawalState(t, f, plan, true)
			reclaimCommonCheckpointWorker(t, f)
			if lost == "claim" {
				if d.calls != 0 {
					t.Fatal("unknown claim dispatched")
				}
				f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
				if !errors.Is(err, objectstorage.ErrNotFound) || d.calls != 0 {
					t.Fatal("unknown claim redispatched", err)
				}
				// Only independently arriving permanent closure proof may unblock this
				// request. The recovering worker does not issue another native write.
				var id string
				if err = f.pool.QueryRow(t.Context(), "select source_bucket_id::text from project_environment_clone_checkpoint_object_retirements where operation_id=$1 and state='requested'", f.lease.Operation.ID).Scan(&id); err != nil {
					t.Fatal(err)
				}
				in, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, id)
				if err != nil {
					t.Fatal(err)
				}
				d.proofs[in.Plan.Creation.Plan.TargetBucketID] = checkpointObjectRetirementTestProof(in.Plan)
			}
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("worker handoff", err)
			}
			want := len(plan.Buckets)
			if lost == "claim" {
				want--
			}
			if d.calls != want {
				t.Fatal("native dispatch count", d.calls, want)
			}
			before := d.calls
			for _, b := range plan.Buckets {
				assertCheckpointObjectRetirementState(t, f, b.SourceBucketID, "retired", "deleted")
				in, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
				if err != nil || in.State != "retired" || in.Plan.Copy == nil || in.Plan.Copy.State != "copied" || in.Observation.RequestID == "" {
					t.Fatal("retired journal replay", err)
				}
				if _, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID); err != nil || dispatch {
					t.Fatal("retired claim", dispatch, err)
				}
				if _, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), old, b.SourceBucketID); !errors.Is(err, state.ErrConflict) {
					t.Fatal("stale worker reserved", err)
				}
			}
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil || d.calls != before {
				t.Fatal("retired operation redispatched", err)
			}
			var sources int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from object_buckets where account_id=$1 and scope='production' and state='ready'", plan.AccountID).Scan(&sources); err != nil || sources != len(plan.Buckets) {
				t.Fatal("source changed", sources, err)
			}
			var grants int
			if err = f.pool.QueryRow(t.Context(), "select count(*) from object_storage_access_grants g join object_buckets b on b.id=g.bucket_id where b.account_id=$1 and b.scope='production'", plan.AccountID).Scan(&grants); err != nil || grants != 2 {
				t.Fatal("source grants changed", grants, err)
			}
		})
	}
}

func TestPGCloneCheckpointObjectRetirementProofFences(t *testing.T) {
	for _, failure := range []string{"create", "copy", "writers", "grants", "source", "request", "receipt", "expired-worker", "rollback", "rollback-bucket"} {
		t.Run(failure, func(t *testing.T) {
			f, store, d, plan := checkpointObjectRetirementFixture(t)
			d.edit = func(o *state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation) {
				switch failure {
				case "create":
					o.CreationPermanentlyFenced = false
				case "copy":
					o.CopyPermanentlyFenced = false
				case "writers":
					o.AllWritersPermanentlyClosed = false
				case "grants":
					o.NoNativeGrants = false
				case "source":
					o.SourceUnchanged = false
				case "request":
					o.CreationRequestID += "x"
				case "receipt":
					o.PlanSHA256 += "x"
				}
			}
			if failure == "expired-worker" {
				d.beforeObserve = func() {
					if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=$1", f.lease.Operation.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if failure == "rollback" {
				if _, err := f.pool.Exec(t.Context(), `create function expire_object_retirement_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_object_retirement_worker after update on project_environment_clone_checkpoint_object_retirements for each row when(NEW.state='retired') execute function expire_object_retirement_worker()`); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "rollback-bucket" {
				if _, err := f.pool.Exec(t.Context(), `create function expire_retired_bucket_worker() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.environment_clone_operation_id; return NEW; end $$; create trigger expire_retired_bucket_worker after update on object_buckets for each row when(NEW.state='deleted') execute function expire_retired_bucket_worker()`); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if !errors.Is(err, state.ErrConflict) || d.calls != 1 {
				t.Fatal("invalid retirement committed", err, d.calls)
			}
			var id string
			if err = f.pool.QueryRow(t.Context(), "select source_bucket_id::text from project_environment_clone_checkpoint_object_retirements where operation_id=$1 and state='requested'", f.lease.Operation.ID).Scan(&id); err != nil {
				t.Fatal(err)
			}
			assertCheckpointObjectRetirementState(t, f, id, "requested", "ready")
			if failure == "rollback" {
				if _, err = f.pool.Exec(t.Context(), "drop trigger expire_object_retirement_worker on project_environment_clone_checkpoint_object_retirements;drop function expire_object_retirement_worker()"); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "rollback-bucket" {
				if _, err = f.pool.Exec(t.Context(), "drop trigger expire_retired_bucket_worker on object_buckets;drop function expire_retired_bucket_worker()"); err != nil {
					t.Fatal(err)
				}
			}
			d.edit = nil
			d.beforeObserve = nil
			reclaimCommonCheckpointWorker(t, f)
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil || d.calls != len(plan.Buckets) {
				t.Fatal("proof recovery redispatched", err, d.calls)
			}
			in, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, id)
			if err != nil {
				t.Fatal(err)
			}
			changed := in.Observation
			changed.RetiredAt = changed.RetiredAt.Add(-time.Microsecond)
			if err = store.FinishProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, id, changed); !errors.Is(err, state.ErrConflict) {
				t.Fatal("changed terminal receipt", err)
			}
		})
	}
}

func TestPGCloneCheckpointObjectRetirementOwnership(t *testing.T) {
	for _, failure := range []string{"published", "name", "placement", "scope", "native-name", "missing-manifest", "copy-source", "copy-version", "copy-state", "revived-credential", "extra-revoked", "surviving-grant", "journal", "copy-deadline", "unproved-deletion", "ordinary-worker"} {
		t.Run(failure, func(t *testing.T) {
			f, store, _, plan := checkpointObjectRetirementFixture(t)
			b := plan.Buckets[0]
			for _, candidate := range plan.Buckets {
				if len(candidate.Preparations) > 0 {
					b = candidate
					break
				}
			}
			if failure != "published" {
				if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "journal" || failure == "copy-deadline" {
				if _, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch failure {
			case "name":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set name='foreign' where id=$1", b.TargetBucketID)
			case "placement":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set backend_fingerprint=$2 where id=$1", b.TargetBucketID, strings.Repeat("f", 64))
			case "scope":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set scope='foreign' where id=$1", b.TargetBucketID)
			case "native-name":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set physical_name='foreign' where id=$1", b.TargetBucketID)
			case "missing-manifest":
				_, err = f.pool.Exec(t.Context(), "delete from project_environment_clone_object_manifests where operation_id=$1 and source_bucket_id=$2", plan.OperationID, b.SourceBucketID)
			case "copy-source":
				_, err = f.pool.Exec(t.Context(), `update project_environment_clone_checkpoint_object_copies set plan=jsonb_set(plan,'{Inventory,SourceResourceID}','"foreign"') where operation_id=$1 and source_bucket_id=$2`, plan.OperationID, b.SourceBucketID)
			case "copy-version":
				_, err = f.pool.Exec(t.Context(), `update project_environment_clone_checkpoint_object_copies set plan=jsonb_set(plan,'{Inventory,Objects,0,version_id}','"foreign"') where operation_id=$1 and source_bucket_id=$2`, plan.OperationID, b.SourceBucketID)
			case "copy-state":
				_, err = f.pool.Exec(t.Context(), "update project_environment_clone_checkpoint_object_copies set state='planned',requested_at=null,observation_sha256=null,copied_at=null where operation_id=$1 and source_bucket_id=$2", plan.OperationID, b.SourceBucketID)
			case "revived-credential":
				_, err = f.pool.Exec(t.Context(), "update object_storage_s3_credentials set status='active',revoked_at=null where bucket_id=$1", b.TargetBucketID)
			case "extra-revoked":
				access, _, e := api.GenerateObjectS3Credential()
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.pool.Exec(t.Context(), "insert into object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,revoked_at) values($1,$2,$3,$4,$5,'kid','foreign','read','revoked',clock_timestamp())", uuid.NewString(), plan.AccountID, b.TargetBucketID, access, []byte("opaque"))
			case "surviving-grant":
				_, err = f.pool.Exec(t.Context(), "insert into object_storage_access_grants(account_id,bucket_id,api_key_id,permission) select $1,$2,api_key_id,'read' from object_storage_access_grants where bucket_id=$3 limit 1", plan.AccountID, b.TargetBucketID, b.SourceBucketID)
			case "journal":
				_, err = f.pool.Exec(t.Context(), `update project_environment_clone_checkpoint_object_retirements set plan=jsonb_set(plan,'{Creation,Plan,BackendFingerprint}','"foreign"') where operation_id=$1 and source_bucket_id=$2`, plan.OperationID, b.SourceBucketID)
			case "copy-deadline":
				_, err = f.pool.Exec(t.Context(), `update project_environment_clone_checkpoint_object_copies set plan=jsonb_set(plan,'{WriteUntil}','"2099-01-01T00:00:00Z"') where operation_id=$1 and source_bucket_id=$2`, plan.OperationID, b.SourceBucketID)
			case "unproved-deletion":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set state='deleted' where id=$1", b.TargetBucketID)
			case "ordinary-worker":
				_, err = f.pool.Exec(t.Context(), "update object_buckets set lease_token=$2,lease_until=clock_timestamp()+interval '1 minute' where id=$1", b.TargetBucketID, uuid.NewString())
			}
			if err != nil {
				t.Fatal("tamper setup", err)
			}
			if _, err = store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unauthenticated cleanup plan accepted", failure, err)
			}
		})
	}
}

func TestPGCloneCheckpointObjectRetirementUnpublishedRequests(t *testing.T) {
	for _, phase := range []string{"no-creation", "planned-creation", "unknown-creation", "ready", "planned-copy", "unknown-copy"} {
		t.Run(phase, func(t *testing.T) {
			f, store, target := checkpointTargetFixture(t)
			var err error
			if phase == "planned-creation" {
				views, err := store.PgStore.ProjectEnvironmentCloneBindings(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, view := range views {
					for _, b := range view.Buckets {
						var id string
						if err = f.pool.QueryRow(t.Context(), "select target_bucket_id::text from project_environment_clone_object_manifests where operation_id=$1 and source_bucket_id=$2", f.lease.Operation.ID, b.ID).Scan(&id); err != nil {
							t.Fatal(err)
						}
						if _, err = store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectTarget(t.Context(), f.lease, view.AppID, b.ID, id); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if phase != "no-creation" {
				target.loseCreate = phase == "unknown-creation"
				f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
				if err != nil && !(phase == "unknown-creation" && errors.Is(err, objectstorage.ErrUnavailable)) {
					t.Fatal(err)
				}
			}
			if phase == "planned-copy" || phase == "unknown-copy" {
				if phase == "planned-copy" {
					for _, r := range f.lease.Operation.Resources {
						if r.Kind == "object_storage" {
							if _, err = store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectCopy(t.Context(), f.lease, r.SourceID); err != nil {
								t.Fatal(err)
							}
						}
					}
				} else {
					copy := &checkpointObjectCopyTestDriver{checkpointTargetTestDriver: target, loseReply: true, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectCopyObservation{}}
					f.lease, err = f.srv.copyCheckpointObjects(t.Context(), f.lease, copy)
					if !errors.Is(err, objectstorage.ErrUnavailable) {
						t.Fatal("unknown copy", err)
					}
				}
			}
			// Capturing workers have no native retirement authority.
			for _, r := range f.lease.Operation.Resources {
				if r.Kind == "object_storage" {
					if _, err = store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, r.SourceID); !errors.Is(err, state.ErrConflict) {
						t.Fatal("capturing cleanup", err)
					}
					break
				}
			}
			compensateCheckpointObjectFixture(t, f)
			d := &checkpointObjectRetirementTestDriver{t: t, f: f, proofs: map[string]state.ProjectEnvironmentCloneCheckpointObjectRetirementObservation{}}
			f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("unpublished cleanup", err)
			}
			want := 2
			if phase == "no-creation" {
				want = 0
			}
			if phase == "unknown-creation" {
				want = 1
			}
			if d.calls != want {
				t.Fatal("request cleanup count", d.calls, want)
			}
			{
				var retired int
				if err = f.pool.QueryRow(t.Context(), "select count(*) from object_buckets where environment_clone_operation_id=$1 and state='deleted'", f.lease.Operation.ID).Scan(&retired); err != nil || retired != 2 {
					t.Fatal("incomplete unused/native cleanup", retired, err)
				}
			}
		})
	}
}

func TestPGCloneCheckpointObjectRetirementClaimRollback(t *testing.T) {
	f, store, _, plan := checkpointObjectRetirementFixture(t)
	if err := store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	b := plan.Buckets[0]
	original, err := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), `create function expire_object_retirement_claim() returns trigger language plpgsql as $$ begin update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=NEW.operation_id; return NEW; end $$; create trigger expire_object_retirement_claim after update on project_environment_clone_checkpoint_object_retirements for each row when(NEW.state='requested') execute function expire_object_retirement_claim()`); err != nil {
		t.Fatal(err)
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
	if !errors.Is(err, state.ErrConflict) || dispatch || !reflect.DeepEqual(in, state.ProjectEnvironmentCloneCheckpointObjectRetirement{}) {
		t.Fatal("failed claim conveyed dispatch", dispatch, err)
	}
	assertCheckpointObjectRetirementState(t, f, b.SourceBucketID, "planned", "ready")
	if _, err = f.pool.Exec(t.Context(), "drop trigger expire_object_retirement_claim on project_environment_clone_checkpoint_object_retirements;drop function expire_object_retirement_claim()"); err != nil {
		t.Fatal(err)
	}
	in, dispatch, err = store.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
	if err != nil || !dispatch || !reflect.DeepEqual(in.Plan, original.Plan) {
		t.Fatal("claim retry changed plan", err)
	}
}

func TestPGCloneCheckpointObjectRetirementMigration(t *testing.T) {
	f, store, _, plan := checkpointObjectRetirementFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006368_clone_checkpoint_object_retirements.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty retirement round trip", err)
		}
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = store.PgStore.WithdrawProjectEnvironmentCloneCheckpointObjects(t.Context(), f.lease); err != nil {
		t.Fatal(err)
	}
	b := plan.Buckets[0]
	for _, phase := range []string{"planned", "requested", "retired"} {
		if phase == "planned" {
			_, err = store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
		}
		if phase == "requested" {
			_, _, err = store.ClaimProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
		}
		if phase == "retired" {
			in, e := store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID)
			if e != nil {
				t.Fatal(e)
			}
			err = store.FinishProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID, checkpointObjectRetirementTestProof(in.Plan))
		}
		if err != nil {
			t.Fatal(phase, err)
		}
		tx, err = f.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), parts[1])
		_ = tx.Rollback(t.Context())
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_checkpoint_object_retirements_down_no_ownership" {
			t.Fatal("owned retirement downgrade", phase, err)
		}
	}
}

func TestPGCloneCheckpointObjectRetirementAdmission(t *testing.T) {
	for _, failure := range []string{"nil-driver", "admission", "capturing"} {
		t.Run(failure, func(t *testing.T) {
			f, _, d, plan := checkpointObjectRetirementFixture(t)
			var driver cloneCheckpointObjectRetirementDriver = d
			expected := errCloneCheckpointUnavailable
			if failure == "nil-driver" {
				driver = nil
			}
			if failure == "admission" {
				expected = errors.New("admission paused")
				f.srv.cloneWorkerAdmission = func(context.Context) error { return expected }
			}
			if failure == "capturing" {
				f.lease.Operation.Status = state.CloneOperationCapturing
				expected = state.ErrConflict
			}
			if _, err := f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, driver); !errors.Is(err, expected) || d.calls != 0 {
				t.Fatal("cleanup bypassed admission", err, d.calls)
			}
			assertCheckpointObjectWithdrawalState(t, f, plan, false)
			var n int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_object_retirements where operation_id=$1", plan.OperationID).Scan(&n); err != nil || n != 0 {
				t.Fatal("unauthorized native request reserved", n, err)
			}
		})
	}
}

func TestPGCloneCheckpointObjectRetirementForeignUnpublishedCredential(t *testing.T) {
	f, store, target := checkpointTargetFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointObjectTargets(t.Context(), f.lease, target)
	if err != nil {
		t.Fatal(err)
	}
	compensateCheckpointObjectFixture(t, f)
	var resource state.ProjectEnvironmentCloneResource
	for _, r := range f.lease.Operation.Resources {
		if r.Kind == "object_storage" {
			resource = r
			break
		}
	}
	access, _, err := api.GenerateObjectS3Credential()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(t.Context(), "insert into object_storage_s3_credentials(id,account_id,bucket_id,access_key_id,secret_sealed,kid,label,permission,status,revoked_at) values($1,$2,$3,$4,$5,'kid','foreign','read','revoked',clock_timestamp())", uuid.NewString(), f.lease.Operation.AccountID, resource.TargetID, access, []byte("opaque")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PgStore.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, resource.SourceID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("foreign revoked credential authorized native cleanup", err)
	}
}

func TestPGCloneCheckpointObjectRetirementRevival(t *testing.T) {
	f, store, d, plan := checkpointObjectRetirementFixture(t)
	var err error
	f.lease, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	b := plan.Buckets[0]
	if _, err = f.pool.Exec(t.Context(), "update object_buckets set state='ready' where id=$1", b.TargetBucketID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReserveProjectEnvironmentCloneCheckpointObjectRetirement(t.Context(), f.lease, b.SourceBucketID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("retired bucket revival accepted", err)
	}
	if _, err = f.srv.retireCheckpointObjectTargets(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) || d.calls != len(plan.Buckets) {
		t.Fatal("retired bucket repaired by native redispatch", err, d.calls)
	}
}
