package acceptance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type operationPinFixture interface {
	operationFixtureStore
	state.RevisionPinStore
	AppByID(context.Context, string) (state.App, error)
	MarkDeploymentLive(context.Context, string) error
	DeploymentByID(context.Context, string) (state.Deployment, error)
}

func testOperationDeploymentPin(t *testing.T, s operationPinFixture) {
	ctx, acct, app, def, alice, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "retained-code", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := s.DeploymentByID(ctx, def.DeploymentID)
	if err != nil || retained.Status != state.DeployLive || retained.TrafficPercent != 0 {
		t.Fatalf("operation code retired by rollout: %+v %v", retained, err)
	}
	if _, err := s.ResolveRevisionPin(ctx, app.ID, def.Scope, def.DeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("private operation retention enabled public revision pins: %v", err)
	}
	inv, err := s.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	_, version, err := state.ResolveInvocationVersion(ctx, s, inv)
	if err != nil || version.DeploymentID != def.DeploymentID {
		t.Fatalf("queued operation moved to new code: %+v %v", version, err)
	}
	if n, err := s.ExpireRevisionPins(ctx); err != nil || n != 0 {
		t.Fatalf("operation pin was expired: %d %v", n, err)
	}
}

func TestMemOperationDeploymentPin(t *testing.T) { testOperationDeploymentPin(t, state.NewMemStore()) }
func TestPgOperationDeploymentPin(t *testing.T)  { s, _ := pgStore(t); testOperationDeploymentPin(t, s) }
