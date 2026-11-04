// adr: 20261104085712
package objectstorage

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeKeyProjects struct {
	ids map[string][]string
	err error
}

func (f fakeKeyProjects) APIKeyProjectLimit(_ context.Context, keyID string) ([]string, bool, error) {
	ids, ok := f.ids[keyID]
	return ids, ok, f.err
}

// An agent key limited to projects uploads only to their apps, as the API
// allows it (ADR-20261104085712 amendment 1).
func TestUploadKeyOutsideProjects(t *testing.T) {
	h := &uploadHandler{keyProjects: fakeKeyProjects{ids: map[string][]string{"agent": {"proj-a"}}}}
	agent := state.APIKey{ID: "agent", Scopes: []string{api.ScopeAgent, api.ScopeStorageWrite}}
	unlimited := state.APIKey{ID: "other", Scopes: []string{api.ScopeAgent, api.ScopeStorageWrite}}
	plain := state.APIKey{ID: "agent", Scopes: []string{api.ScopeStorageWrite}} // not limitable, never read
	for _, tc := range []struct {
		name string
		key  state.APIKey
		app  state.App
		want bool
	}{
		{"limited key, its project", agent, state.App{ProjectID: "proj-a"}, false},
		{"limited key, another project", agent, state.App{ProjectID: "proj-b"}, true},
		{"limited key, app in no project", agent, state.App{}, true},
		{"agent key without a limit", unlimited, state.App{ProjectID: "proj-b"}, false},
		{"non-agent key", plain, state.App{ProjectID: "proj-b"}, false},
	} {
		got, err := h.keyOutsideProjects(context.Background(), tc.key, tc.app)
		if err != nil || got != tc.want {
			t.Errorf("%s: refused=%v err=%v, want refused=%v", tc.name, got, err, tc.want)
		}
	}
	failing := &uploadHandler{keyProjects: fakeKeyProjects{err: errors.New("down")}}
	if _, err := failing.keyOutsideProjects(context.Background(), agent, state.App{ProjectID: "proj-a"}); err == nil {
		t.Fatal("a failed limit read must not admit the upload")
	}
}
