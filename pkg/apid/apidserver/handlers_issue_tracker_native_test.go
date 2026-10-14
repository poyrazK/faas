//go:build !no_pg

package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeGitHubFake struct {
	apidserver.GithubdClient
	repoID int64
	mints  int
}

func (g *nativeGitHubFake) ListInstallableRepos(context.Context, string, int64) ([]apidserver.Repo, error) {
	return []apidserver.Repo{{ID: g.repoID, FullName: "owner/repo"}}, nil
}
func (g *nativeGitHubFake) MintInstallationToken(context.Context, string, int64) (string, time.Time, error) {
	g.mints++
	return "native-secret", time.Now().Add(time.Hour), nil
}

type nativeStatusFake struct {
	status        issuetracker.Status
	reads, writes int
}

func (p *nativeStatusFake) ReadStatus(_ context.Context, _ string, token string) (issuetracker.Status, error) {
	p.reads++
	return p.status, nil
}
func (p *nativeStatusFake) SetStatus(_ context.Context, expected issuetracker.Status, token, wanted, _ string) (issuetracker.Status, error) {
	if p.status != expected {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	p.writes++
	p.status.State = wanted
	return p.status, nil
}

// adr: 1211
func TestNativeIssueTrackerUsesOwnedConnectionAndRejectsReportersAndRevokedRepository(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-tracker")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PaymentError", Message: "failed"}, reporter), 202)
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", base+"/issues/"+accepted.IssueID+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	path := base + "/issues/" + accepted.IssueID + "/tracker-links/" + link.ID + "/native-status"
	input := api.UpdateNativeIssueTrackerStatusRequest{DesiredState: "open", Expected: api.IssueTrackerProviderStatus{Provider: "github", URL: link.URL, ID: "42", State: "resolved"}}
	for _, method := range []string{"GET", "POST"} {
		w := e.Do(t, method, path, input, reporter)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("reporter accessed native provider", method, w.Code)
		}
	}
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 409 {
		t.Fatal("unconnected app reached provider", w.Code, w.Body.String())
	}
	store := apidserver.IssueNativeStore(e)
	acct := apidserver.IssueNativeAccount(e)
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main", AuditGithubLogin: "owner", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: uuid.NewString(), InstallID: 42, RepoFullName: "Owner/Repo", OwnerID: 10, RepoID: 100, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	gh := &nativeGitHubFake{repoID: 100}
	provider := &nativeStatusFake{status: issuetracker.Status{Provider: "github", URL: link.URL, ID: "42", State: "resolved"}}
	apidserver.IssueNativeSetClients(e, gh, provider)
	response := e.Do(t, "GET", path, nil, nil)
	preview := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, response, 200)
	if preview.InstallationID != 42 || preview.RepositoryID != 100 || preview.Status.State != "resolved" || strings.Contains(response.Body.String(), "native-secret") {
		t.Fatal("native connection projection invalid or leaked credential")
	}
	result := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "POST", path, input, nil), 200)
	if result.Status.State != "open" || provider.writes != 1 {
		t.Fatal("native push failed")
	}
	if w := e.Do(t, "POST", path, input, nil); w.Code != 409 || provider.writes != 1 || !strings.Contains(w.Body.String(), "issue_tracker_status_changed") {
		t.Fatal("stale provider snapshot was not distinguished", w.Code, w.Body.String())
	}
	invalid := input
	invalid.DesiredState = "resolved"
	if w := e.Do(t, "POST", path, invalid, nil); w.Code != 409 || provider.writes != 1 {
		t.Fatal("API allowed remote lifecycle different from Gregale", w.Code)
	}
	foreign := apidserver.SeedPGApp(t, e, "native-foreign")
	foreignPath := "/v1/apps/" + foreign.Slug + "/issues/" + accepted.IssueID + "/tracker-links/" + link.ID + "/native-status"
	if w := e.Do(t, "GET", foreignPath, nil, nil); w.Code != http.StatusNotFound {
		t.Fatal("cross-app native issue accessed", w.Code)
	}
	mints := gh.mints
	gh.repoID = 200
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 409 || gh.mints != mints {
		t.Fatal("changed repository identity authorized token mint", w.Code)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "native-secret") {
		t.Fatal("credential persisted in public response")
	}
}
