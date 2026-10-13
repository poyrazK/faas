// Package statetest lets a test fake override a few methods of a state store
// without embedding the store in its own type.
//
// Every struct type that embeds a store makes the Go compiler emit a wrapper for
// each promoted method: about 3,000 for *state.MemStore or *state.PgStore and
// 1,000 for state.Store. At ~80 MB of compiler memory per fake type, that
// dominated the apidserver test compiles (ADR-982). The wrappers here embed each
// store once, so their methods compile once.
//
// A fake keeps the store in a named field and defines only its overrides:
//
//	type failingCreate struct {
//		MemStore *state.MemStore
//		err      error
//	}
//
//	func (f *failingCreate) CreateApp(ctx context.Context, a state.App) (state.App, error) {
//		return state.App{}, f.err
//	}
//
//	srv := newServer(statetest.NewMem(&failingCreate{MemStore: mem, err: boom}), ...)
//
// Methods the fake defines go to the fake (see dispatch.go); all others go to
// the store in its MemStore, PgStore or Store field, read once when wrapping.
package statetest

import (
	"fmt"
	"reflect"

	"github.com/onebox-faas/faas/pkg/state"
)

// Mem is a *state.MemStore whose methods a test fake can override.
type Mem struct {
	*state.MemStore
	over any
}

// Pg is a *state.PgStore whose methods a test fake can override.
type Pg struct {
	*state.PgStore
	over any
}

// Store is a state.Store whose methods a test fake can override.
type Store struct {
	state.Store
	over any
}

// NewMem wraps a fake with a MemStore *state.MemStore field.
func NewMem(over any) *Mem {
	checkOverrides(over, reflect.TypeFor[*state.MemStore](), memDispatched, "Mem")
	return &Mem{MemStore: field[*state.MemStore](over, "MemStore"), over: over}
}

// NewPg wraps a fake with a PgStore *state.PgStore field.
func NewPg(over any) *Pg {
	checkOverrides(over, reflect.TypeFor[*state.PgStore](), pgDispatched, "Pg")
	return &Pg{PgStore: field[*state.PgStore](over, "PgStore"), over: over}
}

// NewStore wraps a fake with a Store state.Store field.
func NewStore(over any) *Store {
	checkOverrides(over, reflect.TypeFor[state.Store](), storeDispatched, "Store")
	return &Store{Store: field[state.Store](over, "Store"), over: over}
}

// Override returns the fake a wrapper was built from, for tests that need it back.
func (s *Mem) Override() any   { return s.over }
func (s *Pg) Override() any    { return s.over }
func (s *Store) Override() any { return s.over }

// field reads over's named store field. over is a struct or a pointer to one.
func field[T any](over any, name string) T {
	v := reflect.ValueOf(over)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			panic(fmt.Sprintf("statetest: nil %T", over))
		}
		v = v.Elem()
	}
	f := v.FieldByName(name)
	if !f.IsValid() {
		panic(fmt.Sprintf("statetest: %T has no %s field", over, name))
	}
	if f.IsZero() {
		// A nil store, as with an embedded nil: only the fake's overrides may be called.
		var zero T
		return zero
	}
	s, ok := f.Interface().(T)
	if !ok {
		panic(fmt.Sprintf("statetest: %T.%s is %s, not %s", over, name, f.Type(), reflect.TypeFor[T]()))
	}
	return s
}

// checkOverrides panics when over defines a store method that the wrapper does
// not dispatch: without a dispatch method the override would be silently ignored.
func checkOverrides(over any, store reflect.Type, dispatched map[string]bool, wrapper string) {
	t := reflect.TypeOf(over)
	for i := 0; i < t.NumMethod(); i++ {
		name := t.Method(i).Name
		if _, ok := store.MethodByName(name); ok && !dispatched[name] {
			panic(fmt.Sprintf("statetest: %s overrides %s, but statetest.%s does not dispatch it; add it to pkg/state/statetest/dispatch.go", t, name, wrapper))
		}
	}
}
