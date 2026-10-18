// adr: 1155 — candidate assessment and customer status feedback contracts.
package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewStore struct {
	calls int
	err   error
}

func (s *previewStore) PreviewEnvironmentGitOpsCommit(_ context.Context, source state.EnvironmentGitSource, sha string, _ environmentsync.DesiredState) (api.PreviewEnvironmentGitPlanResponse, error) {
	s.calls++
	return api.PreviewEnvironmentGitPlanResponse{SourceID: source.ID, ReadOnly: true, ValuesRedacted: true, Plan: api.EnvironmentGitOpsPlan{CommitSHA: sha}}, s.err
}

func TestPreviewVerifiesScopeAndCommitBeforeObservation(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		sourceError, revisionError bool
		storeError                 error
		status, calls              int
	}{
		{name: "verified", status: 200, calls: 1},
		{name: "scope denied", sourceError: true, status: 404},
		{name: "source unavailable", revisionError: true, status: 503},
		{name: "rebind raced", storeError: state.ErrConflict, status: 409, calls: 1},
		{name: "storage unavailable", storeError: errors.New("private database diagnostic"), status: 503, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &previewStore{err: tc.storeError}
			sha := strings.Repeat("a", 40)
			service := PreviewService{Store: store, Decode: func(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) },
				Source: func(_ *http.Request, _ state.Account) (state.EnvironmentGitSource, *api.Problem) {
					if tc.sourceError {
						return state.EnvironmentGitSource{}, api.NewProblem(404, "environment_git_source_not_found", "Missing", "Missing source")
					}
					return state.EnvironmentGitSource{ID: "verified-source"}, nil
				},
				Revision: func(_ context.Context, _ state.Account, source state.EnvironmentGitSource, commit string) (environmentsync.DesiredState, *api.Problem) {
					if source.ID != "verified-source" || commit != sha {
						t.Fatal("provider verification used a different scope or SHA")
					}
					if tc.revisionError {
						return environmentsync.DesiredState{}, api.ErrSourceRefUnavailable("Unavailable commit")
					}
					return environmentsync.DesiredState{}, nil
				}}
			rec := httptest.NewRecorder()
			service.Preview(rec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`{"commit_sha":"`+sha+`"}`)), state.Account{})
			if rec.Code != tc.status || store.calls != tc.calls || strings.Contains(rec.Body.String(), "private database diagnostic") {
				t.Fatalf("status=%d calls=%d body=%s", rec.Code, store.calls, rec.Body.String())
			}
			if tc.status == 200 && rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("candidate result could be cached")
			}
		})
	}
}
