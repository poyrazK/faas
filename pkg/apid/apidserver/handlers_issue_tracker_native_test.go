//go:build !no_pg

package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

type nativeCreateFake struct{ creates int }

func (p *nativeCreateFake) Create(_ context.Context, r issuetracker.Request, token string) (issuetracker.Ticket, error) {
	p.creates++
	if token != "native-secret" {
		return issuetracker.Ticket{}, fmt.Errorf("unexpected credential")
	}
	return issuetracker.Ticket{URL: "https://github.com/owner/repo/issues/43", ProviderID: "43"}, nil
}
func (p *nativeCreateFake) Recover(context.Context, issuetracker.Request, string, string) (issuetracker.Ticket, error) {
	panic("unexpected recovery")
}

// adr: 1212
func TestNativeIssueTicketCreationPersistsRetryEvidenceAndScopesConnection(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-create")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "CreateError", Message: "failed"}, reporter), 202)
	path := base + "/issues/" + accepted.IssueID + "/native-tickets"
	in := api.CreateNativeIssueTicketRequest{RequestID: uuid.NewString(), Repository: "owner/repo", Title: "Fix creation"}
	if w := e.Do(t, "POST", path, in, reporter); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporter created native ticket", w.Code)
	}
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 {
		t.Fatal("unconnected create", w.Code, w.Body.String())
	}
	store := apidserver.IssueNativeStore(e)
	acct := apidserver.IssueNativeAccount(e)
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main", AuditGithubLogin: "owner", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	binding := state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: uuid.NewString(), InstallID: 42, RepoFullName: "Owner/Repo", OwnerID: 10, RepoID: 100, ProductionBranch: "main"}
	if err := store.UpsertGithubInstallBinding(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	gh := &nativeGitHubFake{repoID: 100}
	provider := &nativeCreateFake{}
	apidserver.IssueNativeSetClients(e, gh, &nativeStatusFake{})
	apidserver.IssueNativeSetTicketProvider(e, provider)
	first := apidserver.IssueDecode[api.NativeIssueTicketResult](t, e.Do(t, "POST", path, in, nil), 200)
	second := apidserver.IssueDecode[api.NativeIssueTicketResult](t, e.Do(t, "POST", path, in, nil), 200)
	if first.State != "linked" || first.LinkID == "" || second != first || provider.creates != 1 {
		t.Fatal("native create retry duplicated or lost receipt", first, second)
	}
	refs := apidserver.IssueDecode[api.ListIssueTrackerLinksResponse](t, e.Do(t, "GET", base+"/issues/"+accepted.IssueID+"/tracker-links", nil, nil), 200)
	if len(refs.Items) != 1 || refs.Items[0].URL != first.TicketURL {
		t.Fatal("ticket not attached")
	}
	altered := in
	altered.Title = "changed"
	if w := e.Do(t, "POST", path, altered, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("changed inputs dispatched", w.Code, w.Body.String())
	}
	altered = in
	altered.RequestID = uuid.NewString()
	altered.Repository = "other/repo"
	if w := e.Do(t, "POST", path, altered, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("unbound repository created", w.Code)
	}
	receipts := store.(state.NativeIssueTicketStore)
	r, err := receipts.ReserveNativeIssueTicket(t.Context(), state.NativeIssueTicketReceipt{AccountID: acct.ID, AppID: app.ID, IssueID: accepted.IssueID, RequestID: uuid.NewString(), Hash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	claims := make(chan state.NativeIssueTicketReceipt, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := receipts.ClaimNativeIssueTicket(t.Context(), r, false)
			if e == nil {
				claims <- c
			} else if !errors.Is(e, state.ErrNativeIssueTicketBusy) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatal("concurrent dispatch was not fenced", len(claims))
	}
	claimed := <-claims
	if _, err = receipts.ClaimNativeIssueTicket(t.Context(), r, false); !errors.Is(err, state.ErrNativeIssueTicketBusy) {
		t.Fatal("duplicate dispatch claim", err)
	}
	if _, err = receipts.ClaimNativeIssueTicket(t.Context(), claimed, true); !errors.Is(err, state.ErrNativeIssueTicketBusy) {
		t.Fatal("active recovery claim", err)
	}
	claimed.ClaimID = uuid.NewString()
	claimed.State = "created"
	claimed.TicketURL = first.TicketURL
	claimed.ProviderID = "43"
	if _, err = receipts.FinishNativeIssueTicket(t.Context(), claimed); !errors.Is(err, state.ErrNativeIssueTicketBusy) {
		t.Fatal("stale claim completed", err)
	}
	foreign := apidserver.SeedPGApp(t, e, "native-create-foreign")
	if w := e.Do(t, "POST", "/v1/apps/"+foreign.Slug+"/issues/"+accepted.IssueID+"/native-tickets", in, nil); w.Code != 404 {
		t.Fatal("cross-app creation", w.Code)
	}
	for i := 0; i < api.IssueTrackerNativeRequestsMax-2; i++ {
		if _, err := receipts.ReserveNativeIssueTicket(t.Context(), state.NativeIssueTicketReceipt{AccountID: acct.ID, AppID: app.ID, IssueID: accepted.IssueID, RequestID: uuid.NewString(), Hash: strings.Repeat("b", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := receipts.ReserveNativeIssueTicket(t.Context(), state.NativeIssueTicketReceipt{AccountID: acct.ID, AppID: app.ID, IssueID: accepted.IssueID, RequestID: uuid.NewString(), Hash: strings.Repeat("c", 64)}); err == nil {
		t.Fatal("native receipt quota ignored")
	}
	if w := e.Do(t, "POST", path, in, nil); w.Code != 200 || provider.creates != 1 {
		t.Fatal("quota blocked retained retry", w.Code)
	}
	gh.repoID = 200
	altered = in
	altered.RequestID = uuid.NewString()
	if w := e.Do(t, "POST", path, altered, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("revoked repository created", w.Code)
	}
}
