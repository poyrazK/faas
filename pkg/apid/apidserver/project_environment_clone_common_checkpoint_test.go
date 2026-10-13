//go:build !no_pg

// adr: 590
package apidserver

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type commonCheckpointTestDriver struct {
	t                 *testing.T
	store             cloneCommonCheckpointStore
	captures, reads   int
	loseCapture       bool
	receipt           cloneCommonCheckpointReceipt
	mutateObservation func(*cloneCommonCheckpointReceipt)
}

func (d *commonCheckpointTestDriver) Capture(ctx context.Context, plan cloneCommonCheckpointPlan) error {
	d.captures++
	op, err := d.store.ProjectEnvironmentCloneOperationByID(ctx, plan.AccountID, plan.ProjectID, plan.OperationID)
	if err != nil || len(op.Resources) != 1 || op.Resources[0].Status != "capturing" || op.Resources[0].SourceVersion != cloneCheckpointDigest(plan) {
		d.t.Fatalf("native capture preceded durable dispatch: %+v %v", op.Resources, err)
	}
	d.receipt = cloneCommonCheckpointReceipt{RequestID: plan.OperationID, PlanSHA256: cloneCheckpointDigest(plan),
		CheckpointID: "retained-common-checkpoint", CapturedAt: time.Now().UTC().Truncate(time.Microsecond), CompleteWriterCoverage: true}
	for _, source := range plan.Sources {
		d.receipt.Sources = append(d.receipt.Sources, cloneCommonCheckpointSourceReceipt{Kind: source.Kind, ID: source.ID, CaptureIdentity: "retained-" + source.ID})
	}
	// The coordinator must own its roster independently of the provider.
	plan.Sources[0].ID = "provider-mutated-input"
	if d.loseCapture {
		return objectstorage.ErrUnavailable
	}
	return nil
}

func (d *commonCheckpointTestDriver) Observe(_ context.Context, plan cloneCommonCheckpointPlan) (cloneCommonCheckpointReceipt, error) {
	d.reads++
	if d.receipt.RequestID == "" {
		return cloneCommonCheckpointReceipt{}, objectstorage.ErrNotFound
	}
	if plan.OperationID != d.receipt.RequestID || cloneCheckpointDigest(plan) != d.receipt.PlanSHA256 {
		return cloneCommonCheckpointReceipt{}, state.ErrConflict
	}
	out := d.receipt
	out.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), out.Sources...)
	if d.mutateObservation != nil {
		d.mutateObservation(&out)
	}
	return out, nil
}

func reclaimCommonCheckpointWorker(t *testing.T, f *cloneCoordinatorFixture) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second' where id=$1", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := f.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != f.lease.Operation.ID {
		t.Fatalf("replacement worker: %v", err)
	}
	f.lease = lease
}

func TestPGCloneCommonCheckpointRecoversOriginalCapture(t *testing.T) {
	f, store, provider, buckets := cloneCaptureBarrierFixture(t)
	provider.drained = true
	driver := &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store), loseCapture: true}
	var actual cloneCommonCheckpointReceipt
	var err error
	f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
	if !errors.Is(err, objectstorage.ErrUnavailable) || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) || driver.captures != 1 || driver.reads != 0 {
		t.Fatalf("uncertain capture: %+v %v, calls=%d/%d", actual, err, driver.captures, driver.reads)
	}
	original := driver.receipt
	if len(f.lease.Operation.Resources) != 1 || f.lease.Operation.Resources[0].CapturePoint != "" || f.lease.Operation.Resources[0].Status != "capturing" {
		t.Fatal("uncertain reply selected a point", f.lease.Operation.Resources)
	}
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
	if err != nil || !reflect.DeepEqual(actual, original) || driver.captures != 1 || driver.reads != 1 {
		t.Fatalf("original capture recovery: %+v %v, calls=%d/%d", actual, err, driver.captures, driver.reads)
	}
	resources := append([]state.ProjectEnvironmentCloneResource(nil), f.lease.Operation.Resources...)
	if len(resources) != 2 || resources[0].CapturePoint != original.CapturedAt.Format(time.RFC3339Nano) || resources[1].SourceVersion != cloneCheckpointDigest(original) {
		t.Fatal("original point/proof not retained", resources)
	}
	reclaimCommonCheckpointWorker(t, &f)
	f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
	if err != nil || !reflect.DeepEqual(actual, original) || !reflect.DeepEqual(f.lease.Operation.Resources, resources) || driver.captures != 1 {
		t.Fatalf("receipt replay changed capture: %+v %v", actual, err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); err != nil {
		t.Fatal("successful selection released configuration", err)
	}
	for _, bucket := range buckets {
		if _, err := store.PgStore.BeginObjectBucketMutation(t.Context(), bucket, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
			t.Fatal("successful selection reopened source writes", err)
		}
	}
	if _, err := store.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), f.lease.Operation.AccountID, f.lease.Operation.ProjectID, f.lease.Operation.ID,
		state.CloneOperationCapturing, state.CloneOperationCopying, f.lease.Operation.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatal("private checkpoint became publication strategy", err)
	}
	// Even the same aggregate identity/point cannot rewrite a retained source.
	driver.mutateObservation = func(r *cloneCommonCheckpointReceipt) { r.Sources[0].CaptureIdentity += "-replacement" }
	_, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
	if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) || driver.captures != 1 {
		t.Fatal("receipt rewrite was accepted", actual, err)
	}
}

func TestPGCloneCommonCheckpointRejectsIncompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*cloneCommonCheckpointReceipt)
	}{
		{"coverage", func(r *cloneCommonCheckpointReceipt) { r.CompleteWriterCoverage = false }},
		{"wrong operation", func(r *cloneCommonCheckpointReceipt) { r.RequestID = uuid.NewString() }},
		{"wrong plan", func(r *cloneCommonCheckpointReceipt) { r.PlanSHA256 = "another-plan" }},
		{"missing source", func(r *cloneCommonCheckpointReceipt) { r.Sources = r.Sources[1:] }},
		{"wrong source", func(r *cloneCommonCheckpointReceipt) { r.Sources[0].ID = uuid.NewString() }},
		{"missing identity", func(r *cloneCommonCheckpointReceipt) { r.Sources[0].CaptureIdentity = "" }},
		{"before holds", func(r *cloneCommonCheckpointReceipt) { r.CapturedAt = r.CapturedAt.Add(-time.Hour) }},
		{"future point", func(r *cloneCommonCheckpointReceipt) { r.CapturedAt = r.CapturedAt.Add(time.Hour) }},
		{"duplicate source", func(r *cloneCommonCheckpointReceipt) { r.Sources[1] = r.Sources[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, store, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			driver := &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store), mutateObservation: tc.edit}
			var actual cloneCommonCheckpointReceipt
			var err error
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) || driver.captures != 1 {
				t.Fatal("incomplete evidence accepted", actual, err)
			}
			if f.lease.Operation.Resources[0].CapturePoint != "" || len(f.lease.Operation.Resources) != 1 {
				t.Fatal("invalid evidence committed a point")
			}
			driver.mutateObservation = nil
			reclaimCommonCheckpointWorker(t, &f)
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			if err != nil || driver.captures != 1 || !reflect.DeepEqual(actual, driver.receipt) {
				t.Fatal("failed observation required another capture", err)
			}
		})
	}
}

func TestPGCloneCommonCheckpointWaitsBeforeDispatch(t *testing.T) {
	for _, kind := range []string{"no driver", "busy object", "busy postgres", "unselected postgres", "host admission"} {
		t.Run(kind, func(t *testing.T) {
			f, store, provider, buckets := cloneCaptureBarrierFixture(t)
			provider.drained = true
			driver := &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}
			var captureDriver cloneCommonCheckpointDriver = driver
			switch kind {
			case "no driver":
				captureDriver = nil
			case "busy object":
				if _, err := store.PgStore.BeginObjectBucketMutation(t.Context(), buckets[0], state.ObjectBucketMutationRequest); err != nil {
					t.Fatal(err)
				}
			case "busy postgres":
				provider.drained = false
			case "unselected postgres":
				provider.closeUnselected = 1
			case "host admission":
				f.srv.cloneWorkerAdmission = func(context.Context) error { return errCloneCheckpointUnavailable }
			}
			var err error
			f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, captureDriver)
			want := errCloneCheckpointUnavailable
			if kind == "unselected postgres" {
				want = state.ErrConflict // The existing native catalogue gate rejects this first.
			}
			if !errors.Is(err, want) || driver.captures != 0 || driver.reads != 0 {
				t.Fatalf("unavailable capture dispatched: %v %d/%d", err, driver.captures, driver.reads)
			}
			assertCloneCaptureHasNoPoint(t, f)
		})
	}
}

type commonCheckpointCommitFailureStore struct {
	PgStore               *state.PgStore
	loseFinal, stealFinal bool
	loseStage             string
	next                  state.ProjectEnvironmentCloneLease
}

func (s *commonCheckpointCommitFailureStore) AdvanceProjectEnvironmentCloneCheckpointIntent(ctx context.Context, lease state.ProjectEnvironmentCloneLease, resources []state.ProjectEnvironmentCloneResource) (state.ProjectEnvironmentCloneOperation, error) {
	if resources[0].Status == "captured" && s.stealFinal {
		s.stealFinal = false
		if err := s.PgStore.ReleaseProjectEnvironmentCloneLease(ctx, lease, 0); err != nil {
			return state.ProjectEnvironmentCloneOperation{}, err
		}
		var err error
		s.next, err = s.PgStore.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
		if err != nil {
			return state.ProjectEnvironmentCloneOperation{}, err
		}
	}
	op, err := s.PgStore.AdvanceProjectEnvironmentCloneCheckpointIntent(ctx, lease, resources)
	if err == nil && resources[0].Status == s.loseStage {
		s.loseStage = ""
		return state.ProjectEnvironmentCloneOperation{}, objectstorage.ErrUnavailable
	}
	if err == nil && resources[0].Status == "captured" && s.loseFinal {
		s.loseFinal = false
		return state.ProjectEnvironmentCloneOperation{}, objectstorage.ErrUnavailable
	}
	return op, err
}

func TestPGCloneCommonCheckpointLostIntentRepliesNeverInventDispatch(t *testing.T) {
	for _, stage := range []string{"planned", "capturing"} {
		t.Run(stage, func(t *testing.T) {
			f, base, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			store := &commonCheckpointCommitFailureStore{PgStore: base.PgStore, loseStage: stage}
			f.srv.store = statetest.NewPg(store)
			driver := &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}
			var actual cloneCommonCheckpointReceipt
			var err error
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			if !errors.Is(err, objectstorage.ErrUnavailable) || driver.captures != 0 || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) {
				t.Fatal("unknown intent commit dispatched capture", actual, err)
			}
			reclaimCommonCheckpointWorker(t, &f)
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			if stage == "planned" {
				if err != nil || driver.captures != 1 {
					t.Fatal("retained unclaimed intent could not claim its first dispatch", err)
				}
			} else if !errors.Is(err, objectstorage.ErrNotFound) || driver.captures != 0 || driver.reads != 1 || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) {
				t.Fatal("missing evidence invented retry authority", actual, err)
			}
		})
	}
}

func TestPGCloneCommonCheckpointFencesCommitAndRecoversLostReply(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "handoff before commit", true: "lost commit reply"}[lost], func(t *testing.T) {
			f, base, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			store := &commonCheckpointCommitFailureStore{PgStore: base.PgStore, loseFinal: lost, stealFinal: !lost}
			f.srv.store = statetest.NewPg(store)
			driver := &commonCheckpointTestDriver{t: t, store: statetest.NewPg(store)}
			var actual cloneCommonCheckpointReceipt
			var err error
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			want := state.ErrConflict
			if lost {
				want = objectstorage.ErrUnavailable
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(actual, cloneCommonCheckpointReceipt{}) {
				t.Fatal("unknown/lost authority produced checkpoint evidence", actual, err)
			}
			if lost {
				reclaimCommonCheckpointWorker(t, &f)
			} else {
				f.lease = store.next
				if len(f.lease.Operation.Resources) != 1 || f.lease.Operation.Resources[0].Status != "capturing" {
					t.Fatal("stale worker committed after takeover")
				}
			}
			f.lease, actual, err = f.srv.prepareProjectEnvironmentCloneCommonCheckpoint(t.Context(), f.lease, driver)
			if err != nil || driver.captures != 1 || !reflect.DeepEqual(actual, driver.receipt) {
				t.Fatal("handoff repeated/replaced checkpoint", actual, err)
			}
		})
	}
}
