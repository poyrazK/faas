//go:build !no_pg

// adr: 567
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func pendingCloneConfigurationFixture(t *testing.T) cloneCoordinatorFixture {
	t.Helper()
	f := newCloneCoordinatorFixture(t, false)
	ctx := t.Context()
	op := f.lease.Operation
	if err := f.store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{
		AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: "production", TargetEnvironment: "automatic", IdempotencyKey: "automatic"})
	if err != nil {
		t.Fatal(err)
	}
	f.lease = state.ProjectEnvironmentCloneLease{Operation: pending}
	return f
}

func TestPGCloneCoordinatorDrivesFrozenConfigurationCapture(t *testing.T) {
	for _, mode := range []string{"pending", "capturing_empty", "lost_capture_ack"} {
		t.Run(mode, func(t *testing.T) {
			f := pendingCloneConfigurationFixture(t)
			ctx := t.Context()
			op := f.lease.Operation
			if mode == "capturing_empty" {
				updated, err := f.store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				f.lease.Operation, op = updated, updated
			}
			for _, app := range f.apps {
				if err := f.store.UpsertAppEnvInScope(ctx, op.AccountID, app.ID, "production", "CAPTURED", "later-source-edit"); err != nil {
					t.Fatal(err)
				}
			}
			f.store.loseCaptureAck = mode == "lost_capture_ack"
			err := f.srv.processNextProjectEnvironmentClone(ctx, f.store)
			if mode == "lost_capture_ack" {
				if err == nil {
					t.Fatal("lost capture acknowledgement was hidden")
				}
				capturing, readErr := f.store.ProjectEnvironmentCloneOperationByID(ctx, op.AccountID, op.ProjectID, op.ID)
				if readErr != nil || capturing.Status != state.CloneOperationCapturing || len(capturing.Resources) != 14 {
					t.Fatalf("lost durable capture: %+v, %v", capturing, readErr)
				}
				if _, err := f.store.ProjectEnvironmentBySlug(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("uncertain capture acknowledgement materialized a target")
				}
				if _, err := f.pool.Exec(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()-interval '1 second', next_attempt_at=clock_timestamp() where id=$1", op.ID); err != nil {
					t.Fatal(err)
				}
				err = f.srv.processNextProjectEnvironmentClone(ctx, f.store)
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err := f.store.ProjectEnvironmentCloneOperationByID(ctx, op.AccountID, op.ProjectID, op.ID)
			if err != nil || current.Status != state.CloneOperationCopying || current.SourceRevisionHash != op.SourceRevisionHash || current.TargetReleaseSetID != "" || len(current.Resources) != 14 {
				t.Fatalf("automatic capture did not preserve its source: %+v, %v", current, err)
			}
			for _, resource := range current.Resources {
				if resource.CapturePoint != "" || resource.Kind == "managed_postgres" || resource.Kind == "object_storage" {
					t.Fatalf("configuration capture fabricated data proof: %+v", resource)
				}
			}
			for _, view := range f.targets(t) {
				deployment, err := f.store.DeploymentByID(ctx, view.TargetDeploymentID)
				if err != nil || deployment.ImageDigest != "sha256:captured" || view.TargetDeploymentID == view.SourceDeploymentID {
					t.Fatalf("automatic clone changed or omitted artifact: %+v, %v", view, err)
				}
				values, err := f.store.ListAppEnvInScope(ctx, op.AccountID, view.AppID, op.TargetEnvironment)
				if err != nil || len(values) != 1 || values[0].Value != "original" {
					t.Fatalf("automatic capture reread source values: %+v, %v", values, err)
				}
			}
		})
	}
}

func TestPGCloneCoordinatorRejectsUnrecognizedCaptureProgress(t *testing.T) {
	f := pendingCloneConfigurationFixture(t)
	ctx := t.Context()
	op := f.lease.Operation
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "object_storage", Name: "foreign-bucket", SourceID: "foreign-bucket", Status: "captured"}}
	if _, err := f.store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCapturing, op.Revision, resources, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.srv.processNextProjectEnvironmentClone(ctx, f.store); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted progress outside the frozen catalogue: %v", err)
	}
	if _, err := f.store.ProjectEnvironmentBySlug(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("inconsistent capture created a target")
	}
}

func TestPGCloneStatusAdvanceRejectsLeaseExpiredBehindRowLock(t *testing.T) {
	f := newCloneCoordinatorFixture(t, false)
	ctx := t.Context()
	op := f.lease.Operation
	if _, err := f.pool.Exec(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '200 milliseconds' where id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, "select id from project_environment_clone_operations where id=$1 for update", op.ID); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.store.AdvanceProjectEnvironmentCloneOperation(workerCtx, op.AccountID, op.ProjectID, op.ID, op.Status, op.Status, op.Revision, op.Resources, "")
		done <- err
	}()
	waited := false
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		if err := f.pool.QueryRow(ctx, `select lease_until<=clock_timestamp() and exists(
			select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock'
			and query like '%project_environment_clone_operations%')
			from project_environment_clone_operations where id=$1`, op.ID).Scan(&waited); err != nil {
			t.Fatal(err)
		}
		if waited {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waited {
		t.Fatal("did not observe the status writer waiting past its server-clock lease expiry")
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired status writer advanced after row lock: %v", err)
	}
	current, err := f.store.ProjectEnvironmentCloneOperationByID(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil || current.Revision != op.Revision || current.Status != op.Status {
		t.Fatalf("rejected writer changed operation: %+v, %v", current, err)
	}
}
