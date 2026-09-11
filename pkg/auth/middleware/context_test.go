// Whitebox tests for the ctx-stamping helpers. Lives in package
// auth (internal test) so it can stamp principals via the
// unexported withPrincipal helper. The external test package's
// helper authWithPrincipal does the same job for the cross-package
// callers (see helpers in middleware_test.go).
package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountFromContext_MissingStamp(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if _, _, ok := AccountFromContext(r); ok {
		t.Errorf("ok = true on empty context; want false")
	}
}

func TestAccountFromContext_StampedBearer(t *testing.T) {
	acct := state.Account{ID: "acct-1", Email: "x@y"}
	key := &state.APIKey{ID: "key-1", Scopes: []string{api.ScopeAdmin}}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: key}))
	gotAcct, gotKey, ok := AccountFromContext(r)
	if !ok {
		t.Fatal("ok = false on stamped context")
	}
	if gotAcct.ID != acct.ID {
		t.Errorf("Acct.ID = %q, want %q", gotAcct.ID, acct.ID)
	}
	if gotKey == nil || gotKey.ID != "key-1" {
		t.Errorf("Key.ID = %v, want key-1", gotKey)
	}
}

func TestAccountFromContext_StampedSessionCookie(t *testing.T) {
	// Session-cookie principal: Key=nil → RequireScope treats
	// as implicit admin. AccountFromContext surfaces the raw
	// shape.
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withPrincipal(r.Context(), principal{Acct: state.Account{ID: "acct-1"}, Key: nil}))
	gotAcct, gotKey, ok := AccountFromContext(r)
	if !ok {
		t.Fatal("ok = false on stamped context")
	}
	if gotAcct.ID != "acct-1" {
		t.Errorf("Acct.ID = %q, want acct-1", gotAcct.ID)
	}
	if gotKey != nil {
		t.Errorf("Key = %v, want nil (session cookie)", gotKey)
	}
}

func TestSessionFromContext_MissingStamp(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if _, ok := SessionFromContext(r); ok {
		t.Errorf("ok = true on empty context; want false")
	}
}

func TestSessionFromContext_Stamped(t *testing.T) {
	sess := state.Session{ID: "sid-1", AccountID: "acct-1"}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withSession(r.Context(), sess))
	got, ok := SessionFromContext(r)
	if !ok {
		t.Fatal("ok = false on stamped context")
	}
	if got.ID != "sid-1" {
		t.Errorf("ID = %q, want sid-1", got.ID)
	}
}

func TestMFAPendingFrom_AbsentStamp(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	v, ok := MFAPendingFrom(r)
	if v || ok {
		t.Errorf("got (%v, %v); want (false, false)", v, ok)
	}
}

func TestMFAPendingFrom_StampedTrue(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withMFAPending(r.Context(), true))
	v, ok := MFAPendingFrom(r)
	if !v || !ok {
		t.Errorf("got (%v, %v); want (true, true)", v, ok)
	}
}

func TestMFAPendingFrom_StampedFalse(t *testing.T) {
	// ok=true but v=false — distinguishes "stamped false" from
	// "not stamped" so RequireMFA can short-circuit correctly
	// on each branch.
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(withMFAPending(r.Context(), false))
	v, ok := MFAPendingFrom(r)
	if v || !ok {
		t.Errorf("got (%v, %v); want (false, true)", v, ok)
	}
}

func TestConsumerFromContext_MissingStamp(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if _, ok := ConsumerFromContext(r); ok {
		t.Fatal("ok = true on empty context; want false")
	}
}

func TestConsumerFromContext_StampedIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(WithConsumer(r.Context(), ConsumerIdentity{
		ID: "consumer-1", AppID: "app-1", KeyID: "key-1", Scopes: []string{"read"},
	}))
	got, ok := ConsumerFromContext(r)
	if !ok {
		t.Fatal("ok = false on stamped consumer")
	}
	if got.ID != "consumer-1" || got.AppID != "app-1" || got.KeyID != "key-1" {
		t.Fatalf("identity = %+v, want consumer-1/app-1/key-1", got)
	}
	got.Scopes[0] = "write"
	again, ok := ConsumerFromContext(r)
	if !ok || len(again.Scopes) != 1 || again.Scopes[0] != "read" {
		t.Fatalf("context scopes were mutable: %+v", again.Scopes)
	}
}

func TestNew_NilAuthnPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("New(nil, ...) did not panic")
		}
	}()
	New(nil, nil, nil, nil, nil, nil, nil)
}
