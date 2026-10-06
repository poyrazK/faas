// adr: 521
package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func memOperationCodeFixture(t *testing.T, service bool) (*MemStore, App, Deployment, PlatformTenant) {
	t.Helper()
	s := NewMemStore()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "code-retention@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	manifest := AppManifest{RevisionPinTTLSeconds: 3600}
	if service {
		manifest.ExecutionMode = api.ExecutionModeService
		manifest.ServiceReplicas = &ServiceReplicas{Min: 1, Max: 3, Desired: 2}
	}
	app, err := s.CreateApp(ctx, App{AccountID: acct.ID, Slug: "code-retention", Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	return s, app, dep, tenant
}

func admitMemOperationCode(t *testing.T, s *MemStore, app App, dep Deployment, tenant PlatformTenant, releaseIDs ...string) Operation {
	t.Helper()
	release := ""
	if len(releaseIDs) > 0 {
		release = releaseIDs[0]
	}
	def, err := s.PutOperationDefinition(t.Context(), OperationDefinition{AccountID: app.AccountID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, ReleaseID: release, Spec: api.OperationDefinitionSpec{
			Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
			InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(t.Context(), OperationAdmission{AccountID: app.AccountID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: dep.ID, Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func expireMemOperationCode(t *testing.T, s *MemStore, op Operation) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.operationData.operations[op.ID]
	current.ExpiresAt = time.Now().Add(-time.Hour)
	s.operationData.operations[op.ID] = current
	if _, exists := s.revisionPins[op.DeploymentID]; exists {
		s.revisionPins[op.DeploymentID] = current.ExpiresAt
	}
	s.operationCodePins[op.DeploymentID] = current.ExpiresAt
}

func requireMemRetainedCode(t *testing.T, s *MemStore, op Operation, traffic int) {
	t.Helper()
	dep, err := s.DeploymentByID(t.Context(), op.DeploymentID)
	if err != nil || dep.Status != DeployLive || dep.TrafficPercent != traffic {
		t.Fatalf("operation code projection: %+v %v, want live/%d", dep, err, traffic)
	}
	if traffic == 0 {
		if _, err := s.ResolveRevisionPin(t.Context(), op.AppID, op.Scope, op.DeploymentID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("private operation extended expired public revision pin: %v", err)
		}
	}
	inv, err := s.InvocationByID(t.Context(), op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	_, version, err := ResolveInvocationVersion(t.Context(), s, inv)
	if err != nil || version.DeploymentID != op.DeploymentID {
		t.Fatalf("operation lost original code: %+v %v", version, err)
	}
}

func TestMemOperationCodeRetentionPastTimestamp(t *testing.T) {
	s, app, old, tenant := memOperationCodeFixture(t, false)
	op := admitMemOperationCode(t, s, app, old, tenant)
	expireMemOperationCode(t, s, op)
	next, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	expireMemOperationCode(t, s, op)
	requireMemRetainedCode(t, s, op, 0)
	if count, err := s.ExpireRevisionPins(t.Context()); err != nil || count != 0 {
		t.Fatalf("active code swept: %d %v", count, err)
	}
	settled, err := s.CancelOperation(t.Context(), app.AccountID, tenant.ID, op.ID, op.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := s.ExpireRevisionPins(t.Context()); err != nil || count != 0 {
		t.Fatalf("settled result window lost code: %d %v", count, err)
	}
	expireMemOperationCode(t, s, settled)
	if count, err := s.ExpireRevisionPins(t.Context()); err != nil || count != 1 {
		t.Fatalf("expired settled operation retained code: %d %v", count, err)
	}
}

func TestMemOperationCodeSurvivesServiceHandoffs(t *testing.T) {
	for _, action := range []string{"promote", "abort"} {
		t.Run(action, func(t *testing.T) {
			s, app, stable, tenant := memOperationCodeFixture(t, true)
			stableOp := admitMemOperationCode(t, s, app, stable, tenant)
			now := time.Now().UTC()
			next, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, ImageDigest: "sha256:next", RolloutState: "rolling_out", RolloutStartedAt: &now})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentLive(t.Context(), next.ID); err != nil {
				t.Fatal(err)
			}
			nextOp := admitMemOperationCode(t, s, app, next, tenant)
			expireMemOperationCode(t, s, stableOp)
			expireMemOperationCode(t, s, nextOp)
			if action == "promote" {
				if _, err := s.FinalizeServiceRollout(t.Context(), next.ID); err != nil {
					t.Fatal(err)
				}
				expireMemOperationCode(t, s, stableOp)
				requireMemRetainedCode(t, s, stableOp, 0)
				requireMemRetainedCode(t, s, nextOp, 100)
			} else {
				if _, err := s.AbortServiceRollout(t.Context(), next.ID, "readiness failed"); err != nil {
					t.Fatal(err)
				}
				requireMemRetainedCode(t, s, stableOp, 100)
				expireMemOperationCode(t, s, nextOp)
				requireMemRetainedCode(t, s, nextOp, 0)
			}
			if count, err := s.ExpireRevisionPins(t.Context()); err != nil || count != 0 {
				t.Fatalf("handoff lost active code: %d %v", count, err)
			}
		})
	}
}

func TestMemOperationCodeSurvivesAutoRollback(t *testing.T) {
	s, app, old, tenant := memOperationCodeFixture(t, false)
	oldOp := admitMemOperationCode(t, s, app, old, tenant)
	next, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	nextOp := admitMemOperationCode(t, s, app, next, tenant)
	expireMemOperationCode(t, s, oldOp)
	expireMemOperationCode(t, s, nextOp)
	if target, err := s.LatestSupersededDeployment(t.Context(), app.ID); err != nil || target.ID != old.ID {
		t.Fatalf("retained old revision cannot be selected: %+v %v", target, err)
	}
	if id, err := s.AutoRollbackDeploymentsTx(t.Context(), app.ID, next.ID); err != nil || id != old.ID {
		t.Fatalf("retained old revision cannot be restored: %s %v", id, err)
	}
	requireMemRetainedCode(t, s, oldOp, 100)
	requireMemRetainedCode(t, s, nextOp, 0)
}

func TestMemOperationRetainsWholeReleaseAcrossLongWait(t *testing.T) {
	s, owner, _, tenant := memOperationCodeFixture(t, false)
	ctx := t.Context()
	project, err := s.CreateProject(ctx, Project{AccountID: owner.AccountID, Slug: "operation-graph"})
	if err != nil {
		t.Fatal(err)
	}
	var apps []App
	var old []ProjectReleaseMember
	for _, name := range []string{"origin", "target"} {
		app, err := s.CreateApp(ctx, App{AccountID: owner.AccountID, ProjectID: project.ID, Slug: name, WorkloadName: name,
			Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		old = append(old, ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
	}
	first, err := s.PublishProjectReleaseSet(ctx, owner.AccountID, project.ID, "production", 1800, old)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := s.DeploymentByID(ctx, old[0].DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	op := admitMemOperationCode(t, s, apps[0], origin, tenant, first.ID)
	var next []ProjectReleaseMember
	for _, app := range apps {
		dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: "production", ImageDigest: "sha256:next"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		next = append(next, ProjectReleaseMember{AppID: app.ID, DeploymentID: dep.ID})
	}
	if _, err := s.PublishProjectReleaseSet(ctx, owner.AccountID, project.ID, "production", 1800, next); err != nil {
		t.Fatal(err)
	}
	expireMemOperationCode(t, s, op)
	s.mu.Lock()
	past := time.Now().Add(-time.Hour)
	release := s.projectReleaseSets[first.ID]
	release.ExpiresAt = &past
	s.projectReleaseSets[first.ID] = release
	s.revisionPins[old[1].DeploymentID] = past
	s.operationCodePins[old[1].DeploymentID] = past
	s.mu.Unlock()
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 0 {
		t.Fatalf("active operation's release graph swept: %d %v", count, err)
	}
	requireMemRetainedCode(t, s, op, 0)
	if _, _, err := s.ResolveProjectRelease(ctx, apps[1].ID, "production", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private retention extended an expired public release selector: %v", err)
	}
	retry := OperationAdmission{AccountID: owner.AccountID, DefinitionID: op.DefinitionID,
		PlatformTenantID: tenant.ID, IdempotencyKey: origin.ID, Input: []byte(`{}`)}
	if repeated, fresh, err := s.AdmitOperation(ctx, retry); err != nil || fresh || repeated.ID != op.ID {
		t.Fatalf("public expiry lost an existing operation receipt: %+v %v %v", repeated, fresh, err)
	}
	retry.IdempotencyKey = "fresh-expired-private-graph"
	if _, _, err := s.AdmitOperation(ctx, retry); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private code retention granted a fresh submission: %v", err)
	}
	if release, dep, err := s.ResolveServiceRelease(ctx, apps[0].ID, old[0].DeploymentID, apps[1].ID, first.ID); err != nil || release != first.ID || dep != old[1].DeploymentID {
		t.Fatalf("operation lost original service graph: %s/%s %v", release, dep, err)
	}
	// A mismatched private deployment reference must retain neither the
	// originating code nor the graph selected by its JSON release pointer.
	s.mu.Lock()
	broken := s.operationData.operations[op.ID]
	broken.DeploymentID = next[0].DeploymentID
	s.operationData.operations[op.ID] = broken
	s.mu.Unlock()
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 2 {
		t.Fatalf("unowned operation graph retained code: %d %v", count, err)
	}
}
