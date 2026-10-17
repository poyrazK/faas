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

type rollbackStore struct {
	calls int
	err   error
}

func (s *rollbackStore) PreviewEnvironmentGitOpsRollback(_ context.Context, to state.EnvironmentGitSource, r api.PreviewEnvironmentGitRollbackRequest, _ environmentsync.DesiredState) (api.PreviewEnvironmentGitRollbackResponse, error) {
	s.calls++
	if to.EnvironmentSlug != "production" || r.TargetCommitSHA != strings.Repeat("a", 40) {
		return api.PreviewEnvironmentGitRollbackResponse{}, errors.New("wrong scope")
	}
	return api.PreviewEnvironmentGitRollbackResponse{ReadOnly: true}, s.err
}

func TestRollbackPreviewVerifiesBothScopesAndTargetCommit(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		fromDenied, revisionDenied bool
		err                        error
		status, calls              int
	}{
		{name: "preview", status: 200, calls: 1}, {name: "target forbidden", fromDenied: true, status: 404}, {name: "provider denied", revisionDenied: true, status: 503},
		{name: "unproven release", err: state.ErrEnvironmentGitOpsRollbackReleaseUnproven, status: 409, calls: 1}, {name: "changed observation", err: state.ErrConflict, status: 409, calls: 1}, {name: "database error redacted", err: errors.New("PRIVATE_DATABASE"), status: 503, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &rollbackStore{err: tc.err}
			service := RollbackService{Store: store, Decode: func(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) },
				Source: func(r *http.Request, a state.Account) (state.EnvironmentGitSource, *api.Problem) {
					env := r.PathValue("environment")
					if env == "production" && tc.fromDenied {
						return state.EnvironmentGitSource{}, api.NewProblem(http.StatusNotFound, "environment_git_source_not_found", "Missing source", "No source in this scope.")
					}
					return state.EnvironmentGitSource{EnvironmentSlug: env}, nil
				},
				Revision: func(_ context.Context, _ state.Account, s state.EnvironmentGitSource, sha string) (environmentsync.DesiredState, *api.Problem) {
					if s.EnvironmentSlug != "production" || sha != strings.Repeat("a", 40) {
						t.Fatal("unverified target")
					}
					if tc.revisionDenied {
						return environmentsync.DesiredState{}, api.ErrSourceRefUnavailable("Unavailable")
					}
					return environmentsync.DesiredState{}, nil
				}}
			r := httptest.NewRequest(http.MethodPost, "/rollback-plan", strings.NewReader(`{"release_set_id":"2ba0aa40-2362-4c0e-aeb1-6381666bb514","target_commit_sha":"`+strings.Repeat("a", 40)+`"}`))
			r.SetPathValue("environment", "production")
			w := httptest.NewRecorder()
			service.Preview(w, r, state.Account{})
			if w.Code != tc.status || store.calls != tc.calls || r.PathValue("environment") != "production" || strings.Contains(w.Body.String(), "PRIVATE_DATABASE") {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, store.calls, w.Body.String())
			}
			if tc.status == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable rollback snapshot")
			}
		})
	}
}
