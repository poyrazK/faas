// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationFixtureStore interface {
	state.OperationStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
	AppByID(context.Context, string) (state.App, error)
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	CreatePlatformTenant(context.Context, string, string, string, int) (state.PlatformTenant, bool, error)
	SetPlatformTenantStatus(context.Context, string, string, string) (state.PlatformTenant, error)
	InvocationByID(context.Context, string) (state.Invocation, error)
}

func operationSpec() api.OperationDefinitionSpec {
	return api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
		InputSchema:    []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
		OutputSchema:   []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`),
		ProgressStages: []string{"generating", "uploading"}}
}

func operationFixture(t *testing.T, s operationFixtureStore) (context.Context, state.Account, state.App, state.OperationDefinition, state.PlatformTenant, state.PlatformTenant) {
	t.Helper()
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "operations@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "operations-test", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operations", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: operationSpec()}})
	if err != nil {
		t.Fatal(err)
	}
	alice, _, err := s.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := s.CreatePlatformTenant(ctx, acct.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, acct, app, def, alice, bob
}

func testOperationAdmission(t *testing.T, s operationFixtureStore) {
	t.Helper()
	ctx, acct, app, def, alice, bob := operationFixture(t, s)
	admission := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "same-request", Input: []byte(`{"count":1}`)}
	var wg sync.WaitGroup
	var created atomic.Int64
	ids := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := admission
			if i%2 == 0 {
				r.Input = []byte(`{"count":1.0}`)
			}
			op, fresh, err := s.AdmitOperation(ctx, r)
			if err != nil {
				t.Errorf("concurrent admission: %v", err)
				return
			}
			if fresh {
				created.Add(1)
			}
			ids <- op.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	if created.Load() != 1 {
		t.Fatalf("created %d operations", created.Load())
	}
	var id string
	for next := range ids {
		if id == "" {
			id = next
		}
		if next != id {
			t.Fatal("concurrent submissions got different operation identities")
		}
	}
	op, err := s.OperationByID(ctx, acct.ID, alice.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil || inv.PlatformTenantID != alice.ID || inv.Path != "/exports" {
		t.Fatalf("atomic execution association: %+v %v", inv, err)
	}
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil || headers[api.RevisionHeader] != def.DeploymentID {
		t.Fatal("execution is not pinned to its definition deployment")
	}
	conflict := admission
	conflict.Input = []byte(`{"count":2}`)
	if _, _, err := s.AdmitOperation(ctx, conflict); !errors.Is(err, state.ErrOperationInputConflict) {
		t.Fatalf("changed input: %v", err)
	}
	for _, raw := range []string{`{"count":0}`, `{"count":1,"tenant":"bob"}`, `{"count":1,"count":2}`} {
		invalid := admission
		invalid.IdempotencyKey = "invalid"
		invalid.Input = []byte(raw)
		if _, _, err := s.AdmitOperation(ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid input %s: %v", raw, err)
		}
	}
	other := admission
	other.PlatformTenantID = bob.ID
	bobOp, fresh, err := s.AdmitOperation(ctx, other)
	if err != nil || !fresh || bobOp.ID == id {
		t.Fatalf("tenant key scope: %+v %v", bobOp, err)
	}
	if _, err := s.OperationByID(ctx, acct.ID, bob.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign status exposed: %v", err)
	}
	if _, err := s.OperationEvents(ctx, acct.ID, bob.ID, id, 0, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign stream exposed: %v", err)
	}
	page, err := s.OperationEvents(ctx, acct.ID, alice.ID, id, 0, 10)
	if err != nil || len(page.Events) != 1 || page.Events[0].Sequence != 1 || page.ResyncRequired {
		t.Fatalf("initial events: %+v %v", page, err)
	}
	page.Events[0].Data[0] = '!'
	page, err = s.OperationEvents(ctx, acct.ID, alice.ID, id, 0, 10)
	if err != nil || page.Events[0].Data[0] != '{' {
		t.Fatal("caller changed durable event")
	}
	if _, err := s.SetPlatformTenantStatus(ctx, acct.ID, alice.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if duplicate, fresh, err := s.AdmitOperation(ctx, admission); err != nil || fresh || duplicate.ID != id {
		t.Fatalf("existing acceptance lost during suspension: %v", err)
	}
	newWork := admission
	newWork.IdempotencyKey = "new-work"
	if _, _, err := s.AdmitOperation(ctx, newWork); !errors.Is(err, state.ErrPlatformTenantSuspended) {
		t.Fatalf("suspended tenant admitted new work: %v", err)
	}
	if _, err := s.SetPlatformTenantStatus(ctx, acct.ID, alice.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	newDep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new-operations", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, newDep.ID); err != nil {
		t.Fatal(err)
	}
	changed := def
	changed.ID = ""
	changed.DeploymentID = newDep.ID
	changed.Spec.InputSchema = []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":2}},"additionalProperties":false}`)
	changed, err = s.PutOperationDefinition(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	newDefinitionRetry := admission
	newDefinitionRetry.DefinitionID = changed.ID
	previous, fresh, err := s.AdmitOperation(ctx, newDefinitionRetry)
	if err != nil || fresh || previous.ID != id || previous.DefinitionID != def.ID {
		t.Fatalf("deploy changed an existing receipt: %+v %v", previous, err)
	}
	newDefinitionRetry.IdempotencyKey = "new-definition"
	if _, _, err := s.AdmitOperation(ctx, newDefinitionRetry); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("new submission skipped new schema: %v", err)
	}
	changed = def
	changed.Spec.Path = "/changed"
	if _, err := s.PutOperationDefinition(ctx, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mutated an immutable definition: %v", err)
	}
}

func TestMemOperationAdmission(t *testing.T) { testOperationAdmission(t, state.NewMemStore()) }

func TestPgOperationAdmission(t *testing.T) { s, _ := pgStore(t); testOperationAdmission(t, s) }

func pgStore(t *testing.T) (*state.PgStore, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), ctx
}
