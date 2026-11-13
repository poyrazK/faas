package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261110210000 slice 3h — the edge checks basic auth against a
// verifier keyed by the sealed blob and never opens the credential.

type fakeVerifierStore struct {
	verifiers map[string]string // string(key) -> verifier
	reads     int
	err       error
}

func (f *fakeVerifierStore) AppBasicAuthVerifier(_ context.Context, key []byte) (string, error) {
	f.reads++
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.verifiers[string(key)]
	if !ok {
		return "", state.ErrNotFound
	}
	return v, nil
}

type countingUnsealer struct {
	user, pass string
	calls      int
}

func (u *countingUnsealer) UnsealBasicAuth(context.Context, []byte) (string, string, error) {
	u.calls++
	return u.user, u.pass, nil
}

func TestBasicAuthVerifiers(t *testing.T) {
	ctx := context.Background()
	withVerifier, without := []byte("sealed-with-verifier"), []byte("sealed-legacy")
	verifier, err := basicverifier.New(ctx, "alice", "right")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeVerifierStore{verifiers: map[string]string{string(basicverifier.Key(withVerifier)): verifier}}
	unsealer := &countingUnsealer{user: "bob", pass: "legacy"}
	r := httptest.NewRequest("GET", "https://app.example/", nil)
	app := func(sealed []byte) App {
		return App{ID: "app-1", PublicAuth: PublicAuthConfig{Mode: publicAuthModeBasic, BasicSealed: sealed}}
	}

	for _, tc := range []struct {
		name        string
		fallback    bool
		sealed      []byte
		user, pass  string
		wantMatched bool
		wantOK      bool
	}{
		{"verifier matches", true, withVerifier, "alice", "right", true, true},
		{"verifier mismatch", true, withVerifier, "alice", "wrong", false, true},
		{"legacy blob opens with fallback", true, without, "bob", "legacy", true, true},
		{"legacy blob refused without fallback", false, without, "bob", "legacy", false, false},
		{"verifier matches without fallback", false, withVerifier, "alice", "right", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
			h.WithPublicAuth(NewPublicAuthCache(), unsealer)
			h.WithBasicAuthVerifiers(NewBasicAuthVerifiers(store, tc.fallback, nil))
			calls := unsealer.calls
			matched, ok := h.basicAuthMatches(r, app(tc.sealed), tc.user, tc.pass)
			if matched != tc.wantMatched || ok != tc.wantOK {
				t.Fatalf("matched, ok = %v, %v; want %v, %v", matched, ok, tc.wantMatched, tc.wantOK)
			}
			if string(tc.sealed) == string(withVerifier) && unsealer.calls != calls {
				t.Fatal("a blob with a verifier was opened")
			}
		})
	}

	// A matched credential is remembered; a store error refuses.
	v := NewBasicAuthVerifiers(store, false, nil)
	if ok, err := v.check(ctx, withVerifier, "alice", "right"); !ok || err != nil {
		t.Fatalf("first check = %v, %v", ok, err)
	}
	reads := store.reads
	if ok, err := v.check(ctx, withVerifier, "alice", "right"); !ok || err != nil || store.reads != reads {
		t.Fatalf("cached check = %v, %v, reads %d -> %d", ok, err, reads, store.reads)
	}
	failing := NewBasicAuthVerifiers(&fakeVerifierStore{err: errors.New("db down")}, true, nil)
	h := (&Handler{}).WithBasicAuthVerifiers(failing)
	h.WithPublicAuth(NewPublicAuthCache(), unsealer)
	if matched, ok := h.basicAuthMatches(r, app(withVerifier), "alice", "right"); matched || ok {
		t.Fatalf("store error = %v, %v; want refused", matched, ok)
	}
}
