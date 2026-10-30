package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type staticTokenFetcher struct{}

func (staticTokenFetcher) ExchangeInstallationToken(context.Context, int64) (string, time.Time, error) {
	return "ghs_test", time.Now().Add(time.Hour), nil
}

// rewriteClient sends api.github.com requests to a test server.
type rewriteClient struct {
	base   *http.Client
	target *url.URL
}

func (c rewriteClient) Do(req *http.Request) (*http.Response, error) {
	req.URL.Scheme, req.URL.Host = c.target.Scheme, c.target.Host
	return c.base.Do(req)
}

type knownGitHubDeployment struct{ id int64 }

func (k knownGitHubDeployment) GitHubDeploymentID(context.Context, string) (int64, error) {
	return k.id, nil
}

func (knownGitHubDeployment) SaveGitHubDeploymentID(context.Context, string, int64) error { return nil }

// An App without the Deployments permission must not keep a torn-down
// preview's PR comment from saying so: the refusal is skipped, not retried
// until the check update goes dead.
func TestSyncTornDownPreviewWithoutDeploymentsPermission(t *testing.T) {
	var patched bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments/4242/statuses"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/octo/api/issues/42/comments":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":7,"body":"<!-- gregale-preview:pr-42-demo -->\nlive","user":{"type":"Bot"}}]`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/octo/api/issues/comments/7":
			patched = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":7}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := githubd.NewChecksAPI(githubd.NewTokenCache(staticTokenFetcher{}, time.Minute),
		rewriteClient{base: fake.Client(), target: target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	checks.WithGitHubDeploymentStore(knownGitHubDeployment{id: 4242})
	err = syncTornDownPreview(context.Background(), checks, tornDownPreview{
		deploymentID: "deployment-1", installationID: 99, repo: "octo/api", commitSHA: "deadbeef",
		environment: "preview/demo", appSlug: "pr-42-demo", prNumber: 42,
	})
	if err != nil {
		t.Fatalf("syncTornDownPreview = %v, want the Deployments refusal skipped", err)
	}
	if !patched {
		t.Fatal("the torn-down preview comment was not written")
	}
}

func TestValidateDeploymentCheckTarget(t *testing.T) {
	tests := []struct {
		name           string
		kind           string
		commit         string
		repo           string
		installationID int64
		wantProject    bool
		wantErr        bool
	}{
		{name: "generic deployment ignores absent GitHub metadata", kind: "manual"},
		{name: "GitHub deployment requires commit", kind: string(state.DeploymentKindGitHub), repo: "owner/repo", installationID: 42, wantErr: true},
		{name: "preview deployment requires commit", kind: string(state.DeploymentKindPreview), repo: "owner/repo", installationID: 42, wantErr: true},
		{name: "disconnected repository is complete without projection", kind: string(state.DeploymentKindGitHub), commit: "abc", installationID: 42},
		{name: "disconnected installation is complete without projection", kind: string(state.DeploymentKindGitHub), commit: "abc", repo: "owner/repo"},
		{name: "disconnected preview is complete without projection", kind: string(state.DeploymentKindPreview), commit: "abc", repo: "owner/repo"},
		{name: "GitHub deployment projects", kind: string(state.DeploymentKindGitHub), commit: "abc", repo: "owner/repo", installationID: 42, wantProject: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, err := validateDeploymentCheckTarget("dep-1", tc.kind, tc.commit, tc.repo, tc.installationID)
			if project != tc.wantProject || (err != nil) != tc.wantErr {
				t.Fatalf("validate = (%v, %v), want project=%v err=%v", project, err, tc.wantProject, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "dep-1") {
				t.Fatalf("error %q does not identify the deployment", err)
			}
		})
	}
}

func TestCheckPhaseForDeploymentStatusForRollout(t *testing.T) {
	cases := []struct {
		name             string
		status           string
		rolloutState     string
		canaryTotalSteps int
		want             githubdgrpc.CheckPhase
		ok               bool
	}{
		{name: "building", status: "building", want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "canary pending remains in progress", status: "live", rolloutState: "pending", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "canary rolling remains in progress", status: "live", rolloutState: "rolling_out", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseBuilding, ok: true},
		{name: "complete is successful", status: "live", rolloutState: "complete", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseLive, ok: true},
		{name: "aborted fails the check", status: "live", rolloutState: "aborted", canaryTotalSteps: 4, want: githubdgrpc.CheckPhaseFailed, ok: true},
		{name: "non-canary live is successful", status: "live", rolloutState: "pending", want: githubdgrpc.CheckPhaseLive, ok: true},
		{name: "unknown status is ignored", status: "deleted", want: githubdgrpc.CheckPhaseUnspecified, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := checkPhaseForDeploymentStatusForRollout(tc.status, tc.rolloutState, tc.canaryTotalSteps)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("projection = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
