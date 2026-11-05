package managedpostgres_test

import (
	"context"
	"errors"
	"testing"

	apimanagedpostgres "github.com/onebox-faas/faas/pkg/apid/managedpostgres"
	domain "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type erasureFake struct {
	included  bool
	databases map[string]domain.State
	bindings  map[string][]domain.Binding
	stuck     map[string]bool // databases whose deletion keeps running
	failures  map[string]error
	calls     []string
}

func (f *erasureFake) AccountErasure(context.Context, string) (domain.AccountErasure, error) {
	out := domain.AccountErasure{Included: f.included}
	for id, s := range f.databases {
		if s != domain.StateDeleted {
			out.Databases = append(out.Databases, domain.Database{ID: id, State: s})
		}
	}
	return out, nil
}

func (f *erasureFake) Delete(_ context.Context, accountID, id string) (domain.Database, error) {
	f.calls = append(f.calls, "database:"+id)
	if err := f.failures[id]; err != nil {
		return domain.Database{}, err
	}
	for _, b := range f.bindings[id] {
		if b.State != domain.BindingStateDeleted {
			return domain.Database{}, domain.ErrConflict // the store refuses live bindings
		}
	}
	if f.stuck[id] {
		f.databases[id] = domain.StateDeleting
		return domain.Database{ID: id, State: domain.StateDeleting}, nil
	}
	f.databases[id] = domain.StateDeleted
	return domain.Database{ID: id, State: domain.StateDeleted}, nil
}

func (f *erasureFake) List(_ context.Context, _, databaseID string) ([]domain.Binding, error) {
	return f.bindings[databaseID], nil
}

func (f *erasureFake) DeleteBinding(_ context.Context, accountID, id string) (domain.Binding, error) {
	f.calls = append(f.calls, "binding:"+id+"@"+accountID)
	for _, bindings := range f.bindings {
		for i := range bindings {
			if bindings[i].ID == id {
				bindings[i].State = domain.BindingStateDeleted
				return bindings[i], f.failures[id]
			}
		}
	}
	return domain.Binding{}, domain.ErrNotFound
}

type bindingsOf struct{ *erasureFake }

func (b bindingsOf) List(ctx context.Context, account, database string) ([]domain.Binding, error) {
	return b.erasureFake.List(ctx, account, database)
}

func (b bindingsOf) Delete(ctx context.Context, account, id string) (domain.Binding, error) {
	return b.DeleteBinding(ctx, account, id)
}

func TestAccountErasureBeforeAccountDelete(t *testing.T) {
	acct := state.Account{ID: "acct"}
	tests := []struct {
		name      string
		fake      erasureFake
		pending   bool
		wantCalls []string
		audits    int
	}{
		{name: "nothing to erase", fake: erasureFake{included: true, databases: map[string]domain.State{}}},
		{name: "deletion does not include the databases", pending: true,
			fake: erasureFake{databases: map[string]domain.State{"db": domain.StateReady}}},
		{name: "bindings go before their database", audits: 2,
			fake: erasureFake{included: true, databases: map[string]domain.State{"db": domain.StateReady},
				bindings: map[string][]domain.Binding{"db": {{ID: "b1", AccountID: "app-owner", DatabaseID: "db", State: domain.BindingStateReady}}}},
			wantCalls: []string{"binding:b1@app-owner", "database:db"}},
		{name: "a provider deletion still running keeps the account", pending: true, audits: 1,
			fake:      erasureFake{included: true, databases: map[string]domain.State{"db": domain.StateReady}, stuck: map[string]bool{"db": true}},
			wantCalls: []string{"database:db"}},
		{name: "a failed delete is retried next tick", pending: true,
			fake:      erasureFake{included: true, databases: map[string]domain.State{"db": domain.StateReady}, failures: map[string]error{"db": domain.ErrUnavailable}},
			wantCalls: []string{"database:db"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := tt.fake
			audits := 0
			erasure := apimanagedpostgres.AccountErasure{
				Databases: &fake, Bindings: bindingsOf{&fake},
				Audit: func(context.Context, string, *string, map[string]any) { audits++ },
			}
			err := erasure.BeforeAccountDelete(t.Context(), acct)
			if pending := errors.Is(err, apimanagedpostgres.ErrAccountErasurePending); pending != tt.pending || (!tt.pending && err != nil) {
				t.Fatalf("err = %v, want pending %v", err, tt.pending)
			}
			if len(fake.calls) != len(tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", fake.calls, tt.wantCalls)
			}
			for i := range tt.wantCalls {
				if fake.calls[i] != tt.wantCalls[i] {
					t.Fatalf("calls = %v, want %v", fake.calls, tt.wantCalls)
				}
			}
			if audits != tt.audits {
				t.Fatalf("audits = %d, want %d", audits, tt.audits)
			}
		})
	}
}

func TestAccountErasureWithoutManagedPostgresIsANoOp(t *testing.T) {
	if err := (apimanagedpostgres.AccountErasure{}).BeforeAccountDelete(t.Context(), state.Account{ID: "acct"}); err != nil {
		t.Fatal(err)
	}
}
