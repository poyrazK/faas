// adr: 521
package acceptance_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationHistory(t *testing.T, s operationFixtureStore) {
	ctx, acct, app, def, alice, bob := operationFixture(t, s)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: fmt.Sprintf("history-%d", i), Input: []byte(`{"count":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		want[op.ID] = true
	}
	if _, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: bob.ID, IdempotencyKey: "bob", Input: []byte(`{"count":1}`)}); err != nil {
		t.Fatal(err)
	}
	opts := api.OperationListOptions{AppID: app.ID, Scope: def.Scope, Name: "export", State: api.OperationAccepted, Limit: 2}
	page, err := s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(page.Operations) != 2 || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if _, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID, PlatformTenantID: alice.ID, IdempotencyKey: "after-first-page", Input: []byte(`{"count":1}`)}); err != nil {
		t.Fatal(err)
	}
	foreign := opts
	foreign.Cursor = page.NextCursor
	if _, err := s.ListPlatformTenantOperations(ctx, acct.ID, bob.ID, foreign); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("cross-tenant cursor: %v", err)
	}
	for {
		for _, row := range page.Operations {
			if !want[row.ID] {
				t.Fatalf("foreign, duplicate or new row: %s", row.ID)
			}
			delete(want, row.ID)
		}
		if page.NextCursor == "" {
			break
		}
		opts.Cursor = page.NextCursor
		page, err = s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, opts)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing old operations: %v", want)
	}
	opts.Cursor, opts.Scope = "", "staging"
	page, err = s.ListPlatformTenantOperations(ctx, acct.ID, alice.ID, opts)
	if err != nil || len(page.Operations) != 0 {
		t.Fatalf("environment leak: %+v %v", page, err)
	}
}

func TestMemOperationHistory(t *testing.T) { testOperationHistory(t, state.NewMemStore()) }
func TestPgOperationHistory(t *testing.T)  { s, _ := pgStore(t); testOperationHistory(t, s) }
