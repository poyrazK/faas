package acceptance_test

// ADR-521: grace-period restore preserves operation identity; permanent owner
// deletion purges the customer projection, definition and private ledgers.
import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

type operationDeletionFixture interface {
	operationFixtureStore
	DeleteApp(context.Context, string) error
	RestoreApp(context.Context, string, api.Limits) (state.App, error)
	UpdateAccountStatus(context.Context, string, state.AccountStatus) error
	DeleteAccount(context.Context, string) error
}

func testOperationOwnerDeletion(t *testing.T, s operationDeletionFixture) {
	ctx, acct, app, def, alice, _ := operationFixture(t, s)
	req := state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, IdempotencyKey: "restore-retains-identity", Input: []byte(`{"count":1}`)}
	op, _, err := s.AdmitOperation(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreApp(ctx, app.ID, api.MustLimitsFor(acct.Plan)); err != nil {
		t.Fatal(err)
	}
	restored, err := s.OperationByID(ctx, acct.ID, alice.ID, op.ID)
	if err != nil || restored.ID != op.ID {
		t.Fatalf("app restore discarded work: %+v, %v", restored, err)
	}
	replay, fresh, err := s.AdmitOperation(ctx, req)
	if err != nil || fresh || replay.ID != op.ID {
		t.Fatalf("restore lost submission receipt: %+v, %v", replay, err)
	}
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountDeletedPending); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperationDefinitionByID(ctx, acct.ID, def.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("definition survived permanent owner deletion: %v", err)
	}
	if _, err := s.OperationByID(ctx, acct.ID, "", op.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("operation survived permanent owner deletion: %v", err)
	}
}
func TestMemOperationOwnerDeletion(t *testing.T) { testOperationOwnerDeletion(t, state.NewMemStore()) }
func TestPgOperationOwnerDeletion(t *testing.T)  { s, _ := pgStore(t); testOperationOwnerDeletion(t, s) }
