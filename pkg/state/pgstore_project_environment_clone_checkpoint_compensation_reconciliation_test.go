//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCheckpointCompensationReconciliationRequiresTerminalProofs(t *testing.T) {
	for _, phase := range []string{"planned", "unknown", "captured", "adopted", "adopted-mixed"} {
		t.Run(phase, func(t *testing.T) {
			s, pool, l := pgCaptureRetirementFixture(t, phase)
			ctx := t.Context()
			assertNoReport := func(lease state.ProjectEnvironmentCloneLease, want error) {
				t.Helper()
				report, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, lease)
				if !errors.Is(err, want) || !reflect.DeepEqual(report, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
					t.Fatal("invalid reconciliation returned evidence", report, err)
				}
			}
			assertNoReport(l, state.ErrConflict)
			if phase == "adopted-mixed" {
				source, err := s.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(ctx, l)
				if err != nil {
					t.Fatal(err)
				}
				for _, original := range source.Plan.Capture.Sources {
					if original.Kind == "object_storage" {
						if err = s.ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(ctx, l, original.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if _, err := s.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil {
				t.Fatal(err)
			}
			assertNoReport(l, state.ErrConflict)
			in, dispatch, err := s.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
			if err != nil || !dispatch {
				t.Fatal(err)
			}
			assertNoReport(l, state.ErrConflict)
			if err = s.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, pgCaptureRetirementProof(t, in)); err != nil {
				t.Fatal(err)
			}
			before, err := s.ProjectEnvironmentCloneOperationByID(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			report, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, l)
			if err != nil {
				t.Fatal("reconcile terminal data", err)
			}
			saved, err := s.ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(ctx, l)
			if err != nil {
				t.Fatal(err)
			}
			if report.OperationID != l.Operation.ID || report.Revision != l.Operation.Revision || report.ResourcesSHA256 != captureRetirementDigest(t, l.Operation.Resources) || report.SourceCompensationSHA256 != saved.Plan.SourceCompensation.ObservationSHA256 || report.CaptureRetirementSHA256 != saved.ObservationSHA256 || len(report.Blockers) == 0 {
				t.Fatal("incomplete proof or configuration blockers", report)
			}
			replay, err := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, l)
			if err != nil || !reflect.DeepEqual(report, replay) {
				t.Fatal("reconciliation changed on replay", err)
			}
			after, err := s.ProjectEnvironmentCloneOperationByID(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("reconciliation mutated operation", err)
			}
			// Even an authenticated report cannot bypass the generic terminal guard.
			resources := append([]state.ProjectEnvironmentCloneResource(nil), l.Operation.Resources...)
			for i := range resources {
				resources[i].Status = "compensated"
			}
			if _, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID, l.Operation.Status, state.CloneOperationCompensated, l.Operation.Revision, resources, ""); !errors.Is(err, state.ErrConflict) {
				t.Fatal("report opened generic terminal gate", err)
			}
			old := l
			if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			assertNoReport(old, state.ErrConflict)
			replay, err = s.ReconcileProjectEnvironmentCloneCheckpointCompensation(ctx, l)
			expected := report
			expected.Revision = l.Operation.Revision
			if err != nil || replay.Revision <= report.Revision || !reflect.DeepEqual(expected, replay) {
				t.Fatal("handoff lost proof binding to the current revision", report, replay, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if out, e := s.ReconcileProjectEnvironmentCloneCheckpointCompensation(cancelled, l); e == nil || !reflect.DeepEqual(out, state.ProjectEnvironmentCloneCheckpointCompensationReconciliation{}) {
				t.Fatal("cancelled worker returned evidence", out, e)
			}
			if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_checkpoint_capture_retirements SET observation=jsonb_set(observation,'{CapturePermanentlyFenced}','false'::jsonb) WHERE operation_id=$1", l.Operation.ID); err != nil {
				t.Fatal(err)
			}
			assertNoReport(l, state.ErrConflict)
		})
	}
}
