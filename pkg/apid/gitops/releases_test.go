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

type recoveryStore struct {
	calls int
	err   error
}

func (s *recoveryStore) ListEnvironmentGitReleases(_ context.Context, source state.EnvironmentGitSource, request api.ListEnvironmentGitReleasesRequest, _ environmentsync.DesiredState) (api.ListEnvironmentGitReleasesResponse, error) {
	s.calls++
	if source.EnvironmentSlug != "production" || request.TargetCommitSHA != strings.Repeat("a", 40) {
		return api.ListEnvironmentGitReleasesResponse{}, errors.New("wrong scope")
	}
	return api.ListEnvironmentGitReleasesResponse{ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, Releases: []api.EnvironmentGitRecoveryRelease{}}, s.err
}
func TestRecoveryCatalogVerifiesImmutableTargetOnceAndRedactsErrors(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		invalid, denySource, denyRevision bool
		err                               error
		status, calls                     int
	}{
		{name: "catalog", status: 200, calls: 1}, {name: "invalid commit", invalid: true, status: 400}, {name: "foreign source", denySource: true, status: 404}, {name: "provider failure", denyRevision: true, status: 503}, {name: "stale source", err: state.ErrConflict, status: 409, calls: 1}, {name: "foreign cursor", err: state.ErrNotFound, status: 404, calls: 1}, {name: "database failure", err: errors.New("PRIVATE_DATABASE"), status: 503, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recoveryStore{err: tc.err}
			reads := 0
			service := RecoveryService{Store: store, Source: func(*http.Request, state.Account) (state.EnvironmentGitSource, *api.Problem) {
				if tc.denySource {
					return state.EnvironmentGitSource{}, api.NewProblem(404, "not_found", "Not found", "No source")
				}
				return state.EnvironmentGitSource{EnvironmentSlug: "production"}, nil
			}, Revision: func(_ context.Context, _ state.Account, _ state.EnvironmentGitSource, sha string) (environmentsync.DesiredState, *api.Problem) {
				reads++
				if sha != strings.Repeat("a", 40) {
					t.Fatal("mutable target fetched")
				}
				if tc.denyRevision {
					return environmentsync.DesiredState{}, api.ErrCapacity("Provider unavailable")
				}
				return environmentsync.DesiredState{}, nil
			}}
			sha := strings.Repeat("a", 40)
			if tc.invalid {
				sha = "main"
			}
			request := httptest.NewRequest(http.MethodGet, "/releases?target_commit_sha="+sha, nil)
			rec := httptest.NewRecorder()
			service.List(rec, request, state.Account{})
			if rec.Code != tc.status || store.calls != tc.calls || strings.Contains(rec.Body.String(), "PRIVATE_DATABASE") || reads > 1 || tc.invalid && reads != 0 {
				t.Fatalf("status=%d calls=%d reads=%d body=%s", rec.Code, store.calls, reads, rec.Body.String())
			}
			if tc.status == 200 {
				var out api.ListEnvironmentGitReleasesResponse
				if rec.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.ReadOnly {
					t.Fatal("unsafe response", rec.Body.String())
				}
			}
		})
	}
}
