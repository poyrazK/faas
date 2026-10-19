package statetest

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var errOverride = errors.New("override")

type restoreFailureFake struct {
	MemStore *state.MemStore
}

func (restoreFailureFake) RestoreApp(context.Context, string, api.Limits) (state.App, error) {
	return state.App{}, errOverride
}

func (restoreFailureFake) RestoreAppWithActivity(context.Context, string, api.Limits, state.OrgActivity) (state.App, int64, error) {
	return state.App{}, 0, errOverride
}

func TestMemDispatchesBothRestoreMutationPaths(t *testing.T) {
	store := NewMem(restoreFailureFake{MemStore: state.NewMemStore()})
	ctx := context.Background()
	limits := api.MustLimitsFor(api.PlanPro)
	if _, err := store.RestoreApp(ctx, "identified-app", limits); !errors.Is(err, errOverride) {
		t.Fatalf("RestoreApp = %v, want override", err)
	}
	if _, outbox, err := store.RestoreAppWithActivity(ctx, "identified-app", limits, state.OrgActivity{}); !errors.Is(err, errOverride) || outbox != 0 {
		t.Fatalf("RestoreAppWithActivity = %d, %v, want override without outbox", outbox, err)
	}
	var inventory state.AccountDeletedAppsStore = store
	if rows, err := inventory.ListAccountDeletedApps(ctx, "owned-account", time.Time{}, "", 2); err != nil || len(rows) != 0 {
		t.Fatalf("inventory delegation = %v, %v", rows, err)
	}
}

type appBySlugFake struct {
	MemStore *state.MemStore
}

func (appBySlugFake) AppBySlug(context.Context, string) (state.App, error) {
	return state.App{}, errOverride
}

func TestMemDispatchesOverridesAndDelegatesTheRest(t *testing.T) {
	mem := state.NewMemStore()
	ctx := context.Background()
	acct, err := mem.CreateAccount(ctx, "statetest@example.com", "hobby")
	if err != nil {
		t.Fatal(err)
	}
	var s state.Store = NewMem(&appBySlugFake{MemStore: mem})

	if _, err := s.AppBySlug(ctx, "anything"); !errors.Is(err, errOverride) {
		t.Fatalf("AppBySlug = %v, want the override's error", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil || got.ID != acct.ID {
		t.Fatalf("AccountByID = %+v, %v; want the wrapped store's account", got, err)
	}
}

func TestValueFakeDispatchesValueReceiverOverrides(t *testing.T) {
	s := NewMem(appBySlugFake{MemStore: state.NewMemStore()})
	if _, err := s.AppBySlug(context.Background(), "x"); !errors.Is(err, errOverride) {
		t.Fatalf("AppBySlug = %v, want the override's error", err)
	}
}

type undispatchedFake struct {
	MemStore *state.MemStore
}

// AccountByID is a MemStore method that Mem does not dispatch.
func (undispatchedFake) AccountByID(context.Context, string) (state.Account, error) {
	return state.Account{}, nil
}

func TestNewMemPanicsOnAnUndispatchedOverride(t *testing.T) {
	if memDispatched["AccountByID"] {
		t.Skip("AccountByID is dispatched now; pick another undispatched method for this test")
	}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "does not dispatch it") {
			t.Fatalf("recover() = %v, want the undispatched-override panic", r)
		}
	}()
	NewMem(&undispatchedFake{MemStore: state.NewMemStore()})
}

type nilStoreFake struct {
	Store state.Store
}

func (nilStoreFake) AppByID(context.Context, string) (state.App, error) {
	return state.App{}, errOverride
}

// TestNewStoreAcceptsANilStore: fakes that only exercise their overrides may
// leave the store nil, as they could with an embedded nil interface.
func TestNewStoreAcceptsANilStore(t *testing.T) {
	s := NewStore(&nilStoreFake{})
	if _, err := s.AppByID(context.Background(), "x"); !errors.Is(err, errOverride) {
		t.Fatalf("AppByID = %v, want the override's error", err)
	}
}

func TestNewMemPanicsWithoutTheStoreField(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "no MemStore field") {
			t.Fatalf("recover() = %v, want the missing-field panic", r)
		}
	}()
	NewMem(&struct{ Other int }{})
}

// TestDispatchMapsMatchMethods keeps dispatch.go's maps in step with its methods:
// a method missing from its map would make the constructors reject valid fakes,
// and a name without a method would let an override be silently ignored.
func TestDispatchMapsMatchMethods(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "dispatch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]map[string]bool{"Mem": {}, "Pg": {}, "Store": {}}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil {
			continue
		}
		recv := fd.Recv.List[0].Type.(*ast.StarExpr).X.(*ast.Ident).Name
		declared[recv][fd.Name.Name] = true
	}
	for recv, m := range map[string]map[string]bool{"Mem": memDispatched, "Pg": pgDispatched, "Store": storeDispatched} {
		for name := range m {
			if !declared[recv][name] {
				t.Errorf("%sDispatched lists %s but dispatch.go has no (*%s).%s", strings.ToLower(recv[:1])+recv[1:], name, recv, name)
			}
		}
		for name := range declared[recv] {
			if !m[name] {
				t.Errorf("dispatch.go declares (*%s).%s but its map does not list it", recv, name)
			}
		}
	}
}
