package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSameAppResourceOwner(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b state.App
		want bool
	}{
		{"same company different namespaces", state.App{AccountID: "alice", OrgID: "company"}, state.App{AccountID: "bob", OrgID: "company"}, true},
		{"same namespace different companies", state.App{AccountID: "alice", OrgID: "one"}, state.App{AccountID: "alice", OrgID: "two"}, false},
		{"personal and company", state.App{AccountID: "alice"}, state.App{AccountID: "alice", OrgID: "company"}, false},
		{"legacy personal", state.App{AccountID: "alice"}, state.App{AccountID: "alice"}, true},
		{"missing namespace", state.App{OrgID: "company"}, state.App{AccountID: "bob", OrgID: "company"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.SameAppResourceOwner(tc.a, tc.b); got != tc.want {
				t.Fatalf("ownership=%v; want %v", got, tc.want)
			}
		})
	}
}
