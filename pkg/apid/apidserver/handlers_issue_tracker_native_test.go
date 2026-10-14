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

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
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

// adr: 1213
func TestNativeIssueTrackerAutoSyncScopesClaimsPushesAndPauses(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-auto")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	fixed := apidserver.IssueSeedDeployment(t, e, app, 2)
	ingest := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + ingest.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "AutoError", Message: "failed"}, reporter), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	path := issuePath + "/tracker-links/" + link.ID + "/auto-sync"
	native := issuePath + "/tracker-links/" + link.ID + "/native-status"
	for _, method := range []string{"GET", "PUT"} {
		if w := e.Do(t, method, path, api.UpdateIssueTrackerAutoSyncRequest{}, reporter); w.Code != 401 && w.Code != 403 {
			t.Fatal("reporter accessed policy", method, w.Code)
		}
	}
	original := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path, nil, nil), 200)
	if original.Enabled || original.Revision != 0 || original.Outcome != "disabled" {
		t.Fatal("policy enabled by default", original)
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
	provider := &nativeStatusFake{status: issuetracker.Status{Provider: "github", URL: link.URL, ID: "42", State: "open"}}
	apidserver.IssueNativeSetClients(e, gh, provider)
	snapshot := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	in := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedStatus: &snapshot, ExpectedIssueState: "open"}
	enabled := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, in, nil), 200)
	if !enabled.Enabled || enabled.Revision != 1 || enabled.Outcome != "matched" || provider.writes != 0 {
		t.Fatal("opt-in policy invalid", enabled)
	}
	if w := e.Do(t, "PUT", path, in, nil); w.Code != 409 {
		t.Fatal("stale policy revision accepted", w.Code, w.Body.String())
	}
	policies := store.(state.IssueTrackerAutoSyncStore)
	var wg sync.WaitGroup
	claims := make(chan state.IssueTrackerAutoSyncRecord, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := policies.ClaimIssueTrackerAutoSync(t.Context())
			if err == nil {
				claims <- r
			} else if !errors.Is(err, state.ErrNoIssueTrackerAutoSyncWork) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(claims)
	if len(claims) != 1 {
		t.Fatal("multiple workers claimed one reference", len(claims))
	}
	claim := <-claims
	if _, err := policies.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", issuePath+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID}, nil), 200)
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	applied := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path, nil, nil), 200)
	if applied.Outcome != "applied" || applied.ProviderState != "resolved" || provider.writes != 1 || provider.status.State != "resolved" || applied.PendingState != "" {
		t.Fatal("background resolution failed", applied)
	}
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	claim, err := policies.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	disabled := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, api.UpdateIssueTrackerAutoSyncRequest{ExpectedRevision: 1}, nil), 200)
	if disabled.Enabled || disabled.Revision != 2 {
		t.Fatal(disabled)
	}
	if err := policies.CheckIssueTrackerAutoSyncClaim(t.Context(), claim); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		t.Fatal("disable retained claim", err)
	}
	if _, err := policies.FinishIssueTrackerAutoSync(t.Context(), claim); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		t.Fatal("stale worker resurrected disabled policy", err)
	}
	snapshot = apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	in.ExpectedRevision = 2
	in.ExpectedIssueState = "resolved"
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, in, nil), 200)
	provider.status.State = "open"
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	paused := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path, nil, nil), 200)
	if paused.Outcome != "conflict" || paused.Reason != "provider_changed" || provider.writes != 1 || paused.NextCheckAt != nil {
		t.Fatal("remote edit overwritten", paused)
	}
	reads := provider.reads
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	if provider.reads != reads {
		t.Fatal("paused policy automatically resumed")
	}
	provider.status.State = "resolved"
	snapshot = apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	in.ExpectedRevision = 3
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, in, nil), 200)
	other := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "OtherAutoError", Message: "another"}, reporter), 202)
	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+"/issues/"+other.IssueID+"/actions", api.IssueActionRequest{Action: "merge", TargetIssueID: accepted.IssueID}, nil), 200)
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	regrouped := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path, nil, nil), 200)
	if regrouped.Outcome != "conflict" || provider.writes != 1 {
		t.Fatal("regrouped source retained authority", regrouped)
	}
	foreign := apidserver.SeedPGApp(t, e, "native-auto-foreign")
	if w := e.Do(t, "GET", "/v1/apps/"+foreign.Slug+"/issues/"+other.IssueID+"/tracker-links/"+link.ID+"/auto-sync", nil, nil); w.Code != 404 {
		t.Fatal("cross-app policy exposed", w.Code)
	}
	// Disabling remains available after installation visibility is revoked.
	gh.repoID = 200
	if w := e.Do(t, "PUT", path, api.UpdateIssueTrackerAutoSyncRequest{ExpectedRevision: 4}, nil); w.Code != 200 {
		t.Fatal("revoked connection could not disable", w.Code, w.Body.String())
	}
	// A legacy tombstone may have status=deleted without DeletedAt. It must not dispatch.
	gh.repoID = 100
	snapshot = apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	in.ExpectedRevision = 5
	in.ExpectedIssueState = snapshot.Status.State
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, in, nil), 200)
	pgStore := store.(*state.PgStore)
	current, err := store.AppByID(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := pgStore.CompareAndSetAppStatus(t.Context(), app.ID, current.Status, state.AppDeleted); err != nil || !ok {
		t.Fatal(err)
	}
	mints := gh.mints
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	tombstoned, err := policies.GetIssueTrackerAutoSync(t.Context(), app.ID, acct.ID, accepted.IssueID, link.ID)
	if err != nil || tombstoned.Policy.Outcome != "blocked" || gh.mints != mints {
		t.Fatal("deleted app used GitHub credentials", tombstoned.Policy, err)
	}
	// Restoring the app does not silently resume; an owner reviews and re-enables.
	if ok, err := pgStore.CompareAndSetAppStatus(t.Context(), app.ID, state.AppDeleted, current.Status); err != nil || !ok {
		t.Fatal(err)
	}
	in.ExpectedRevision = 6
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path, in, nil), 200)
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	mints = gh.mints
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	downgraded, err := policies.GetIssueTrackerAutoSync(t.Context(), app.ID, acct.ID, accepted.IssueID, link.ID)
	if err != nil || downgraded.Policy.Outcome != "blocked" || gh.mints != mints {
		t.Fatal("downgrade used GitHub credentials", downgraded.Policy, err)
	}

}

type nativeLinearFake struct {
	calls int
	fail  bool
}

func (p *nativeLinearFake) VerifyLinearConnection(_ context.Context, token, team string) (issuetracker.LinearIdentity, error) {
	p.calls++
	if p.fail {
		return issuetracker.LinearIdentity{}, errors.New("lin_api_secret provider diagnostic")
	}
	return issuetracker.LinearIdentity{WorkspaceID: "11111111-1111-4111-8111-111111111111", ViewerID: "33333333-3333-4333-8333-333333333333", TeamID: team}, nil
}

// adr: 1215
func TestNativeIssueLinearConnectionScopesSealsAndDisconnects(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-connection")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	path := "/v1/apps/" + app.Slug + "/issue-tracker-linear-connection"
	provider := &nativeLinearFake{}
	apidserver.IssueNativeSetLinearVerifier(e, provider)
	identity, _ := age.GenerateX25519Identity()
	previous := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = previous })
	initial := apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "GET", path, nil, nil), 200)
	if initial.Connected || initial.Revision != 0 {
		t.Fatal("default connection enabled")
	}
	in := api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: "22222222-2222-4222-8222-222222222222", APIKey: "lin_api_secret"}
	for _, method := range []string{"GET", "PUT"} {
		w := e.Do(t, method, path, in, reporter)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("reporter connection access", w.Code)
		}
	}
	w := e.Do(t, "PUT", path, in, nil)
	out := apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, w, 200)
	if !out.Connected || out.Revision != 1 || strings.Contains(w.Body.String(), in.APIKey) {
		t.Fatal("invalid public connection", w.Body.String())
	}
	store := apidserver.IssueNativeStore(e).(state.IssueTrackerLinearStore)
	acct := apidserver.IssueNativeAccount(e)
	record, err := store.GetIssueTrackerLinearConnection(t.Context(), app.ID, acct.ID)
	if err != nil || strings.Contains(string(record.SealedCredential), in.APIKey) {
		t.Fatal("credential not sealed", err)
	}
	scope := issuetrackers.LinearCredentialScope{AccountID: acct.ID, AppID: app.ID, Identity: issuetracker.LinearIdentity{WorkspaceID: out.WorkspaceID, TeamID: out.TeamID, ViewerID: out.ViewerID}}
	token, err := issuetrackers.OpenLinearCredential([]*age.X25519Identity{identity}, scope, record.SealedCredential)
	if err != nil || token != in.APIKey {
		t.Fatal("sealed identity invalid", err)
	}
	if w := e.Do(t, "PUT", path, in, nil); w.Code != 409 {
		t.Fatal("stale revision accepted", w.Code, w.Body.String())
	}
	foreign := apidserver.SeedPGApp(t, e, "linear-other")
	if _, err := store.GetIssueTrackerLinearConnection(t.Context(), foreign.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign owner read", err)
	}
	// Concurrent contenders sharing one observed revision have exactly one winner.
	contenders := 8
	var wg sync.WaitGroup
	results := make(chan error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ConfigureIssueTrackerLinearConnection(t.Context(), record, 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("connection revision race", wins)
	}
	provider.fail = true
	in.ExpectedRevision = 2
	if w := e.Do(t, "PUT", path, in, nil); w.Code != 400 || strings.Contains(w.Body.String(), in.APIKey) {
		t.Fatal("provider failure leaked or accepted", w.Code, w.Body.String())
	}
	before := provider.calls
	if err := apidserver.IssueNativeStore(e).UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, "PUT", path, in, nil); w.Code != 402 || provider.calls != before {
		t.Fatal("downgrade authorized provider access", w.Code)
	}
	*apidserver.SetSecretRecipient = nil
	disconnected := apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", path, api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	if disconnected.Connected || disconnected.Revision != 3 || disconnected.TeamID != "" || provider.calls != before {
		t.Fatal("offline disconnect failed", disconnected)
	}
	record, err = store.GetIssueTrackerLinearConnection(t.Context(), app.ID, acct.ID)
	if err != nil || len(record.SealedCredential) != 0 {
		t.Fatal("disconnect retained credential", err)
	}
}
