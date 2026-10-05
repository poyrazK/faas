//go:build !no_pg

// adr: 585
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCapturedCloneConfigurationIsAtomicAndRecoverable(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "canonical-capture@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "canonical"})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	for _, scope := range []string{"production", "default"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "canonical-" + scope, WorkloadName: "canonical-" + scope, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:canonical"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetDeploymentRootfs(ctx, deployment.ID, "/captured.ext4", "layers/canonical-"+deployment.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, scope, "CAPTURED", "customer-data-"+scope); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertAppSecretWithKidAndValueHashInScope(ctx, a.ID, app.ID, scope, "TOKEN", "age1-test", "1111111111111111", []byte("sealed-private")); err != nil {
			t.Fatal(err)
		}
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: a.ID, ProjectID: p.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values}); err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "atomic"}
	sourceEnvironment, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	flagScope := state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: sourceEnvironment.ID}
	sourceFlags, customerID := cloneFlagFixture(t, s, flagScope)
	op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request)
	if err != nil || op.Status != state.CloneOperationPending || len(op.SourceRevisionHash) != 64 || op.Revision != 1 {
		t.Fatalf("atomic capture: %+v, %v", op, err)
	}
	views, err := s.ProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID)
	if err != nil || len(views) != 2 {
		t.Fatalf("creation did not persist complete workload roster: %v", err)
	}
	var root []byte
	if err := pool.QueryRow(ctx, "select configuration from project_environment_clone_configuration_captures where operation_id=$1", op.ID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(root), "customer-data") || strings.Contains(string(root), "sealed-private") || strings.Contains(string(root), "age1-test") || strings.Contains(string(root), "region") || strings.Contains(string(root), "captured-description") || strings.Contains(string(root), customerID) {
		t.Fatal("configuration root exposed values or encrypted content")
	}
	var rootFields map[string]json.RawMessage
	if err := json.Unmarshal(root, &rootFields); err != nil || len(rootFields["feature_flags_hash"]) != 66 {
		t.Fatalf("root omitted flag snapshot identity: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("capture created target environment: %v", err)
	}
	same := request
	same.TargetEnvironment, same.IdempotencyKey = "stage-same", "same"
	if equivalent, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, same); err != nil || equivalent.ID == op.ID || equivalent.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("equivalent source changed revision with operation/target identity: %+v, %v", equivalent, err)
	}
	sourceFlags.Flags[0].Description = "changed-source-flags"
	if _, err := s.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: flagScope, ExpectedVersion: sourceFlags.Version, Config: sourceFlags.Config, Actor: "developer"}); err != nil {
		t.Fatal(err)
	}
	flagsOnly := request
	flagsOnly.TargetEnvironment, flagsOnly.IdempotencyKey = "flags-only", "flags-only"
	if changed, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, flagsOnly); err != nil || changed.SourceRevisionHash == op.SourceRevisionHash {
		t.Fatalf("flag-only edit did not change captured revision: %v", err)
	}
	for i, app := range apps {
		scope := []string{"production", "default"}[i]
		if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, scope, "CAPTURED", "later-live-value"); err != nil {
			t.Fatal(err)
		}
	}
	if recovered, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || recovered.ID != op.ID || recovered.SourceRevisionHash != op.SourceRevisionHash || recovered.Revision != op.Revision {
		t.Fatalf("retry recaptured changed production: %+v, %v", recovered, err)
	}
	other := request
	other.TargetEnvironment, other.IdempotencyKey = "stage-new", "new"
	newer, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, other)
	if err != nil || newer.SourceRevisionHash == op.SourceRevisionHash {
		t.Fatalf("new capture did not include changed configuration: %+v, %v", newer, err)
	}
	wrong := request
	wrong.TargetEnvironment = "other"
	if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("idempotency key adopted a different target: %v", err)
	}
	lease, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != op.ID {
		t.Fatalf("claim atomic capture: %+v, %v", lease, err)
	}
	capture, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, lease)
	if err != nil || capture.Version != 1 || capture.WorkloadCount != 2 || capture.Hash != op.SourceRevisionHash {
		t.Fatalf("leased root: %+v, %v", capture, err)
	}
	stale := lease
	stale.Token = uuid.NewString()
	if _, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong owner read root: %v", err)
	}
	op, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	if captured, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); err != nil || !reflect.DeepEqual(captured, views) {
		t.Fatalf("worker recaptured source after atomic creation: %v", err)
	}
	for _, fault := range []struct {
		name, change, restore string
		value                 any
	}{
		{"root_content", "update project_environment_clone_configuration_captures set configuration='{}' where operation_id=$1", "update project_environment_clone_configuration_captures set configuration=$2 where operation_id=$1", root},
		{"root_hash", "update project_environment_clone_configuration_captures set configuration_hash=repeat('f',64) where operation_id=$1", "update project_environment_clone_configuration_captures set configuration_hash=$2 where operation_id=$1", op.SourceRevisionHash},
		{"operation_hash", "update project_environment_clone_operations set source_revision_hash=repeat('f',64) where id=$1", "update project_environment_clone_operations set source_revision_hash=$2 where id=$1", op.SourceRevisionHash},
	} {
		t.Run(fault.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, fault.change, op.ID); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, fault.restore, op.ID, fault.value); err != nil {
					t.Error(err)
				}
			}()
			if _, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("changed root accepted: %v", err)
			}
			if _, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("binding worker bypassed changed root: %v", err)
			}
			if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("changed root recaptured source: %v", err)
			}
		})
	}
	if _, err := pool.Exec(ctx, "delete from project_environment_clone_configuration_captures where operation_id=$1", op.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing root downgraded to legacy capture: %v", err)
	}
	if _, err := pool.Exec(ctx, "insert into project_environment_clone_configuration_captures(operation_id,version,configuration_hash,configuration) values($1,1,$2,$3)", op.ID, op.SourceRevisionHash, root); err != nil {
		t.Fatal(err)
	}
	t.Run("artifact_pin_failure_rolls_back_created_rows", func(t *testing.T) {
		var key string
		if err := pool.QueryRow(ctx, "select rootfs_key from deployments where id=$1", views[0].SourceDeploymentID).Scan(&key); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "update layer_artifact_retention set state='deleted',deletion_id=$2,delete_requested_at=clock_timestamp(),deleted_at=clock_timestamp() where storage_key=$1", key, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := pool.Exec(ctx, "update layer_artifact_retention set state='retained',deletion_id=null,delete_requested_at=null,deleted_at=null where storage_key=$1", key); err != nil {
				t.Error(err)
			}
		}()
		failed := request
		failed.TargetEnvironment, failed.IdempotencyKey = "retired", "retired"
		if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, failed); !errors.Is(err, state.ErrLayerArtifactRetired) {
			t.Fatalf("retired artifact entered atomic capture: %v", err)
		}
		if _, err := s.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, a.ID, p.ID, failed.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("artifact pin failure retained operation/workload rows: %v", err)
		}
	})
	// A newly added undeployed workload prevents a new capture, but cannot
	// alter the original root or leave a partial operation/name reservation.
	if _, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "undeployed", WorkloadName: "undeployed", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	failed := request
	failed.TargetEnvironment, failed.IdempotencyKey = "failed", "failed"
	if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, failed); err == nil {
		t.Fatal("captured project with missing runnable artifact")
	}
	if _, err := s.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, a.ID, p.ID, "failed"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("capture failure left operation: %v", err)
	}
	if replay, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || replay.ID != op.ID || replay.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("completed capture replay read newly added workload: %v", err)
	}
	if _, err := s.ProjectEnvironmentCloneConfigurationForLease(context.Background(), stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale root reader became valid: %v", err)
	}
	legacy := state.ProjectEnvironmentCloneOperation{AccountID: a.ID, ProjectID: p.ID, SourceEnvironment: "production", TargetEnvironment: "legacy",
		IdempotencyKey: "legacy", SourceRevisionHash: strings.Repeat("a", 64)}
	if _, err := s.CreateProjectEnvironmentCloneOperation(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	legacyRequest := request
	legacyRequest.TargetEnvironment, legacyRequest.IdempotencyKey = "legacy", "legacy"
	if _, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, legacyRequest); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("atomic creation adopted an uncaptured legacy operation: %v", err)
	}
	if _, err := pool.Exec(ctx, "delete from project_environment_clone_workloads where operation_id=$1 and app_id=$2", op.ID, views[0].AppID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProjectEnvironmentCloneConfigurationForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("root accepted incomplete captured roster: %v", err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("incomplete captured roster was regenerated from live state: %v", err)
	}
}
