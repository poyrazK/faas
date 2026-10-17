//go:build !no_pg

package apidserver_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
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
	readErr       error
	status        issuetracker.Status
	reads, writes int
}

func (p *nativeStatusFake) ReadStatus(_ context.Context, _ string, token string) (issuetracker.Status, error) {
	p.reads++
	return p.status, p.readErr
}
func (p *nativeStatusFake) SetStatus(_ context.Context, expected issuetracker.Status, token, wanted, _ string) (issuetracker.Status, error) {
	if p.status != expected {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	p.writes++
	p.status.State = wanted
	return p.status, nil
}

// adr: 1466
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

// adr: 1467
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

// adr: 1468
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
	if original.Enabled || original.Revision != 0 || original.Outcome != "disabled" || original.Direction != "outbound" {
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

// adr: 1470
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

type nativeLinearCreateFake struct {
	creates, recovers int
	uncertain         bool
	requestID         string
}

func (p *nativeLinearCreateFake) Create(_ context.Context, r issuetracker.Request, token string) (issuetracker.Ticket, error) {
	p.creates++
	p.requestID = r.RequestID
	if token != "lin_api_secret" || r.Provider != "linear" {
		return issuetracker.Ticket{}, errors.New("wrong credential or provider")
	}
	if p.uncertain {
		return issuetracker.Ticket{}, errors.New("lin_api_secret transport diagnostics")
	}
	return issuetracker.Ticket{URL: "https://linear.app/acme/issue/PAY-42", ProviderID: r.RequestID}, nil
}
func (p *nativeLinearCreateFake) Recover(_ context.Context, r issuetracker.Request, token, url string) (issuetracker.Ticket, error) {
	p.recovers++
	if token != "lin_api_secret" || r.RequestID != p.requestID || url != "" {
		return issuetracker.Ticket{}, errors.New("wrong UUID recovery inputs")
	}
	return issuetracker.Ticket{URL: "https://linear.app/acme/issue/PAY-42", ProviderID: r.RequestID}, nil
}

// adr: 1471
func TestNativeIssueLinearCreationRecoversUUIDAndRejectsRevokedConnections(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-native-create")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "LinearCreateError", Message: "failed"}, reporter), 202)
	path := base + "/issues/" + accepted.IssueID + "/native-linear-tickets"
	in := api.CreateNativeLinearIssueTicketRequest{RequestID: uuid.NewString(), TeamID: "22222222-2222-4222-8222-222222222222", Title: "Fix payments"}
	if w := e.Do(t, "POST", path, in, reporter); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporter reached stored credential", w.Code)
	}
	identity, _ := age.GenerateX25519Identity()
	prevRecipient, prevIdentities := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = prevRecipient; *apidserver.MfaIdentities = prevIdentities })
	verifier := &nativeLinearFake{}
	apidserver.IssueNativeSetLinearVerifier(e, verifier)
	connectionPath := base + "/issue-tracker-linear-connection"
	connection := api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: in.TeamID, APIKey: "lin_api_secret"}
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, connection, nil), 200)
	provider := &nativeLinearCreateFake{uncertain: true}
	apidserver.IssueNativeSetTicketProvider(e, provider)
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 || provider.creates != 1 || strings.Contains(w.Body.String(), "lin_api_secret") {
		t.Fatal("unknown dispatch was not retained", w.Code, w.Body.String())
	}
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("uncertain creation duplicated", w.Code)
	}
	in.Recover = true
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 || provider.recovers != 0 {
		t.Fatal("active dispatch lease ignored", w.Code)
	}
	acct := apidserver.IssueNativeAccount(e)
	if err := apidserver.IssueNativeTicketExpire(e, t.Context(), acct.ID, in.RequestID); err != nil {
		t.Fatal(err)
	}
	// A replacement credential with unchanged provider identity preserves recovery.
	connection.ExpectedRevision = 1
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, connection, nil), 200)
	result := apidserver.IssueDecode[api.NativeIssueTicketResult](t, e.Do(t, "POST", path, in, nil), 200)
	if result.State != "linked" || result.RequestID != in.RequestID || provider.creates != 1 || provider.recovers != 1 {
		t.Fatal("UUID recovery failed", result)
	}
	apidserver.IssueDecode[api.NativeIssueTicketResult](t, e.Do(t, "POST", path, in, nil), 200)
	if provider.recovers != 1 {
		t.Fatal("durable result dispatched twice")
	}
	in.Title = "changed"
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 {
		t.Fatal("changed inputs reused receipt", w.Code)
	}
	in.Title = "Fix payments"
	in.Recover = false
	in.RequestID = uuid.NewString()
	wrong := in
	wrong.TeamID = uuid.NewString()
	if w := e.Do(t, "POST", path, wrong, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("foreign team dispatched", w.Code)
	}
	verifier.fail = true
	if w := e.Do(t, "POST", path, in, nil); w.Code != 502 || provider.creates != 1 || strings.Contains(w.Body.String(), "lin_api_secret") {
		t.Fatal("revoked key dispatched or leaked", w.Code, w.Body.String())
	}
	verifier.fail = false
	foreign := apidserver.SeedPGApp(t, e, "linear-native-foreign")
	if w := e.Do(t, "POST", "/v1/apps/"+foreign.Slug+"/issues/"+accepted.IssueID+"/native-linear-tickets", in, nil); w.Code != 404 {
		t.Fatal("cross-app issue reached connection", w.Code)
	}
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	if w := e.Do(t, "POST", path, in, nil); w.Code != 409 || provider.creates != 1 {
		t.Fatal("disconnected credential dispatched", w.Code)
	}
}

type nativeLinearLifecycleFake struct {
	readErr       error
	status        issuetracker.Status
	reads, writes int
	unsupported   bool
	uncertain     bool
	workflowErr   bool
	workflows     []issuetracker.LinearWorkflowState
	workflowReads int
	readHook      func()
}

func (p *nativeLinearLifecycleFake) ListLinearWorkflowStates(_ context.Context, token, team string) ([]issuetracker.LinearWorkflowState, error) {
	p.workflowReads++
	if token != "lin_api_secret" || team != p.status.TeamID {
		return nil, errors.New("wrong workflow scope")
	}
	return p.workflows, nil
}
func (p *nativeLinearLifecycleFake) VerifyLinearWorkflowState(_ context.Context, token, team, id, wanted string) error {
	if token != "lin_api_secret" || team != p.status.TeamID || p.workflowErr {
		return errors.New("workflow unavailable")
	}
	return nil
}
func (p *nativeLinearLifecycleFake) ReadStatus(_ context.Context, _ string, token string) (issuetracker.Status, error) {
	p.reads++
	if p.readHook != nil {
		p.readHook()
	}
	if token != "lin_api_secret" {
		return issuetracker.Status{}, errors.New("wrong credential")
	}
	if p.readErr != nil {
		return issuetracker.Status{}, p.readErr
	}
	if p.unsupported {
		return issuetracker.Status{}, issuetracker.ErrUnsupportedStatus
	}
	return p.status, nil
}
func (p *nativeLinearLifecycleFake) SetStatus(_ context.Context, expected issuetracker.Status, token, wanted, selected string) (issuetracker.Status, error) {
	if token != "lin_api_secret" {
		return issuetracker.Status{}, errors.New("wrong credential")
	}
	if p.workflowErr {
		return issuetracker.Status{}, errors.New("selected workflow unavailable")
	}
	if expected != p.status {
		return issuetracker.Status{}, issuetracker.ErrStatusChanged
	}
	if p.status.State != wanted {
		p.writes++
		p.status.State = wanted
		p.status.StateID = selected
	}
	if p.uncertain {
		return issuetracker.Status{}, errors.New("unknown outcome")
	}
	return p.status, nil
}

// adr: 1472
func TestNativeIssueLinearStatusPinsPreviewAndRequiresWorkflowSelection(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-native-status")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "LinearStatusError", Message: "failed"}, reporter), 202)
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", base+"/issues/"+accepted.IssueID+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-42"}, nil), 200)
	path := base + "/issues/" + accepted.IssueID + "/tracker-links/" + link.ID + "/native-linear-status"
	if w := e.Do(t, "GET", path, nil, reporter); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporter read stored provider credentials", w.Code)
	}
	identity, _ := age.GenerateX25519Identity()
	oldRecipient, oldIDs := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIDs })
	verifier := &nativeLinearFake{}
	apidserver.IssueNativeSetLinearVerifier(e, verifier)
	connection := api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: uuid.NewString(), APIKey: "lin_api_secret"}
	connectionPath := base + "/issue-tracker-linear-connection"
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, connection, nil), 200)
	provider := &nativeLinearLifecycleFake{status: issuetracker.Status{Provider: "linear", URL: link.URL, ID: uuid.NewString(), TeamID: connection.TeamID, StateID: uuid.NewString(), State: "resolved"}}
	apidserver.IssueNativeSetClients(e, nil, provider)
	preview := apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "GET", path, nil, nil), 200)
	input := api.UpdateNativeLinearIssueTrackerStatusRequest{DesiredState: "open", Expected: preview.Status, ExpectedConnectionRevision: preview.ConnectionRevision, ExpectedGroupFingerprint: preview.GroupFingerprint}
	if w := e.Do(t, "POST", path, input, nil); w.Code != 400 || provider.writes != 0 {
		t.Fatal("implicit workflow state allowed", w.Code)
	}
	input.LinearStateID = uuid.NewString()
	result := apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "POST", path, input, nil), 200)
	if result.Status.State != "open" || result.Status.StateID != input.LinearStateID || provider.writes != 1 {
		t.Fatal("selected workflow not used", result)
	}
	if w := e.Do(t, "POST", path, input, nil); w.Code != 409 || provider.writes != 1 {
		t.Fatal("stale provider snapshot accepted", w.Code)
	}
	input.Expected = result.Status
	input.ExpectedGroupFingerprint = strings.Repeat("b", 64)
	if w := e.Do(t, "POST", path, input, nil); w.Code != 409 || provider.writes != 1 {
		t.Fatal("stale group accepted", w.Code)
	}
	input.ExpectedGroupFingerprint = result.GroupFingerprint
	connection.ExpectedRevision = 1
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, connection, nil), 200)
	if w := e.Do(t, "POST", path, input, nil); w.Code != 409 || provider.writes != 1 {
		t.Fatal("stale connection revision accepted", w.Code)
	}
	provider.unsupported = true
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 409 {
		t.Fatal("unsupported provider state exposed as a resolution", w.Code)
	}
	provider.unsupported = false
	provider.status.TeamID = uuid.NewString()
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 502 || provider.writes != 1 {
		t.Fatal("foreign team evidence accepted", w.Code)
	}
	provider.status.TeamID = connection.TeamID
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connectionPath, api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 409 {
		t.Fatal("disconnected provider read", w.Code)
	}
}

// adr: 1473
func TestNativeIssueLinearAutoSyncPersistsMappingsAndRecoversUncertainWrites(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-auto-sync")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	fixed := apidserver.IssueSeedDeployment(t, e, app, 2)
	reporterToken := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + reporterToken.Token}
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "LinearAutoError", Message: "failed"}, reporter), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-42"}, nil), 200)
	path := issuePath + "/tracker-links/" + link.ID
	identity, _ := age.GenerateX25519Identity()
	oldRecipient, oldIDs := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIDs })
	apidserver.IssueNativeSetLinearVerifier(e, &nativeLinearFake{})
	conn := api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: uuid.NewString(), APIKey: "lin_api_secret"}
	connPath := base + "/issue-tracker-linear-connection"
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connPath, conn, nil), 200)
	provider := &nativeLinearLifecycleFake{status: issuetracker.Status{Provider: "linear", URL: link.URL, ID: uuid.NewString(), TeamID: conn.TeamID, StateID: uuid.NewString(), State: "open"}}
	apidserver.IssueNativeSetClients(e, nil, provider)
	preview := apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "GET", path+"/native-linear-status", nil, nil), 200)
	policy := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Enabled || policy.Revision != 0 {
		t.Fatal(policy)
	}
	in := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedLinearStatus: &preview, ExpectedIssueState: "open", LinearOpenStateID: provider.status.StateID, LinearResolvedStateID: uuid.NewString()}
	if w := e.Do(t, "PUT", path+"/auto-sync", in, reporter); w.Code != 401 && w.Code != 403 {
		t.Fatal("reporter delegated provider writes", w.Code)
	}
	provider.workflowErr = true
	if w := e.Do(t, "PUT", path+"/auto-sync", in, nil); w.Code != 502 {
		t.Fatal("invalid workflow enabled", w.Code, w.Body.String())
	}
	provider.workflowErr = false
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path+"/auto-sync", in, nil), 200)
	if policy.Revision != 1 || policy.LinearResolvedStateID != in.LinearResolvedStateID || provider.writes != 0 {
		t.Fatal(policy)
	}
	if w := e.Do(t, "PUT", path+"/auto-sync", in, nil); w.Code != 409 {
		t.Fatal("stale policy replaced", w.Code)
	}
	apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", issuePath+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID}, nil), 200)
	provider.uncertain = true
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Outcome != "error" || policy.PendingState != "resolved" || provider.writes != 1 {
		t.Fatal(policy, provider.writes)
	}
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Outcome != "applied" || policy.PendingState != "" || provider.writes != 1 {
		t.Fatal("uncertain write repeated", policy, provider.writes)
	}
	conn.ExpectedRevision = 1
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connPath, conn, nil), 200)
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Outcome != "conflict" || policy.NextCheckAt != nil || provider.writes != 1 {
		t.Fatal("replacement connection retained delegation", policy)
	}
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", connPath, api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path+"/auto-sync", api.UpdateIssueTrackerAutoSyncRequest{ExpectedRevision: policy.Revision}, nil), 200)
	if policy.Enabled || policy.Outcome != "disabled" || policy.Revision != 2 {
		t.Fatal("offline disable failed", policy)
	}
}

func TestNativeIssueDashboardTrackerSyncReviewConnectAndRecovery(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "dashboard-tracker-sync")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DashboardSync", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-42"}, nil), 200)
	identity, _ := age.GenerateX25519Identity()
	oldRecipient, oldIDs := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIDs })
	apidserver.IssueNativeSetLinearVerifier(e, &nativeLinearFake{})
	team := uuid.NewString()
	open, resolved := uuid.NewString(), uuid.NewString()
	provider := &nativeLinearLifecycleFake{status: issuetracker.Status{Provider: "linear", URL: link.URL, ID: uuid.NewString(), TeamID: team, StateID: resolved, State: "resolved"}}
	provider.workflows = []issuetracker.LinearWorkflowState{{ID: open, TeamID: team, Name: "In progress", Type: "started", State: "open"}, {ID: resolved, TeamID: team, Name: "Done", Type: "completed", State: "resolved"}}
	apidserver.IssueNativeSetClients(e, nil, provider)
	page := "/dashboard/apps/" + app.Slug + "/issues?issue=" + accepted.IssueID
	get := httptest.NewRequest("GET", page, nil)
	e.AddAdminSession(t, get)
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, get)
	if response.Code != 200 || provider.reads != 0 || provider.workflowReads != 0 || !strings.Contains(response.Body.String(), "Refresh provider status and review sync") {
		t.Fatal("page load fetched provider or omitted controls", response.Code, response.Body.String())
	}
	var csrf *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == apidserver.DashboardIssueCookie {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF")
	}
	formPath := "/dashboard/apps/" + app.Slug + "/issues/" + accepted.IssueID
	post := func(suffix string, values url.Values, validCSRF bool) *httptest.ResponseRecorder {
		if validCSRF {
			values.Set("csrf_token", csrf.Value)
		}
		req := httptest.NewRequest("POST", formPath+suffix, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range get.Cookies() {
			req.AddCookie(cookie)
		}
		req.AddCookie(csrf)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	previewForm := func() url.Values {
		return url.Values{"action": {"preview"}, "link_id": {link.ID}, "provider": {"linear"}}
	}
	if out := post("/tracker-sync", previewForm(), false); out.Code != 400 || provider.reads != 0 {
		t.Fatal("CSRF bypass", out.Code)
	}
	connection := url.Values{"action": {"connect"}, "expected_revision": {"0"}, "team_id": {team}, "api_key": {"lin_api_secret"}}
	out := post("/linear-connection", connection, true)
	if out.Code != 303 || strings.Contains(out.Body.String(), "lin_api_secret") || strings.Contains(out.Header().Get("Location"), "secret") {
		t.Fatal("connection failed or key leaked", out.Code, out.Body.String())
	}
	if out := post("/linear-connection", connection, true); out.Code != 409 {
		t.Fatal("stale connection form accepted", out.Code)
	}
	preview := post("/tracker-sync", previewForm(), true)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Push Gregale open") || strings.Contains(preview.Body.String(), `name="action" value="enable"`) {
		t.Fatal("unaligned review invalid", preview.Code, preview.Body.String())
	}
	expected := func(out *httptest.ResponseRecorder) string {
		match := regexp.MustCompile(`name="expected" value="([^"]*)"`).FindStringSubmatch(out.Body.String())
		if len(match) != 2 {
			t.Fatal("missing preview snapshot", out.Body.String())
		}
		return html.UnescapeString(match[1])
	}
	if !strings.Contains(preview.Body.String(), `<select name="linear_state_id"`) || !strings.Contains(preview.Body.String(), "In progress (started)") {
		t.Fatal("named alignment picker missing")
	}
	push := url.Values{"action": {"push"}, "provider": {"linear"}, "link_id": {link.ID}, "source_state": {"open"}, "expected_revision": {"0"}, "expected": {expected(preview)}, "linear_state_id": {open}}
	provider.workflowErr = true
	if out := post("/tracker-sync", push, true); out.Code != 502 || provider.writes != 0 {
		t.Fatal("stale selected workflow mutated provider", out.Code)
	}
	provider.workflowErr = false
	if out := post("/tracker-sync", push, true); out.Code != 303 || provider.writes != 1 || provider.status.StateID != open {
		t.Fatal("manual alignment failed", out.Code, out.Body.String())
	}
	preview = post("/tracker-sync", previewForm(), true)
	enable := url.Values{"action": {"enable"}, "provider": {"linear"}, "link_id": {link.ID}, "source_state": {"open"}, "expected_revision": {"0"}, "expected": {expected(preview)}, "linear_open_state_id": {open}, "linear_resolved_state_id": {resolved}}
	if !strings.Contains(preview.Body.String(), `<select name="linear_open_state_id"`) || !strings.Contains(preview.Body.String(), "Done (completed)") {
		t.Fatal("named delegation pickers missing")
	}
	provider.workflowErr = true
	if out := post("/tracker-sync", enable, true); out.Code != 502 || provider.writes != 1 {
		t.Fatal("stale mapping enabled", out.Code)
	}
	provider.workflowErr = false
	if out := post("/tracker-sync", enable, true); out.Code != 303 || provider.writes != 1 {
		t.Fatal("delegation failed", out.Code, out.Body.String())
	}
	saved := post("/tracker-sync", previewForm(), true)
	if saved.Code != 200 || !strings.Contains(saved.Body.String(), "Configured open workflow: <strong>In progress</strong>") || !strings.Contains(saved.Body.String(), "<strong>Done</strong>") {
		t.Fatal("stored workflow names missing", saved.Code, saved.Body.String())
	}

	provider.status.State, provider.status.StateID = "resolved", resolved
	inbound := post("/tracker-sync", previewForm(), true)
	if inbound.Code != 200 || !strings.Contains(inbound.Body.String(), "Inbound sync preview") || !strings.Contains(inbound.Body.String(), "needs_deployment") || !strings.Contains(inbound.Body.String(), "explicitly selected fixing deployment") || provider.writes != 1 {
		t.Fatal("inbound resolution assessment missing or wrote provider", inbound.Code, inbound.Body.String())
	}
	inboundPolicy := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", issuePath+"/tracker-links/"+link.ID+"/auto-sync", nil, nil), 200)
	if inboundPolicy.Revision != 1 || !inboundPolicy.Enabled || inboundPolicy.ProviderState != "open" {
		t.Fatal("preview changed policy", inboundPolicy)
	}
	provider.status.State, provider.status.StateID = "open", open
	if out := post("/tracker-sync", enable, true); out.Code != 409 {
		t.Fatal("stale policy form accepted", out.Code)
	}
	push.Set("expected_revision", "1")
	if out := post("/tracker-sync", push, true); out.Code != 409 || provider.writes != 1 {
		t.Fatal("manual push while delegated allowed", out.Code)
	}
	enable.Set("expected_revision", "1")
	provider.status.StateID = uuid.NewString()
	if out := post("/tracker-sync", enable, true); out.Code != 409 {
		t.Fatal("stale provider preview accepted", out.Code)
	}
	disconnected := post("/linear-connection", url.Values{"action": {"disconnect"}, "expected_revision": {"1"}}, true)
	if disconnected.Code != 303 {
		t.Fatal("disconnect failed", disconnected.Code, disconnected.Body.String())
	}
	reads := provider.reads
	disabled := post("/tracker-sync", url.Values{"action": {"disable"}, "link_id": {link.ID}, "expected_revision": {"1"}}, true)
	if disabled.Code != 303 || provider.reads != reads {
		t.Fatal("disable needs provider access", disabled.Code, disabled.Body.String())
	}
	policy := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", issuePath+"/tracker-links/"+link.ID+"/auto-sync", nil, nil), 200)
	if policy.Enabled || policy.Revision != 2 {
		t.Fatal(policy)
	}
	// A reference from another app cannot be used in this app's review.
	other := apidserver.SeedPGApp(t, e, "dashboard-sync-other")
	req := httptest.NewRequest("POST", "/dashboard/apps/"+other.Slug+"/issues/"+accepted.IssueID+"/tracker-sync", strings.NewReader(url.Values{"action": {"preview"}, "link_id": {link.ID}, "provider": {"linear"}, "csrf_token": {csrf.Value}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range get.Cookies() {
		req.AddCookie(cookie)
	}
	req.AddCookie(csrf)
	denied := httptest.NewRecorder()
	(*e.H()).ServeHTTP(denied, req)
	if denied.Code != 404 {
		t.Fatal("cross-app reference exposed", denied.Code)
	}

	// Restore a connection and an aligned baseline for explicit inbound review.
	connection.Set("expected_revision", "2")
	if out := post("/linear-connection", connection, true); out.Code != 303 {
		t.Fatal("reconnect", out.Code, out.Body.String())
	}
	provider.status.State, provider.status.StateID = "open", open
	preview = post("/tracker-sync", previewForm(), true)
	enable.Set("direction", "inbound")
	enable.Set("expected_revision", "2")
	enable.Set("expected", expected(preview))
	if out := post("/tracker-sync", enable, true); out.Code != 303 {
		t.Fatal("enable inbound fixture", out.Code, out.Body.String())
	}
	provider.status.State, provider.status.StateID = "resolved", resolved
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	held := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", issuePath+"/tracker-links/"+link.ID+"/auto-sync", nil, nil), 200)
	if held.Outcome != "blocked" || held.Reason != "fixing_deployment_required" || held.ObservedState != "resolved" {
		t.Fatal("inbound resolution was not held", held)
	}
	pullForm := func() url.Values {
		reviewed := post("/tracker-sync", previewForm(), true)
		if reviewed.Code != 200 || !strings.Contains(reviewed.Body.String(), `name="fixed_deployment_id"`) || !strings.Contains(reviewed.Body.String(), dep.ID) {
			t.Fatal("deployment picker missing", reviewed.Code, reviewed.Body.String())
		}
		guard := regexp.MustCompile(`name="inbound_guard_revision" value="([0-9]+)"`).FindStringSubmatch(reviewed.Body.String())
		if len(guard) != 2 {
			t.Fatal("guard missing", reviewed.Body.String())
		}
		return url.Values{"action": {"pull"}, "provider": {"linear"}, "link_id": {link.ID}, "expected_revision": {"3"}, "inbound_guard_revision": {guard[1]}, "expected": {expected(reviewed)}, "fixed_deployment_id": {dep.ID}}
	}
	pull := pullForm()
	if out := post("/tracker-sync", pull, false); out.Code != 400 {
		t.Fatal("pull bypassed CSRF", out.Code)
	}
	pull.Del("fixed_deployment_id")
	if out := post("/tracker-sync", pull, true); out.Code != 400 {
		t.Fatal("resolution lacked deployment", out.Code)
	}
	pull.Set("fixed_deployment_id", dep.ID)
	// Change away and back *during* the fresh provider read. Lifecycle strings
	// and group fingerprints match again; only the durable guard detects this.
	provider.readHook = func() {
		provider.readHook = nil
		apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", issuePath+"/actions", api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, nil), 200)
		apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", issuePath+"/actions", api.IssueActionRequest{Action: "reopen"}, nil), 200)
	}
	if out := post("/tracker-sync", pull, true); out.Code != 409 {
		t.Fatal("ABA during provider read applied", out.Code, out.Body.String())
	}
	pull = pullForm()
	provider.status.State, provider.status.StateID = "open", open
	if out := post("/tracker-sync", pull, true); out.Code != 409 {
		t.Fatal("changed provider applied", out.Code)
	}
	provider.status.State, provider.status.StateID = "resolved", resolved
	foreignDep := apidserver.IssueSeedDeployment(t, e, other, 1)
	pull = pullForm()
	pull.Set("fixed_deployment_id", foreignDep.ID)
	if out := post("/tracker-sync", pull, true); out.Code != 404 {
		t.Fatal("foreign deployment applied", out.Code, out.Body.String())
	}
	pull.Set("fixed_deployment_id", dep.ID)
	// A repeated occurrence changes counters without invalidating intent.
	apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "DashboardSync", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	if out := post("/tracker-sync", pull, true); out.Code != 303 || provider.writes != 1 {
		t.Fatal("pull failed or wrote provider", out.Code, out.Body.String())
	}
	if out := post("/tracker-sync", pull, true); out.Code != 409 {
		t.Fatal("pull replay accepted", out.Code)
	}
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", issuePath+"/tracker-links/"+link.ID+"/auto-sync", nil, nil), 200)
	if policy.Enabled || policy.Revision != 4 || policy.ProviderState != "resolved" || policy.SourceState != "resolved" {
		t.Fatal("pull did not atomically align and pause", policy)
	}
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
	audited := false
	for _, a := range detail.Activity {
		if a.Action == "resolved" && a.Details["tracker_direction"] == "pull" && a.Details["tracker_link_id"] == link.ID && a.Details["fixed_deployment_id"] == dep.ID {
			audited = true
		}
	}
	if !audited || detail.Issue.State != "resolved" {
		t.Fatal("pull audit missing", detail.Activity)
	}
	// Re-enable only after explicit alignment review, then pull a reopening.
	preview = post("/tracker-sync", previewForm(), true)
	enable.Set("expected_revision", "4")
	enable.Set("source_state", "resolved")
	enable.Set("expected", expected(preview))
	if out := post("/tracker-sync", enable, true); out.Code != 303 {
		t.Fatal("reenable after pull", out.Code, out.Body.String())
	}
	provider.status.State, provider.status.StateID = "open", open
	reopenedPreview := post("/tracker-sync", previewForm(), true)
	guard := regexp.MustCompile(`name="inbound_guard_revision" value="([0-9]+)"`).FindStringSubmatch(reopenedPreview.Body.String())
	if len(guard) != 2 {
		t.Fatal("reopen preview unavailable", reopenedPreview.Body.String())
	}
	reopening := url.Values{"action": {"pull"}, "provider": {"linear"}, "link_id": {link.ID}, "expected_revision": {"5"}, "inbound_guard_revision": {guard[1]}, "expected": {expected(reopenedPreview)}}
	if out := post("/tracker-sync", reopening, true); out.Code != 303 || provider.writes != 1 {
		t.Fatal("reopen pull failed", out.Code, out.Body.String())
	}
}

// adr: 1476
func TestNativeIssueLinearCallbacksAreScopedDurableHintsAndPreserveClaimWakeups(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-callbacks")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "CallbackError", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-42"}, nil), 200)
	identity, _ := age.GenerateX25519Identity()
	oldRecipient, oldIDs := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIDs })
	apidserver.IssueNativeSetLinearVerifier(e, &nativeLinearFake{})
	secret := "linear_signing_secret_1234567890abcdef"
	connection := apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", base+"/issue-tracker-linear-connection", api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: uuid.NewString(), APIKey: "lin_api_secret", WebhookSecret: secret}, nil), 200)
	if !connection.WebhookEnabled || connection.CallbackPath != "/hooks/issue-trackers/linear/"+app.ID {
		t.Fatal(connection)
	}
	provider := &nativeLinearLifecycleFake{status: issuetracker.Status{Provider: "linear", URL: link.URL, ID: uuid.NewString(), TeamID: connection.TeamID, StateID: uuid.NewString(), State: "open"}}
	apidserver.IssueNativeSetClients(e, nil, provider)
	path := issuePath + "/tracker-links/" + link.ID
	snapshot := apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "GET", path+"/native-linear-status", nil, nil), 200)
	input := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedLinearStatus: &snapshot, ExpectedIssueState: "open", LinearOpenStateID: provider.status.StateID, LinearResolvedStateID: uuid.NewString()}
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path+"/auto-sync", input, nil), 200)
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.NextCheckAt == nil || !policy.NextCheckAt.After(time.Now()) {
		t.Fatal("initial policy not delayed", policy)
	}
	callbacks := apidserver.IssueNativeStore(e).(state.IssueTrackerCallbackStore)
	healthStore := apidserver.IssueNativeStore(e).(state.IssueTrackerCallbackHealthStore)
	readHealth := func() issuetracker.CallbackHealth {
		t.Helper()
		health, err := healthStore.GetIssueTrackerCallbackHealth(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
		if err != nil {
			t.Fatal(err)
		}
		return health
	}
	if h := readHealth(); !h.Configured || h.LastVerifiedAt != nil {
		t.Fatal("configured connection already verified", h)
	}
	payload := map[string]any{"type": "Issue", "action": "update", "organizationId": connection.WorkspaceID, "webhookTimestamp": time.Now().UnixMilli(), "data": map[string]string{"id": provider.status.ID, "teamId": connection.TeamID, "updatedAt": "event-one", "state": "completed"}}
	delivery := uuid.NewString()
	send := func(id string, body map[string]any, valid bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(raw)
		sig := hex.EncodeToString(mac.Sum(nil))
		if !valid {
			sig = "bad"
		}
		req := httptest.NewRequest("POST", connection.CallbackPath, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Linear-Signature", sig)
		req.Header.Set("Linear-Delivery", id)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	if out := send(delivery, payload, false); out.Code != 401 {
		t.Fatal("unsigned callback accepted", out.Code)
	}
	if h := readHealth(); h.InvalidSignature != 1 || h.LastVerifiedAt != nil || h.LastRejectedAt == nil {
		t.Fatal("rejection changed setup proof", h)
	}
	first := send(delivery, payload, true)
	if first.Code != 200 || !strings.Contains(first.Body.String(), "accepted") || strings.Contains(first.Body.String(), secret) {
		t.Fatal(first.Code, first.Body.String())
	}
	checkHealth := func(retained, pending int64) {
		t.Helper()
		health, err := healthStore.GetIssueTrackerCallbackHealth(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
		if err != nil || health.RetainedDeliveries != retained || health.PendingHints != pending || (retained > 0 && health.LastReceivedAt == nil) {
			t.Fatal("callback health", health, err)
		}
	}
	checkHealth(1, 1)
	verified := readHealth().LastVerifiedAt
	if verified == nil || readHealth().Setup != "Verified delivery received within 24 hours" {
		t.Fatal("valid delivery did not verify setup")
	}

	if _, err := healthStore.GetIssueTrackerCallbackHealth(t.Context(), app.ID, uuid.NewString(), accepted.IssueID, link.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("health crossed account scope", err)
	}
	payload["webhookTimestamp"] = time.Now().Add(-10 * time.Minute).UnixMilli()
	if out := send(delivery, payload, true); out.Code != 200 || !strings.Contains(out.Body.String(), "duplicate") {
		t.Fatal("known late retry failed", out.Code, out.Body.String())
	}
	if h := readHealth(); h.LastVerifiedAt == nil || !h.LastVerifiedAt.Equal(*verified) || h.StaleDelivery != 0 {
		t.Fatal("known late retry changed setup proof", h)
	}
	payload["data"].(map[string]string)["updatedAt"] = "stale-new-event"
	if out := send(uuid.NewString(), payload, true); out.Code != 401 {
		t.Fatal("unknown stale event accepted", out.Code)
	}
	payload["webhookTimestamp"] = time.Now().UnixMilli()
	payload["data"].(map[string]string)["updatedAt"] = "event-one"
	payload["data"].(map[string]string)["teamId"] = uuid.NewString()
	if out := send(uuid.NewString(), payload, true); out.Code != 401 {
		t.Fatal("foreign team accepted", out.Code)
	}
	payload["data"].(map[string]string)["teamId"] = connection.TeamID
	if out := send("invalid-delivery", payload, true); out.Code != 401 {
		t.Fatal("malformed delivery accepted", out.Code)
	}
	payload["data"].(map[string]string)["updatedAt"] = "changed-payload"
	if out := send(delivery, payload, true); out.Code < 400 {
		t.Fatal("changed delivery accepted", out.Code)
	}
	payload["data"].(map[string]string)["updatedAt"] = "event-one"
	if h := readHealth(); h.InvalidSignature != 1 || h.WrongScope != 1 || h.StaleDelivery != 1 || h.InvalidPayload != 1 || h.DeliveryConflict != 1 {
		t.Fatal("rejection counters", h)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Outcome != "matched" || provider.writes != 0 {
		t.Fatal("signed payload overrode fresh provider state", policy)
	}
	checkHealth(1, 0)
	payload["data"].(map[string]string)["updatedAt"] = "event-two"
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	policies := apidserver.IssueNativeStore(e).(state.IssueTrackerAutoSyncStore)
	claim, err := policies.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out := send(uuid.NewString(), payload, true); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if processed, err := callbacks.ProcessIssueTrackerCallback(t.Context()); err != nil || processed {
		t.Fatal("hint consumed during live claim", processed, err)
	}
	if _, err := policies.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	if processed, err := callbacks.ProcessIssueTrackerCallback(t.Context()); err != nil || !processed {
		t.Fatal("in-flight hint lost", processed, err)
	}
	checkHealth(2, 0)
	provider.status.State = "resolved"
	provider.status.StateID = input.LinearResolvedStateID
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy = apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
	if policy.Outcome != "conflict" || policy.Reason != "provider_changed" || provider.writes != 0 || detail.Issue.State != "open" {
		t.Fatal("manual edit was overwritten or pulled", policy)
	}
	payload["data"].(map[string]string)["updatedAt"] = "event-three"
	if out := send(uuid.NewString(), payload, true); out.Code != 200 || !strings.Contains(out.Body.String(), "ignored") {
		t.Fatal("callback resumed paused policy", out.Code, out.Body.String())
	}
	secret = "replacement_signing_secret_1234567890abcdef"
	connection = apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", base+"/issue-tracker-linear-connection", api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, ExpectedRevision: 1, TeamID: connection.TeamID, APIKey: "lin_api_secret", WebhookSecret: secret}, nil), 200)
	diagnostics := apidserver.IssueNativeStore(e).(state.IssueTrackerCallbackDiagnosticStore)
	if err := diagnostics.RecordIssueTrackerCallbackDiagnostic(t.Context(), app.ID, app.AccountID, 1, "verified"); err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.RecordIssueTrackerCallbackDiagnostic(t.Context(), app.ID, uuid.NewString(), 2, "invalid_signature"); err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.RecordIssueTrackerCallbackDiagnostic(t.Context(), app.ID, app.AccountID, 2, "untrusted-reason"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("arbitrary diagnostic reason accepted", err)
	}
	if h := readHealth(); !h.Configured || h.LastVerifiedAt != nil || h.InvalidSignature != 0 || h.WrongScope != 0 || h.RetainedDeliveries != 0 {
		t.Fatal("replacement reused old evidence", h)
	}
	payload["webhookTimestamp"] = time.Now().UnixMilli()
	if out := send(uuid.NewString(), payload, true); out.Code != 200 || !strings.Contains(out.Body.String(), "ignored") {
		t.Fatal("paused replacement callback", out.Code, out.Body.String())
	}
	if h := readHealth(); h.LastVerifiedAt == nil || h.InvalidSignature != 0 || h.PendingHints != 0 || provider.writes != 0 {
		t.Fatal("untracked callback did not verify without writes", h)
	}
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", base+"/issue-tracker-linear-connection", api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	checkHealth(0, 0)
	if h := readHealth(); h.Configured || h.LastVerifiedAt != nil || h.InvalidSignature != 0 {
		t.Fatal("disconnect retained setup evidence", h)
	}

	if out := send(uuid.NewString(), payload, true); out.Code != 404 {
		t.Fatal("disconnected callbacks accepted", out.Code)
	}
}

func TestNativeIssueGitHubCallbacksOnlyWakeOwnedActiveReferences(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "github-callbacks")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "GithubCallback", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	store := apidserver.IssueNativeStore(e)
	acct := apidserver.IssueNativeAccount(e)
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main", AuditGithubLogin: "owner", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: uuid.NewString(), InstallID: 42, RepoFullName: "Owner/Repo", OwnerID: 10, RepoID: 100, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	provider := &nativeStatusFake{status: issuetracker.Status{Provider: "github", URL: link.URL, ID: "42", State: "open"}}
	apidserver.IssueNativeSetClients(e, &nativeGitHubFake{repoID: 100}, provider)
	path := issuePath + "/tracker-links/" + link.ID
	snapshot := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", path+"/native-status", nil, nil), 200)
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", path+"/auto-sync", api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedStatus: &snapshot, ExpectedIssueState: "open"}, nil), 200)
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	callbacks := store.(state.IssueTrackerCallbackStore)
	body := []byte(`{"action":"closed","installation":{"id":42},"repository":{"id":100,"full_name":"Owner/Repo"},"issue":{"number":42,"html_url":"https://github.com/owner/repo/issues/42"}}`)
	event, err := issuetracker.DecodeGitHubIssueCallback(body, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	foreign := event
	foreign.InstallationID = 43
	foreign.ScopeKey = "github/43"
	if status, err := callbacks.EnqueueIssueTrackerCallback(t.Context(), foreign); err != nil || status != "ignored" {
		t.Fatal("foreign install woke policy", status, err)
	}
	if status, err := callbacks.EnqueueIssueTrackerCallback(t.Context(), event); err != nil || status != "accepted" {
		t.Fatal(status, err)
	}
	if status, err := callbacks.EnqueueIssueTrackerCallback(t.Context(), event); err != nil || status != "duplicate" {
		t.Fatal(status, err)
	}
	h, err := store.(state.IssueTrackerCallbackHealthStore).GetIssueTrackerCallbackHealth(t.Context(), app.ID, acct.ID, accepted.IssueID, link.ID)
	if err != nil || !h.Configured || h.LastVerifiedAt == nil || h.Setup != "Verified delivery received within 24 hours" || h.RetainedDeliveries != 1 || h.InvalidSignature != 0 {
		t.Fatal("GitHub setup evidence", h, err)
	}
	provider.status.State = "resolved"
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	policy := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", path+"/auto-sync", nil, nil), 200)
	if policy.Outcome != "conflict" || provider.writes != 0 {
		t.Fatal("manual GitHub edit overwritten", policy)
	}
	// Historical bindings may lack the owner field; health must fail closed
	// without a NULL-to-bool scan failure or old receipt evidence.
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE apps SET github_install_account_id=NULL WHERE id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	h, err = store.(state.IssueTrackerCallbackHealthStore).GetIssueTrackerCallbackHealth(t.Context(), app.ID, acct.ID, accepted.IssueID, link.ID)
	if err != nil || h.Configured || h.LastVerifiedAt != nil || h.Setup != "Not configured" {
		t.Fatal("partial GitHub binding reported configured", h, err)
	}

}

// adr: 1480
func TestNativeIssueInboundStoreIsAtomicAndScopesDeployment(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "inbound-atomic")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "AtomicPull", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	path := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", path+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	store := state.NewPgStore(*e.Pool())
	baseline := api.IssueTrackerProviderStatus{Provider: "github", URL: link.URL, ID: "42", State: "open"}
	r, err := store.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: baseline, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	guard := func() int64 {
		v, err := store.GetIssueTrackerInboundGuard(t.Context(), app.ID, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	observed := baseline
	observed.State = "resolved"
	in := state.IssueTrackerInboundApply{Record: r, GuardRevision: guard(), Observed: observed, FixedDeploymentID: dep.ID, Now: time.Now().UTC()}

	if _, err := store.GetIssueTrackerInboundGuard(t.Context(), app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign owner read intent fence", err)
	}
	// Assignment away and back is local intent even though lifecycle stays open.
	if _, err := store.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeAccountID: app.AccountID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyIssueTrackerInbound(t.Context(), in); err == nil {
		t.Fatal("assignment intent overwritten")
	}
	in.GuardRevision = guard()
	// Source identity, policy and its baseline remain intact after failed actions.
	other := apidserver.SeedPGApp(t, e, "inbound-other")
	otherDep := apidserver.IssueSeedDeployment(t, e, other, 1)
	in.FixedDeploymentID = otherDep.ID
	if _, err := store.ApplyIssueTrackerInbound(t.Context(), in); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign deployment accepted", err)
	}
	in.FixedDeploymentID = dep.ID
	unchanged, err := store.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || unchanged.Policy.Revision != 1 || !unchanged.Policy.Enabled || guard() != in.GuardRevision {
		t.Fatal("failed action changed baseline or intent", unchanged, err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET environment='staging' WHERE id=$1", accepted.IssueID); err != nil {
		t.Fatal(err)
	}
	in.GuardRevision = guard()
	choices, err := store.ListIssueTrackerInboundDeployments(t.Context(), app.ID, app.AccountID, accepted.IssueID)
	if err != nil || len(choices) != 0 {
		t.Fatal("unreleased staging deployment offered", choices, err)
	}
	if _, err := store.ApplyIssueTrackerInbound(t.Context(), in); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-environment resolution accepted", err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET environment='application' WHERE id=$1", accepted.IssueID); err != nil {
		t.Fatal(err)
	}
	in.GuardRevision = guard()
	// A worker owns the policy: even a caller with the latest intent fence cannot
	// apply until its lease has ended and its pending outcome is understood.
	claim := uuid.NewString()
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET claim_id=$2,claim_until=now()+interval '1 minute' WHERE link_id=$1", link.ID, claim); err != nil {
		t.Fatal(err)
	}
	in.GuardRevision = guard()
	if _, err := store.ApplyIssueTrackerInbound(t.Context(), in); err == nil {
		t.Fatal("active worker overwritten")
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET claim_id=NULL,claim_until=NULL,pending_state='resolved',outcome='pending' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	in.GuardRevision = guard()
	if _, err := store.ApplyIssueTrackerInbound(t.Context(), in); err == nil {
		t.Fatal("pending outbound write overwritten")
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET pending_state='',outcome='matched' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	in.GuardRevision = guard()
	// Concurrent duplicate submissions: exactly one action/baseline commit wins.
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := store.ApplyIssueTrackerInbound(context.Background(), in); results <- err }()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("duplicate pull commits", successes)
	}
	detail, err := store.GetIssueDetail(t.Context(), app.ID, accepted.IssueID, time.Now().Add(-time.Hour), time.Now(), state.IssueDetailCursors{})
	if err != nil || detail.Issue.State != "resolved" || detail.Issue.FixedDeploymentID != dep.ID {
		t.Fatal(detail, err)
	}
	count := 0
	for _, a := range detail.Activity {
		if a.Details["tracker_direction"] == "pull" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate/missing atomic audit", count)
	}
	final, err := store.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || final.Policy.Enabled || final.Policy.Revision != 2 || final.Baseline != observed {
		t.Fatal(final, err)
	}
}

// adr: 1481
func TestNativeIssueDirectionalWorkerReopensAndHoldsResolutions(t *testing.T) {
	for _, providerName := range []string{"github", "linear"} {
		t.Run(providerName, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			app := apidserver.SeedPGApp(t, e, "directional-"+providerName)
			dep := apidserver.IssueSeedDeployment(t, e, app, 1)
			fixed := apidserver.IssueSeedDeployment(t, e, app, 2)
			token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
			base := "/v1/apps/" + app.Slug
			send := func(exception string) api.IssueEventResponse {
				return apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: exception, Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
			}
			accepted := send("DirectionalWorker")
			issuePath := base + "/issues/" + accepted.IssueID
			ticketURL := "https://github.com/owner/repo/issues/42"
			if providerName == "linear" {
				ticketURL = "https://linear.app/acme/issue/PAY-42"
			}
			link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: ticketURL}, nil), 200)
			trackerPath := issuePath + "/tracker-links/" + link.ID
			store := state.NewPgStore(*e.Pool())
			ghProvider := &nativeStatusFake{status: issuetracker.Status{Provider: "github", URL: ticketURL, ID: "42", State: "resolved"}}
			linearProvider := &nativeLinearLifecycleFake{}
			openID, resolvedID := uuid.NewString(), uuid.NewString()
			if providerName == "github" {
				st := apidserver.IssueNativeStore(e)
				acct := apidserver.IssueNativeAccount(e)
				if err := st.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main", AuditGithubLogin: "owner", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				if err := st.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{AppID: app.ID, AccountID: acct.ID, BindingID: uuid.NewString(), InstallID: 42, RepoFullName: "Owner/Repo", OwnerID: 10, RepoID: 100, ProductionBranch: "main"}); err != nil {
					t.Fatal(err)
				}
				apidserver.IssueNativeSetClients(e, &nativeGitHubFake{repoID: 100}, ghProvider)
			} else {
				identity, _ := age.GenerateX25519Identity()
				oldRecipient, oldIDs := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
				*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
				*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
				t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIDs })
				apidserver.IssueNativeSetLinearVerifier(e, &nativeLinearFake{})
				conn := api.UpdateIssueTrackerLinearConnectionRequest{Connected: true, TeamID: uuid.NewString(), APIKey: "lin_api_secret"}
				apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", base+"/issue-tracker-linear-connection", conn, nil), 200)
				linearProvider.status = issuetracker.Status{Provider: "linear", URL: ticketURL, ID: uuid.NewString(), TeamID: conn.TeamID, StateID: resolvedID, State: "resolved"}
				apidserver.IssueNativeSetClients(e, nil, linearProvider)
			}
			setRemote := func(life string) {
				if providerName == "github" {
					ghProvider.status.State = life
				} else {
					linearProvider.status.State = life
					linearProvider.status.StateID = openID
					if life == "resolved" {
						linearProvider.status.StateID = resolvedID
					}
				}
			}
			writes := func() int {
				if providerName == "github" {
					return ghProvider.writes
				}
				return linearProvider.writes
			}
			configure := func(direction string, rev int64) {
				detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
				in := api.UpdateIssueTrackerAutoSyncRequest{Direction: direction, Enabled: true, ExpectedRevision: rev, ExpectedIssueState: detail.Issue.State}
				if providerName == "github" {
					snap := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", trackerPath+"/native-status", nil, nil), 200)
					in.ExpectedStatus = &snap
				} else {
					snap := apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "GET", trackerPath+"/native-linear-status", nil, nil), 200)
					in.ExpectedLinearStatus = &snap
					in.LinearOpenStateID = openID
					in.LinearResolvedStateID = resolvedID
				}
				out := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", trackerPath+"/auto-sync", in, nil), 200)
				if out.Direction != direction || out.Revision != rev+1 {
					t.Fatal(out)
				}
			}
			act := func(in api.IssueActionRequest) {
				apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", issuePath+"/actions", in, nil), 200)
			}
			policy := func() api.IssueTrackerAutoSync {
				return apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "GET", trackerPath+"/auto-sync", nil, nil), 200)
			}
			run := func() {
				if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
					t.Fatal(err)
				}
				apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
			}
			act(api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
			if out := e.Do(t, "PUT", trackerPath+"/auto-sync", api.UpdateIssueTrackerAutoSyncRequest{Direction: "sideways"}, nil); out.Code != 400 {
				t.Fatal("invalid direction accepted", out.Code)
			}
			configure("inbound", 0)
			if _, err := store.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 2}); err != nil {
				t.Fatal(err)
			}
			hook, err := apidserver.IssueNativeStore(e).CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://example.com/tracker-sync", Enabled: true, EventFilter: []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}, SecretSealed: []byte("test-sealed")})
			if err != nil {
				t.Fatal(err)
			}
			notices := func(want int, kind string) {
				page, err := store.ListIssueTrackerSyncNotifications(t.Context(), app.ID, app.AccountID, app.AccountID, "", state.IssueCursor{})
				if err != nil || len(page.Items) != want || want > 0 && page.Items[0].Kind != kind {
					t.Fatal("notification transition", page, err)
				}
			}
			// Both providers use the real worker: consecutive read failures
			// escalate once, and only a confirmed healthy check sends recovery.
			ghProvider.readErr = errors.New("private-token-provider-diagnostic")
			linearProvider.readErr = errors.New("private-token-provider-diagnostic")
			run()
			notices(0, "")
			ghProvider.readErr = context.DeadlineExceeded
			linearProvider.readErr = context.DeadlineExceeded
			run()
			notices(1, "failure")
			run()
			notices(1, "failure")
			ghProvider.readErr = nil
			linearProvider.readErr = nil
			run()
			notices(2, "recovered")
			before, err := store.GetIssueTrackerSourceRevision(t.Context(), app.ID, app.AccountID, accepted.IssueID)
			if err != nil {
				t.Fatal(err)
			}
			// An unrelated group and its assignment do not invalidate this baseline.
			other := send("UnrelatedWorkerIssue")
			apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", base+"/issues/"+other.IssueID+"/actions", api.IssueActionRequest{Action: "assign", AssigneeAccountID: app.AccountID}, nil), 200)
			after, err := store.GetIssueTrackerSourceRevision(t.Context(), app.ID, app.AccountID, accepted.IssueID)
			if err != nil || before != after {
				t.Fatal("unrelated issue invalidated source", before, after, err)
			}
			run()
			if out := policy(); out.Outcome != "matched" {
				t.Fatal("worker bookkeeping changed source intent", out)
			}
			setRemote("open")
			run()
			out := policy()
			if out.Outcome != "applied" || out.SourceState != "open" || out.ProviderState != "open" || out.ObservedState != "open" || !out.Enabled || writes() != 0 {
				t.Fatal("automatic reopening failed", out)
			}
			detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
			audits := 0
			for _, a := range detail.Activity {
				if a.Action == "reopened" && a.Details["tracker_mode"] == "automatic" {
					audits++
				}
			}
			if detail.Issue.State != "open" || detail.Issue.FixedDeploymentID != "" || audits != 1 {
				t.Fatal("automatic audit/state missing", detail)
			}
			run()
			if out := policy(); out.Outcome != "matched" || writes() != 0 {
				t.Fatal("reopening loop", out)
			}
			// A local intent change away and back pauses inbound even if state matches.
			act(api.IssueActionRequest{Action: "assign", AssigneeAccountID: app.AccountID})
			act(api.IssueActionRequest{Action: "assign"})
			run()
			if out := policy(); out.Outcome != "conflict" || out.Reason != "source_intent_changed" {
				t.Fatal("manual edit overwritten", out)
			}
			notices(3, "paused")
			run()
			notices(3, "paused")
			configure("both", 1)
			notices(3, "paused") // Enabling alone is not recovery.
			act(api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
			run()
			if out := policy(); out.Outcome != "applied" || out.ProviderState != "resolved" || writes() != 1 {
				t.Fatal("both direction local push failed", out, writes())
			}
			notices(4, "recovered")
			setRemote("open")
			run()
			if out := policy(); out.Outcome != "applied" || out.SourceState != "open" || writes() != 1 {
				t.Fatal("both direction pull failed", out)
			}
			setRemote("resolved")
			run()
			held := policy()
			if held.Outcome != "blocked" || held.Reason != "fixing_deployment_required" || held.ObservedState != "resolved" || held.ProviderState != "open" || held.SourceState != "open" {
				t.Fatal("resolution was not held", held)
			}
			notices(5, "closure")
			run()
			notices(5, "closure")
			detail = apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
			if detail.Issue.State != "open" {
				t.Fatal("provider closure resolved without deployment")
			}
			paused := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", trackerPath+"/auto-sync", api.UpdateIssueTrackerAutoSyncRequest{Enabled: false, ExpectedRevision: 2}, nil), 200)
			if paused.Enabled || paused.Direction != "both" || writes() != 1 {
				t.Fatal("pause failed", paused)
			}
			notices(5, "closure")
			relay := apidserver.IssueNativeStore(e).(state.AppWebhookEventOutboxStore)
			if _, err := relay.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			deliveries, _, err := apidserver.IssueNativeStore(e).ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 20, "")
			if err != nil || len(deliveries) != 5 {
				t.Fatal("webhook fanout", len(deliveries), err)
			}
			for _, delivery := range deliveries {
				payload := string(delivery.Payload)
				if strings.Contains(payload, "private-token") || !strings.Contains(payload, "health_path") {
					t.Fatal("unsafe/incomplete notification", delivery)
				}
			}
			if _, err := relay.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
				t.Fatal(err)
			}
			again, _, err := apidserver.IssueNativeStore(e).ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 20, "")
			if err != nil || len(again) != 5 {
				t.Fatal("notification replay duplicated fanout", len(again), err)
			}
		})
	}
}

// adr: 1481
func TestNativeIssueAutomaticReopenConsumesClaimAtomically(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "reopen-claims")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "ClaimReopen", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	path := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", path+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	st := state.NewPgStore(*e.Pool())
	baseline := api.IssueTrackerProviderStatus{Provider: "github", URL: link.URL, ID: "42", State: "resolved"}
	if _, err := st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	configure := func(rev int64) {
		source, err := st.GetIssueTrackerSourceRevision(t.Context(), app.ID, app.AccountID, accepted.IssueID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{SourceRevision: source, BindingFingerprint: strings.Repeat("a", 64), Baseline: baseline, Policy: api.IssueTrackerAutoSync{Direction: "inbound", LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "resolved"}}, rev)
		if err != nil {
			t.Fatal(err)
		}
	}
	configure(0)
	claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	observed := baseline
	observed.State = "open"
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET claim_until=now()-interval '1 second' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyIssueTrackerAutoReopen(t.Context(), claim, observed); err == nil {
		t.Fatal("expired claim applied")
	}
	configure(1)
	claim, err = st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyIssueTrackerAutoReopen(t.Context(), claim, baseline); err == nil {
		t.Fatal("automatic resolution/state no-op accepted")
	}
	// Duplicate workers with the same proof and claim can commit once only.
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			out, err := st.ApplyIssueTrackerAutoReopen(context.Background(), claim, observed)
			if err == nil && !out.Completed {
				err = errors.New("atomic receipt not marked completed")
			}
			results <- err
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		if err := <-results; err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("duplicate atomic reopen", winners)
	}
	if _, err := st.FinishIssueTrackerAutoSync(t.Context(), claim); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		t.Fatal("stale finish replaced committed baseline", err)
	}
	current, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || current.Baseline.State != "open" || current.ClaimID != "" || current.SourceRevision <= claim.SourceRevision {
		t.Fatal(current, err)
	}
	detail, err := st.GetIssueDetail(t.Context(), app.ID, accepted.IssueID, time.Now().Add(-time.Hour), time.Now(), state.IssueDetailCursors{})
	count := 0
	for _, a := range detail.Activity {
		if a.Details["tracker_mode"] == "automatic" {
			count++
		}
	}
	if err != nil || count != 1 || detail.Issue.State != "open" {
		t.Fatal(detail, err)
	}

	// A non-API database writer (for example a foreign-key assignment cleanup)
	// can hold the source fence without taking the app lock. The atomic reader
	// must see its committed intent after waiting, not an older subquery snapshot.
	if _, err := st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	configure(2)
	racingClaim, err := st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changedTx, err := (*e.Pool()).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = changedTx.Rollback(context.Background()) }()
	if _, err := changedTx.Exec(t.Context(), "UPDATE app_issues SET assignee_account_id=$2 WHERE id=$1", accepted.IssueID, app.AccountID); err != nil {
		t.Fatal(err)
	}
	raceCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	raceResult := make(chan error, 1)
	go func() { _, err := st.ApplyIssueTrackerAutoReopen(raceCtx, racingClaim, observed); raceResult <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := (*e.Pool()).QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%IssueTrackerSourceRevisionLock%')").Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic source check never waited for its fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := changedTx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-raceResult; err == nil {
		t.Fatal("committed edit was hidden by the pre-lock snapshot")
	}
	afterRace, err := st.GetIssueDetail(t.Context(), app.ID, accepted.IssueID, time.Now().Add(-time.Hour), time.Now(), state.IssueDetailCursors{})
	if err != nil || afterRace.Issue.State != "resolved" || afterRace.Issue.AssigneeAccountID != app.AccountID {
		t.Fatal("waited edit overwritten", afterRace, err)
	}
}

func TestNativeIssueTrackerReviewQueueScopesFiltersAndPages(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "tracker-review")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "ReviewQueue", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	st := state.NewPgStore(*e.Pool())
	// Seed one policy per original issue to exercise app-wide paging without
	// exceeding the per-issue tracker-link quota.
	for n := 0; n < api.IssuePageSize+1; n++ {
		id := uuid.NewString()
		if n == 0 {
			id = accepted.IssueID
		} else {
			_, err := (*e.Pool()).Exec(t.Context(), `INSERT INTO app_issues(id,account_id,app_id,environment,fingerprint,grouping_version,title,first_seen_at,last_seen_at)
    SELECT $2,account_id,app_id,environment,$3,grouping_version,'Queue sibling',first_seen_at,last_seen_at FROM app_issues WHERE id=$1`, accepted.IssueID, id, fmt.Sprintf("%064x", n))
			if err != nil {
				t.Fatal(err)
			}
		}
		link, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, id, app.AccountID, api.PutIssueTrackerLinkRequest{URL: fmt.Sprintf("https://github.com/owner/repo/issues/%d", n+100)}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, id, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: fmt.Sprint(n + 100), URL: link.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceState: "open", SourceIssueID: id}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		outcome, reason, pending := "blocked", "connection_required", ""
		if n == 0 {
			reason = "fixing_deployment_required"
		}
		if n == 1 {
			outcome, reason = "conflict", "source_intent_changed"
		}
		if n == 2 {
			outcome, reason, pending = "pending", "", "resolved"
		}
		if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET outcome=$2,reason=$3,pending_state=$4,observed_state='resolved' WHERE link_id=$1", link.ID, outcome, reason, pending); err != nil {
			t.Fatal(err)
		}
	}
	first, err := st.ListIssueTrackerReviews(t.Context(), app.ID, app.AccountID, "all", state.IssueCursor{})
	if err != nil || len(first.Items) != api.IssuePageSize || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	cursor, err := state.DecodeIssueCursor(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.ListIssueTrackerReviews(t.Context(), app.ID, app.AccountID, "all", cursor)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, row := range first.Items {
		seen[row.LinkID] = true
	}
	if seen[second.Items[0].LinkID] {
		t.Fatal("duplicate page boundary")
	}
	for category, want := range map[string]int{"closures": 1, "conflicts": 1, "pending": 1, "paused": api.IssuePageSize - 2} {
		page, err := st.ListIssueTrackerReviews(t.Context(), app.ID, app.AccountID, category, state.IssueCursor{})
		if err != nil || len(page.Items) != want {
			t.Fatal(category, page, err)
		}
		if _, err = st.ListIssueTrackerReviews(t.Context(), app.ID, app.AccountID, category, cursor); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("cursor crossed filter", category, err)
		}
	}
	foreign, err := st.ListIssueTrackerReviews(t.Context(), app.ID, uuid.NewString(), "all", state.IssueCursor{})
	if err != nil || len(foreign.Items) != 0 {
		t.Fatal("foreign owner read queue", foreign, err)
	}
	if _, err = st.ListIssueTrackerReviews(t.Context(), app.ID, app.AccountID, "unknown", state.IssueCursor{}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
	// Queue requests only read saved observations, even without provider clients.
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-review"
	get := func(path string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if auth {
			e.AddAdminSession(t, req)
		}
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	response := get(pageURL+"?category=closures", true)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "review fixing deployment") {
		t.Fatal(response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) == 0 {
		t.Fatal("missing CSRF cookie")
	}
	if r := get(pageURL+"?category=unknown", true); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := get(pageURL+"?category=closures&cursor="+url.QueryEscape(first.NextCursor), true); r.Code != 400 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := get(pageURL, false); r.Code == 200 {
		t.Fatal("anonymous queue access")
	}
	// Evidence includes occurrences on the reporting deployment, not just traffic
	// from unrelated groups; named environments still require release membership.
	choices, err := st.ListIssueTrackerInboundDeployments(t.Context(), app.ID, app.AccountID, accepted.IssueID)
	if err != nil || len(choices) != 1 || choices[0].ID != dep.ID || choices[0].ObservedEvents != 1 || choices[0].LastSeenAt.IsZero() {
		t.Fatal(choices, err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET environment='staging' WHERE id=$1", accepted.IssueID); err != nil {
		t.Fatal(err)
	}
	choices, err = st.ListIssueTrackerInboundDeployments(t.Context(), app.ID, app.AccountID, accepted.IssueID)
	if err != nil || len(choices) != 0 {
		t.Fatal("off-environment suggestion", choices, err)
	}
}

// adr: 1483
func TestNativeIssueSyncHealthReceiptsAndReadOnlyRecovery(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "sync-health")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	ingest := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "SyncHealth", Message: "failed"}, map[string]string{"Authorization": "Bearer " + ingest.Token}), 202)
	issuePath := base + "/issues/" + accepted.IssueID
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", issuePath+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, nil), 200)
	native := issuePath + "/tracker-links/" + link.ID + "/native-status"
	policyPath := issuePath + "/tracker-links/" + link.ID + "/auto-sync"
	store := apidserver.IssueNativeStore(e)
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{AccountID: app.AccountID, InstallationID: 42, DefaultBranch: "main", AuditGithubLogin: "owner", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGithubInstallBinding(t.Context(), state.GitHubBinding{AppID: app.ID, AccountID: app.AccountID, BindingID: uuid.NewString(), InstallID: 42, RepoFullName: "Owner/Repo", OwnerID: 10, RepoID: 100, ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	provider := &nativeStatusFake{status: issuetracker.Status{Provider: "github", URL: link.URL, ID: "42", State: "open"}}
	apidserver.IssueNativeSetClients(e, &nativeGitHubFake{repoID: 100}, provider)
	snapshot := apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	enable := api.UpdateIssueTrackerAutoSyncRequest{Enabled: true, ExpectedStatus: &snapshot, ExpectedIssueState: "open"}
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", policyPath, enable, nil), 200)
	st := state.NewPgStore(*e.Pool())
	health := func() state.IssueTrackerSyncHealth {
		v, err := st.GetIssueTrackerSyncHealth(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	h := health()
	if h.LastSuccessfulAt != nil || len(h.History) != 1 || h.History[0].EventType != "enabled" {
		t.Fatal(h)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	h = health()
	if h.LastSuccessfulAt == nil || h.History[0].EventType != "checked" || h.History[0].Outcome != "matched" {
		t.Fatal(h)
	}
	successful := *h.LastSuccessfulAt
	provider.readErr = errors.New("token=secret-provider-body")
	if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	apidserver.IssueNativeRunAutoSyncBatch(e, t.Context())
	h = health()
	if !h.LastSuccessfulAt.Equal(successful) || h.History[0].Outcome != "error" || h.History[0].Reason != "provider_unavailable" {
		t.Fatal(h)
	}
	healthPath := "/dashboard/apps/" + app.Slug + "/issues/" + accepted.IssueID + "/tracker-sync/" + link.ID
	get := func(auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", healthPath, nil)
		if auth {
			e.AddAdminSession(t, req)
		}
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	reads := provider.reads
	response := get(true)
	if response.Code != 200 || provider.reads != reads || !strings.Contains(response.Body.String(), "Retryable check failure") || strings.Contains(response.Body.String(), "secret-provider-body") {
		t.Fatal(response.Code, response.Body.String(), provider.reads)
	}
	var csrf *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF")
	}
	preview := func(valid bool) *httptest.ResponseRecorder {
		form := url.Values{"action": {"preview"}, "provider": {"github"}, "link_id": {link.ID}}
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		req := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/"+accepted.IssueID+"/tracker-sync", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			req.AddCookie(csrf)
		}
		e.AddAdminSession(t, req)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	count := len(health().History)
	if out := preview(false); out.Code != 400 || len(health().History) != count {
		t.Fatal("unauthenticated inspection", out.Code)
	}
	if out := preview(true); out.Code != 502 {
		t.Fatal("provider error not returned", out.Code, out.Body.String())
	}
	h = health()
	if h.History[0].EventType != "inspected" || h.History[0].Reason != "provider_unavailable" || h.History[0].ObservedState != "" || !h.LastSuccessfulAt.Equal(successful) {
		t.Fatal(h)
	}
	if get(false).Code == 200 {
		t.Fatal("anonymous health access")
	}
	if _, err := st.GetIssueTrackerSyncHealth(t.Context(), app.ID, uuid.NewString(), accepted.IssueID, link.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign owner saw history", err)
	}
	provider.readErr = nil
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET outcome='conflict',reason='provider_changed' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out := preview(true); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	after, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	h = health()
	if err != nil || before.Policy.Revision != after.Policy.Revision || after.Policy.Outcome != "conflict" || before.Baseline != after.Baseline || provider.writes != 0 || h.History[0].EventType != "inspected" || h.History[0].ObservedState != "open" || !h.LastSuccessfulAt.Equal(successful) {
		t.Fatal("inspection resumed or rebased policy", after, h, err)
	}
	snapshot = apidserver.IssueDecode[api.NativeIssueTrackerStatus](t, e.Do(t, "GET", native, nil, nil), 200)
	enable.ExpectedStatus = &snapshot
	enable.ExpectedRevision = 1
	apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", policyPath, enable, nil), 200)
	claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, claim, 2); err == nil {
		t.Fatal("active lease was reset by enable")
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET claim_until=now()-interval '1 second' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		t.Fatal("expired completion accepted", err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_auto_sync SET claim_until=now()+interval '1 minute' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	// Force the history insert to fail. The successful policy completion and its
	// last-success timestamp must roll back, retaining the live claim for retry.
	if _, err = (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION reject_sync_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'receipt unavailable'; END $$; CREATE TRIGGER reject_sync_receipt BEFORE INSERT ON issue_tracker_sync_history FOR EACH ROW EXECUTE FUNCTION reject_sync_receipt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err == nil {
		t.Fatal("receipt failure committed completion")
	}
	after, err = st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || after.ClaimID != claim.ClaimID || !health().LastSuccessfulAt.Equal(successful) {
		t.Fatal("partial completion", after, err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "DROP TRIGGER reject_sync_receipt ON issue_tracker_sync_history; DROP FUNCTION reject_sync_receipt()"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	count = len(health().History)
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) || len(health().History) != count {
		t.Fatal("duplicate completion receipt", err)
	}
	if err = apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	claim, err = st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := st.MarkIssueTrackerAutoSyncPending(t.Context(), claim, "resolved")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, pending, 2); err == nil {
		t.Fatal("pending intent replaced by enable")
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{Policy: api.IssueTrackerAutoSync{LinkID: link.ID}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	h = health()
	if h.History[0].EventType != "disabled" || h.History[0].PendingState != "resolved" {
		t.Fatal("unknown dispatch evidence lost on disable", h)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), pending); !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
		t.Fatal(err)
	}
	current, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := st.GetIssueTrackerInboundGuard(t.Context(), app.ID, app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	inspection := state.IssueTrackerSyncInspection{Record: current, GuardRevision: guard, Observed: current.Baseline, Reason: "policy_disabled"}
	lastSuccess := *h.LastSuccessfulAt
	for range api.IssueTrackerSyncHistoryMax + 2 {
		if err = st.RecordIssueTrackerSyncInspection(t.Context(), inspection); err != nil {
			t.Fatal(err)
		}
	}
	h = health()
	if len(h.History) != api.IssueTrackerSyncHistoryMax || h.History[0].EventType != "inspected" || !h.LastSuccessfulAt.Equal(lastSuccess) {
		t.Fatal("unbounded or misleading history", h)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE app_issues SET assignee_account_id=$2 WHERE id=$1", accepted.IssueID, app.AccountID); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordIssueTrackerSyncInspection(t.Context(), inspection); err == nil {
		t.Fatal("stale inspection accepted")
	}
}

// adr: 1484
func TestNativeIssueSyncNotificationOwnershipSettingsAndAtomicOutbox(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "sync-notifications")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "SyncNotification", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	full := apidserver.IssueNativeStore(e)
	st := state.NewPgStore(*e.Pool())
	org, err := full.CreateOrg(t.Context(), state.Org{Slug: "tracker-notify-" + uuid.NewString()[:8], Name: "Notify", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := full.CreateAccount(t.Context(), "notify-member-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := full.CreateAccount(t.Context(), "notify-outsider-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{app.AccountID, member.ID, outsider.ID} {
		role := state.OrgRoleDeveloper
		if id == app.AccountID {
			role = state.OrgRoleOwner
		}
		if err = full.AddOrgMember(t.Context(), org.ID, id, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "payments", api.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{app.AccountID, member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	link, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, accepted.IssueID, app.AccountID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: "42", URL: link.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AppID: app.ID, AccountID: app.AccountID, TargetURL: "https://example.com/sync", Enabled: true, EventFilter: []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	_ = hook
	complete := func(outcome, reason string) {
		if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
			t.Fatal(err)
		}
		claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		claim.Policy.Outcome = outcome
		claim.Policy.Reason = reason
		if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(actor, filter string) state.IssueTrackerNotificationPage {
		p, err := st.ListIssueTrackerSyncNotifications(t.Context(), app.ID, app.AccountID, actor, filter, state.IssueCursor{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	initial, err := st.GetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || initial.Enabled || initial.Revision != 0 {
		t.Fatal(initial, err)
	}
	complete("error", "provider_unavailable")
	complete("error", "provider_unavailable")
	if len(inbox(app.AccountID, "").Items) != 0 {
		t.Fatal("notifications enabled by default")
	}
	policy, err := st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, outsider.ID, policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member changed notification policy", err)
	}
	if _, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 2}); err == nil {
		t.Fatal("stale settings accepted")
	}
	if _, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Revision: 1, FailureThreshold: api.IssueTrackerSyncNotificationFailuresMax + 1}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal(err)
	}
	complete("error", "provider_unavailable")
	if len(inbox(app.AccountID, "").Items) != 0 {
		t.Fatal("historical failures replayed after opt-in")
	}
	complete("error", "provider_unavailable")
	complete("error", "provider_unavailable")
	if p := inbox(member.ID, team.ID); len(p.Items) != 1 || p.Items[0].Kind != "failure" || p.Items[0].TeamID != team.ID {
		t.Fatal("team routing", p)
	}
	if p := inbox(outsider.ID, ""); len(p.Items) != 0 {
		t.Fatal("unrelated org member read notifications", p)
	}
	// App members can read local health, while only the owner gets inspection controls.
	getAs := func(account state.Account, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		saved := *e.Acct()
		*e.Acct() = account
		e.AddAdminSession(t, req)
		*e.Acct() = saved
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	healthPath := "/dashboard/apps/" + app.Slug + "/issues/" + accepted.IssueID + "/tracker-sync/" + link.ID
	if out := getAs(member, healthPath); out.Code != 200 || strings.Contains(out.Body.String(), `value="preview"`) {
		t.Fatal("member health inspection authority", out.Code, out.Body.String())
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	if out := getAs(member, pageURL); out.Code != 200 || strings.Contains(out.Body.String(), `name="failure_threshold"`) || !strings.Contains(out.Body.String(), "Payments") {
		t.Fatal("member inbox", out.Code, out.Body.String())
	}
	if out := getAs(outsider, healthPath); out.Code != 404 {
		t.Fatal("unrelated member read link health", out.Code, out.Body.String())
	}
	if out := getAs(outsider, pageURL); out.Code != 404 {
		t.Fatal("unrelated member opened inbox", out.Code)
	}
	// Triage is shared by incident, with revision checks and no lifecycle effects.
	notice := inbox(member.ID, team.ID).Items[0].ID
	triageGet := func(actor string) state.IssueTrackerTriage {
		t.Helper()
		v, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, actor, notice)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	triageAct := func(actor string, revision int64, action string) error {
		return st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, actor, notice, revision, action)
	}
	beforeTriage, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v := triageGet(member.ID); !v.Active || v.Revision != 0 || len(v.History) != 0 {
		t.Fatal(v)
	}
	if _, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, outsider.ID, notice); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unrelated member read triage", err)
	}
	if err := triageAct(outsider.ID, 0, "claim"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unrelated member claimed", err)
	}
	if err := triageAct(member.ID, 0, "acknowledge"); err != nil {
		t.Fatal(err)
	}
	if err := triageAct(member.ID, 0, "claim"); err == nil {
		t.Fatal("stale triage revision accepted")
	}
	claims := make(chan error, 2)
	for _, actor := range []string{member.ID, app.AccountID} {
		go func(actor string) { claims <- triageAct(actor, 1, "claim") }(actor)
	}
	wins := 0
	for range 2 {
		if <-claims == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("concurrent claims", wins)
	}
	v := triageGet(member.ID)
	if v.Revision != 2 || v.AcknowledgedBy != member.ID || v.ClaimedBy == "" || len(v.History) != 2 {
		t.Fatal(v)
	}

	otherClaimant := app.AccountID
	if v.ClaimedBy == app.AccountID {
		otherClaimant = member.ID
	}
	if triageAct(otherClaimant, 2, "claim") == nil {
		t.Fatal("existing claim was overwritten")
	}
	if v.ClaimedBy == app.AccountID && triageAct(member.ID, 2, "release") == nil {
		t.Fatal("member released another account's claim")
	}
	if err := triageAct(app.AccountID, 2, "release"); err != nil {
		t.Fatal(err)
	}
	if err := triageAct(member.ID, 3, "claim"); err != nil {
		t.Fatal(err)
	}
	// A history write failure must not leave a partially changed claim.
	if _, err := (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION reject_tracker_triage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'history unavailable'; END $$; CREATE TRIGGER reject_tracker_triage BEFORE INSERT ON issue_tracker_sync_triage_activity FOR EACH ROW EXECUTE FUNCTION reject_tracker_triage()`); err != nil {
		t.Fatal(err)
	}
	if triageAct(app.AccountID, 4, "release") == nil {
		t.Fatal("triage committed without history")
	}
	if v := triageGet(member.ID); v.Revision != 4 || v.ClaimedBy != member.ID || len(v.History) != 4 {
		t.Fatal("partial triage", v)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "DROP TRIGGER reject_tracker_triage ON issue_tracker_sync_triage_activity; DROP FUNCTION reject_tracker_triage()"); err != nil {
		t.Fatal(err)
	}
	if err := triageAct(app.AccountID, 4, "release"); err != nil {
		t.Fatal(err)
	}
	// Acknowledgment is idempotent and records the first actor.
	if err := triageAct(app.AccountID, 5, "acknowledge"); err != nil {
		t.Fatal(err)
	}
	if v := triageGet(app.AccountID); v.Revision != 5 || v.AcknowledgedBy != member.ID {
		t.Fatal(v)
	}
	triageURL := pageURL + "/triage/" + notice
	triagePage := getAs(member, triageURL)
	if triagePage.Code != 200 || !strings.Contains(triagePage.Body.String(), "Claim for me") || !strings.Contains(triagePage.Body.String(), member.Email) {
		t.Fatal("member triage page", triagePage.Code, triagePage.Body.String())
	}
	if out := getAs(outsider, triageURL); out.Code != 404 {
		t.Fatal("unrelated triage read", out.Code)
	}
	var triageCSRF *http.Cookie
	for _, c := range triagePage.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			triageCSRF = c
		}
	}
	if triageCSRF == nil {
		t.Fatal("missing triage CSRF")
	}
	triagePost := func(valid bool, revision int64) *httptest.ResponseRecorder {
		form := url.Values{"action": {"claim"}, "expected_revision": {fmt.Sprint(revision)}}
		if valid {
			form.Set("csrf_token", triageCSRF.Value)
		}
		req := httptest.NewRequest("POST", triageURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			req.AddCookie(triageCSRF)
		}
		saved := *e.Acct()
		*e.Acct() = member
		e.AddAdminSession(t, req)
		*e.Acct() = saved
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	if out := triagePost(false, 5); out.Code != 400 {
		t.Fatal("triage CSRF bypass", out.Code)
	}
	if out := triagePost(true, 5); out.Code != 303 {
		t.Fatal("member triage write", out.Code, out.Body.String())
	}
	if out := triagePost(true, 5); out.Code != 409 {
		t.Fatal("stale HTTP triage", out.Code)
	}

	// Bound history independently of the number of ownership handoffs.
	finalTriageRevision := triageGet(member.ID).Revision
	for range api.IssueTrackerSyncTriageHistoryMax {
		if err := triageAct(app.AccountID, finalTriageRevision, "release"); err != nil {
			t.Fatal(err)
		}
		finalTriageRevision++
		if err := triageAct(member.ID, finalTriageRevision, "claim"); err != nil {
			t.Fatal(err)
		}
		finalTriageRevision++
	}
	if v := triageGet(member.ID); len(v.History) != api.IssueTrackerSyncTriageHistoryMax || v.History[0].Revision != finalTriageRevision {
		t.Fatal("unbounded triage history", v)
	}
	afterTriage, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || afterTriage.Policy.Revision != beforeTriage.Policy.Revision || afterTriage.Policy.Outcome != beforeTriage.Policy.Outcome || afterTriage.Policy.PendingState != beforeTriage.Policy.PendingState || len(inbox(member.ID, team.ID).Items) != 1 {
		t.Fatal("triage changed sync lifecycle", afterTriage, err)
	}
	if v := inbox(member.ID, team.ID).Items[0]; v.ClaimedBy != member.ID || v.AcknowledgedBy != member.ID {
		t.Fatal("inbox handling state", v)
	}
	// Snoozes share incident state, use database time, and resurface without a worker.
	viewInbox := func(actor, view string) state.IssueTrackerNotificationPage {
		t.Helper()
		p, err := st.ListIssueTrackerSyncNotificationsView(t.Context(), app.ID, app.AccountID, actor, "", view, state.IssueCursor{})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	snoozeUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	snooze := func(actor string, revision int64, until time.Time, reason string) error {
		return st.UpdateIssueTrackerTriage(t.Context(), app.ID, app.AccountID, actor, notice, revision, state.IssueTrackerTriageAction{Action: "snooze", SnoozedUntil: &until, Reason: reason})
	}
	for _, tc := range []struct {
		until  time.Time
		reason string
	}{
		{time.Now().Add(-time.Hour), "past"},
		{time.Now().Add(api.IssueTrackerSyncSnoozeMax + time.Hour), "too long"},
		{snoozeUntil, strings.Repeat("é", api.IssueTrackerSyncSnoozeReasonMaxBytes)},
		{snoozeUntil, "nul\x00reason"},
		{snoozeUntil, string([]byte{0xff})},
	} {
		if err := snooze(member.ID, finalTriageRevision, tc.until, tc.reason); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid snooze accepted", err)
		}
	}
	if err := snooze(outsider.ID, finalTriageRevision, snoozeUntil, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unrelated recipient snoozed incident", err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION reject_tracker_snooze() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'history unavailable'; END $$; CREATE TRIGGER reject_tracker_snooze BEFORE INSERT ON issue_tracker_sync_triage_activity FOR EACH ROW EXECUTE FUNCTION reject_tracker_snooze()`); err != nil {
		t.Fatal(err)
	}
	if snooze(member.ID, finalTriageRevision, snoozeUntil, "rollback") == nil {
		t.Fatal("snooze committed without history")
	}
	if v := triageGet(member.ID); v.Snoozed || v.SnoozedUntil != nil || v.Revision != finalTriageRevision {
		t.Fatal("partial snooze", v)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "DROP TRIGGER reject_tracker_snooze ON issue_tracker_sync_triage_activity; DROP FUNCTION reject_tracker_snooze()"); err != nil {
		t.Fatal(err)
	}
	snoozePost := func(action string, revision int64, until, reason string, valid bool) *httptest.ResponseRecorder {
		form := url.Values{"action": {action}, "expected_revision": {fmt.Sprint(revision)}, "snoozed_until": {until}, "reason": {reason}}
		if valid {
			form.Set("csrf_token", triageCSRF.Value)
		}
		req := httptest.NewRequest("POST", triageURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			req.AddCookie(triageCSRF)
		}
		saved := *e.Acct()
		*e.Acct() = member
		e.AddAdminSession(t, req)
		*e.Acct() = saved
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	if out := snoozePost("snooze", finalTriageRevision, snoozeUntil.Format(time.RFC3339), "", false); out.Code != 400 {
		t.Fatal("snooze CSRF bypass", out.Code)
	}
	if out := snoozePost("snooze", finalTriageRevision, "invalid", "", true); out.Code != 400 {
		t.Fatal("invalid snooze date", out.Code)
	}
	if out := snoozePost("snooze", finalTriageRevision, "", "", true); out.Code != 400 {
		t.Fatal("missing snooze date", out.Code)
	}
	if out := snoozePost("snooze", finalTriageRevision, snoozeUntil.Format(time.RFC3339), "  investigate deployment  ", true); out.Code != 303 {
		t.Fatal("member snooze HTTP", out.Code, out.Body.String())
	}
	finalTriageRevision++
	if v := triageGet(member.ID); !v.Snoozed || v.SnoozedUntil == nil || !v.SnoozedUntil.Equal(snoozeUntil) || v.SnoozedBy != member.ID || v.SnoozeReason != "investigate deployment" || v.History[0].Action != "snooze" || v.History[0].Reason != "investigate deployment" || v.History[0].SnoozedUntil == nil {
		t.Fatal("snooze evidence", v)
	}
	if len(inbox(member.ID, team.ID).Items) != 0 || len(viewInbox(member.ID, "snoozed").Items) != 1 || len(viewInbox(member.ID, "all").Items) != 1 {
		t.Fatal("snooze inbox views")
	}
	if out := getAs(member, pageURL+"?view=snoozed"); out.Code != 200 || !strings.Contains(out.Body.String(), "investigate deployment") {
		t.Fatal("snoozed inbox page", out.Code, out.Body.String())
	}
	if out := getAs(member, triageURL); out.Code != 200 || !strings.Contains(out.Body.String(), `value="unsnooze"`) {
		t.Fatal("snooze controls", out.Code, out.Body.String())
	}
	if out := snoozePost("unsnooze", finalTriageRevision-1, "", "", true); out.Code != 409 {
		t.Fatal("stale unsnooze", out.Code)
	}
	if out := snoozePost("unsnooze", finalTriageRevision, "", "", true); out.Code != 303 {
		t.Fatal("unsnooze HTTP", out.Code, out.Body.String())
	}
	finalTriageRevision++
	if v := triageGet(member.ID); v.Snoozed || v.SnoozedUntil != nil || v.History[0].Action != "unsnooze" || len(inbox(member.ID, team.ID).Items) != 1 {
		t.Fatal("manual unsnooze", v)
	}
	if err := triageAct(app.AccountID, finalTriageRevision, "unsnooze"); err != nil || triageGet(member.ID).Revision != finalTriageRevision {
		t.Fatal("unsnooze not idempotent", err)
	}
	// Concurrent deferrals must not overwrite one another.
	snoozes := make(chan error, 2)
	for _, actor := range []string{member.ID, app.AccountID} {
		go func(actor string) { snoozes <- snooze(actor, finalTriageRevision, snoozeUntil, "") }(actor)
	}
	snoozeWins := 0
	for range 2 {
		if <-snoozes == nil {
			snoozeWins++
		}
	}
	if snoozeWins != 1 {
		t.Fatal("concurrent snoozes", snoozeWins)
	}
	finalTriageRevision++
	if err := triageAct(app.AccountID, finalTriageRevision, "release"); err != nil {
		t.Fatal(err)
	}
	finalTriageRevision++
	if err := triageAct(member.ID, finalTriageRevision, "claim"); err != nil {
		t.Fatal(err)
	}
	finalTriageRevision++
	if !triageGet(member.ID).Snoozed {
		t.Fatal("claim update lost snooze")
	}
	// Advance only the isolated fixture deadline; reads must resurface it without an expiry job.
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_sync_triage SET snoozed_until=clock_timestamp() WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	if v := triageGet(member.ID); v.Snoozed || !v.Resurfaced || v.Revision != finalTriageRevision {
		t.Fatal("snooze did not expire", v)
	}
	if p := inbox(member.ID, team.ID); len(p.Items) != 1 || !p.Items[0].Resurfaced || !p.Items[0].SortAt.After(p.Items[0].CreatedAt) {
		t.Fatal("expired incident did not resurface", p)
	}
	if len(viewInbox(member.ID, "snoozed").Items) != 0 {
		t.Fatal("expired item remained in Snoozed view")
	}
	if out := getAs(member, triageURL); out.Code != 200 || !strings.Contains(out.Body.String(), "Returned from snooze") || strings.Contains(out.Body.String(), `value="unsnooze"`) {
		t.Fatal("expiry display", out.Code, out.Body.String())
	}
	if err := snooze(member.ID, finalTriageRevision, snoozeUntil, "waiting for recovery"); err != nil {
		t.Fatal(err)
	}
	finalTriageRevision++
	if err = full.RemoveOrgMember(t.Context(), org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if len(inbox(member.ID, "").Items) != 0 {
		t.Fatal("removed org member retained inbox access")
	}
	if _, err := st.GetIssueTrackerNotificationHealth(t.Context(), app.ID, app.AccountID, member.ID, accepted.IssueID, link.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed member retained health read", err)
	}

	if _, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed member retained triage", err)
	}
	if err := triageAct(member.ID, finalTriageRevision, "release"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed member wrote triage", err)
	}

	if len(viewInbox(member.ID, "all").Items) != 0 {
		t.Fatal("removed member retained snoozed notices")
	}
	if err := snooze(member.ID, finalTriageRevision, snoozeUntil, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed member updated snooze", err)
	}

	inviteToken := []byte(strings.Repeat("n", 32))
	if _, err = full.CreateOrgInvitation(t.Context(), state.OrgInvitation{OrgID: org.ID, Email: member.Email, Role: state.OrgRoleDeveloper, TokenHash: inviteToken, InvitedByAccountID: &app.AccountID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = full.ConsumeOrgInvitation(t.Context(), inviteToken, member); err != nil {
		t.Fatal(err)
	}
	_, err = st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "payments", api.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox(member.ID, "").Items) != 0 {
		t.Fatal("removed team member retained inbox access")
	}

	if _, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed team member retained triage", err)
	}
	if err := triageAct(member.ID, finalTriageRevision, "release"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("removed team member changed triage", err)
	}

	if len(viewInbox(member.ID, "all").Items) != 0 {
		t.Fatal("removed team member retained snoozed notices")
	}
	complete("matched", "")
	p := inbox(app.AccountID, team.ID)
	if len(p.Items) != 2 || p.Items[0].Kind != "recovered" {
		t.Fatal("recovery routing", p)
	}
	if v := triageGet(app.AccountID); v.Active || v.Snoozed || v.Resurfaced || v.ClaimedBy != member.ID || len(v.History) != api.IssueTrackerSyncTriageHistoryMax {
		t.Fatal("recovery lost triage history", v)
	}
	if triageAct(app.AccountID, finalTriageRevision, "release") == nil {
		t.Fatal("recovered incident accepted action")
	}

	if _, err = st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	complete("error", "provider_unavailable")
	complete("error", "provider_unavailable")
	if p := inbox(app.AccountID, ""); len(p.Items) != 3 || p.Items[0].TeamID != "" {
		t.Fatal("owner fallback", p)
	}

	freshNotice := inbox(app.AccountID, "").Items[0].ID
	if fresh, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, freshNotice); err != nil || !fresh.Active || fresh.Revision != 0 || fresh.ClaimedBy != "" || fresh.Snoozed || fresh.SnoozedUntil != nil {
		t.Fatal("new incident inherited handling", fresh, err)
	}

	if err := st.UpdateIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, freshNotice, 0, state.IssueTrackerTriageAction{Action: "snooze", SnoozedUntil: &snoozeUntil}); err != nil {
		t.Fatal(err)
	}
	policy.Enabled = false
	policy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, policy)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, freshNotice, 0, "claim"); err == nil {
		t.Fatal("disabled notification policy allowed triage")
	}
	if fresh, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, freshNotice); err != nil || fresh.Active || fresh.Snoozed {
		t.Fatal("opt-out retained active triage", fresh, err)
	}
	complete("matched", "")
	if len(inbox(app.AccountID, "").Items) != 3 {
		t.Fatal("opt-out sent recovery")
	}
	policy.Enabled = true
	policy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, policy)
	if err != nil {
		t.Fatal(err)
	}
	complete("matched", "")
	if len(inbox(app.AccountID, "").Items) != 3 {
		t.Fatal("settings change replayed old incident")
	}
	// Bounded per-link retention and app-wide keyset pagination with actor binding.
	for range api.IssueTrackerSyncNotificationsMax/2 + 1 {
		complete("error", "provider_unavailable")
		complete("error", "provider_unavailable")
		complete("matched", "")
	}
	var retained int
	if err = (*e.Pool()).QueryRow(t.Context(), "SELECT count(*) FROM issue_tracker_sync_notifications WHERE link_id=$1", link.ID).Scan(&retained); err != nil || retained != api.IssueTrackerSyncNotificationsMax {
		t.Fatal("unbounded notices", retained, err)
	}

	if _, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, notice); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("pruned notice retained triage access", err)
	}
	var triageRetained int
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT count(*) FROM issue_tracker_sync_triage WHERE link_id=$1", link.ID).Scan(&triageRetained); err != nil || triageRetained != 0 {
		t.Fatal("orphaned triage retention", triageRetained, err)
	}
	other, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, accepted.IssueID, app.AccountID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/43"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: "43", URL: other.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: other.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil || claim.Policy.LinkID != other.ID {
		t.Fatal(claim, err)
	}
	claim.Policy.Outcome = "blocked"
	claim.Policy.Reason = "connection_required"
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	p = inbox(app.AccountID, "")
	if len(p.Items) != api.IssuePageSize || p.NextCursor == "" {
		t.Fatal("missing next page", p)
	}
	cursor, err := state.DecodeIssueCursor(p.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	last, err := st.ListIssueTrackerSyncNotifications(t.Context(), app.ID, app.AccountID, app.AccountID, "", cursor)
	if err != nil || len(last.Items) != 1 || last.NextCursor != "" {
		t.Fatal(last, err)
	}
	if _, err = st.ListIssueTrackerSyncNotifications(t.Context(), app.ID, app.AccountID, member.ID, "", cursor); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cursor crossed actor", err)
	}

	if _, err := st.ListIssueTrackerSyncNotificationsView(t.Context(), app.ID, app.AccountID, app.AccountID, "", "all", cursor); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cursor crossed inbox view", err)
	}
	// Outbox failure must roll back both completion and incident/notification state.
	complete("error", "provider_unavailable")
	if err = apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	claim, err = st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	claim.Policy.Outcome = "error"
	claim.Policy.Reason = "provider_unavailable"
	before, err := st.GetIssueTrackerSyncHealth(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION reject_tracker_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event LIKE 'issue.tracker_sync.%' THEN RAISE EXCEPTION 'outbox unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_tracker_outbox BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_tracker_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err == nil {
		t.Fatal("outbox failure committed completion")
	}
	after, err := st.GetIssueTrackerSyncHealth(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil || after.History[0].ID != before.History[0].ID {
		t.Fatal("partial completion", after, err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "DROP TRIGGER reject_tracker_outbox ON app_webhook_event_outbox; DROP FUNCTION reject_tracker_outbox()"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
		t.Fatal("completion not retryable after rollback", err)
	}
	// Owner settings forms preserve CSRF and revision checks without touching sync.
	response := getAs(*e.Acct(), pageURL)
	var csrf *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == apidserver.DashboardIssueCookie {
			csrf = cookie
		}
	}
	if response.Code != 200 || csrf == nil {
		t.Fatal(response.Code, response.Body.String())
	}
	post := func(valid bool) *httptest.ResponseRecorder {
		form := url.Values{"enabled": {"1"}, "expected_revision": {fmt.Sprint(policy.Revision)}, "failure_threshold": {"2"}}
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		req := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			req.AddCookie(csrf)
		}
		e.AddAdminSession(t, req)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, req)
		return out
	}
	if out := post(false); out.Code != 400 {
		t.Fatal("CSRF bypass", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale settings form accepted", out.Code)
	}
}

func TestNativeIssueTrackerRemindersAreScopedBoundedAndAtomic(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "tracker-reminders")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Reminder", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	full := apidserver.IssueNativeStore(e)
	st := state.NewPgStore(*e.Pool())
	org, err := full.CreateOrg(t.Context(), state.Org{Slug: "reminders-" + uuid.NewString()[:8], Name: "Reminders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := full.CreateAccount(t.Context(), "reminder-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, app.AccountID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "reminders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID, member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	link, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, accepted.IssueID, app.AccountID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: "42", URL: link.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	noticePolicy, err := st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AppID: app.ID, AccountID: app.AccountID, TargetURL: "https://example.com/reminders", Enabled: true, EventFilter: []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}, SecretSealed: []byte("fake-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	complete := func(outcome string) {
		t.Helper()
		if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
			t.Fatal(err)
		}
		claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		claim.Policy.Outcome = outcome
		claim.Policy.Reason = ""
		if outcome == "error" {
			claim.Policy.Reason = "provider_unavailable"
		}
		if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(actor string) []state.IssueTrackerSyncNotification {
		t.Helper()
		p, err := st.ListIssueTrackerSyncNotificationsView(t.Context(), app.ID, app.AccountID, actor, "", "all", state.IssueCursor{})
		if err != nil {
			t.Fatal(err)
		}
		return p.Items
	}
	forceDue := func() {
		t.Helper()
		if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_sync_incidents SET opened_at=clock_timestamp()-interval '2 days',last_reminded_at=CASE WHEN reminder_count>0 THEN clock_timestamp()-interval '2 days' ELSE NULL END WHERE link_id=$1 AND incident_id IS NOT NULL", link.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_sync_reminder_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		var n int
		if err := (*e.Pool()).QueryRow(t.Context(), "SELECT reminder_count FROM issue_tracker_sync_incidents WHERE link_id=$1", link.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	enqueue := func(want bool) {
		t.Helper()
		ok, err := st.EnqueueIssueTrackerReminder(t.Context())
		if err != nil || ok != want {
			t.Fatal("enqueue", ok, err, "want", want)
		}
	}
	rp, err := st.GetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || rp.Enabled || rp.Revision != 0 || rp.IntervalMinutes != api.IssueTrackerReminderIntervalDefaultMinutes {
		t.Fatal(rp, err)
	}
	complete("error")
	notice := inbox(app.AccountID)[0].ID
	forceDue()
	enqueue(false)
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerReminderPolicy{Enabled: true, IntervalMinutes: api.IssueTrackerReminderIntervalMinMinutes})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := st.GetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID); err != nil || p.Revision != noticePolicy.Revision {
		t.Fatal("reminder settings invalidated incident", p, err)
	}
	enqueue(false) // Enabling starts a full interval, even for an old incident.
	if _, err := st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerReminderPolicy{Enabled: true, IntervalMinutes: rp.IntervalMinutes}); err == nil {
		t.Fatal("stale reminder settings accepted")
	}
	if _, err := st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, member.ID, rp); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member changed reminder settings", err)
	}
	if _, err := st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerReminderPolicy{Revision: rp.Revision, IntervalMinutes: api.IssueTrackerReminderIntervalMaxMinutes + 1}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("invalid reminder interval", err)
	}
	until := time.Now().Add(time.Hour)
	if err := st.UpdateIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice, 0, state.IssueTrackerTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false)
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_sync_triage SET snoozed_until=clock_timestamp() WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	enqueue(false) // A full interval follows snooze expiry.
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_sync_triage SET snoozed_until=clock_timestamp()-interval '2 days' WHERE link_id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice, 1, "claim"); err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), `CREATE FUNCTION reject_reminder_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event='issue.tracker_sync.attention' THEN RAISE EXCEPTION 'outbox unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_reminder_outbox BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_reminder_outbox()`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueIssueTrackerReminder(t.Context()); err == nil {
		t.Fatal("reminder acknowledged failed outbox enqueue")
	}
	if count() != 0 || len(inbox(app.AccountID)) != 1 {
		t.Fatal("partial reminder commit")
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "DROP TRIGGER reject_reminder_outbox ON app_webhook_event_outbox; DROP FUNCTION reject_reminder_outbox()"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		queued bool
		err    error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() { ok, err := st.EnqueueIssueTrackerReminder(t.Context()); results <- result{ok, err} }()
	}
	wins := 0
	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.queued {
			wins++
		}
	}
	if wins != 1 || count() != 1 {
		t.Fatal("duplicate reminder under concurrency", wins, count())
	}
	enqueue(false)
	if p := inbox(member.ID); len(p) != 2 || p[0].Kind != "reminder" || p[0].ReminderSequence != 1 || p[0].TeamID != team.ID {
		t.Fatal("team reminder routing", p)
	}
	if v, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice); err != nil || len(v.Reminders) != 1 || v.Revision != 2 {
		t.Fatal("reminder history/claim changed", v, err)
	}
	complete("error")
	if count() != 1 {
		t.Fatal("automatic checks reset reminder budget")
	}
	if _, err := st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeAccountID: member.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	forceDue()
	if err := issuetrackers.EnqueueReminderBatch(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("reminder batch did not enqueue")
	}
	if p := inbox(member.ID); p[0].ReminderSequence != 2 || p[0].TeamID != "" {
		t.Fatal("reminder ignored reassignment", p)
	}
	if err := full.RemoveOrgMember(t.Context(), org.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(true)
	if len(inbox(member.ID)) != 0 {
		t.Fatal("removed member retained reminder inbox")
	}
	if p := inbox(app.AccountID); p[0].ReminderSequence != 3 || p[0].TeamID != "" {
		t.Fatal("owner fallback", p)
	}
	forceDue()
	enqueue(false)
	rp.Enabled = false
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	rp.Enabled = true
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false) // Reconfiguration cannot replay the capped incident.
	complete("matched")
	enqueue(false)
	if v, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, notice); err != nil || v.Active || len(v.Reminders) != api.IssueTrackerRemindersMax {
		t.Fatal("recovery lost reminder history", v, err)
	}
	complete("error")
	fresh := inbox(app.AccountID)[0].ID
	if count() != 0 {
		t.Fatal("new incident inherited reminder budget")
	}
	if err := st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, fresh, 0, "acknowledge"); err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false)
	complete("matched")
	complete("error")
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false)
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='pro',status='suspended' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	enqueue(false)
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET status='active',abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	enqueue(false)
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET abuse_hold_at=NULL,abuse_hold_reason=NULL WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	noticePolicy.Enabled = false
	noticePolicy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, noticePolicy)
	if err != nil {
		t.Fatal(err)
	}
	enqueue(false)
	noticePolicy.Enabled = true
	noticePolicy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, noticePolicy)
	if err != nil {
		t.Fatal(err)
	}
	enqueue(false) // An old incident cannot bypass the notification settings revision.
	complete("error")
	forceDue()
	enqueue(true)
	rp.Enabled = false
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false)
	rp.Enabled = true
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	enqueue(false) // Re-enabling restarts the waiting interval, without resetting count.
	if count() != 1 {
		t.Fatal("reminder opt-out reset count")
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	req := httptest.NewRequest("GET", pageURL, nil)
	e.AddAdminSession(t, req)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, req)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "interval_minutes") {
		t.Fatal("reminder settings page", page.Code, page.Body.String())
	}
	post := func(valid bool) *httptest.ResponseRecorder {
		form := url.Values{"settings_kind": {"reminders"}, "enabled": {"1"}, "expected_revision": {fmt.Sprint(rp.Revision)}, "interval_minutes": {fmt.Sprint(rp.IntervalMinutes)}}
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			r.AddCookie(csrf)
		}
		e.AddAdminSession(t, r)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		return out
	}
	if out := post(false); out.Code != 400 {
		t.Fatal("reminder CSRF bypass", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("reminder save", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("stale reminder form", out.Code)
	}
	if _, err := st.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	reminderDeliveries := 0
	ids := map[string]bool{}
	for _, d := range deliveries {
		var p map[string]any
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["kind"] == "reminder" {
			reminderDeliveries++
			id, _ := p["id"].(string)
			if id == "" || ids[id] || p["health_path"] == nil || p["reminder_sequence"] == nil {
				t.Fatal("reminder payload/dedup", p)
			}
			ids[id] = true
		}
	}
	if reminderDeliveries != 4 {
		t.Fatal("unexpected reminder delivery count", reminderDeliveries)
	}
	if _, err := st.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	again, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(again) != len(deliveries) {
		t.Fatal("duplicate reminder fanout", len(again), len(deliveries), err)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE apps SET deleted_at=clock_timestamp() WHERE id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	forceDue()
	enqueue(false)
}

func TestNativeIssueTrackerEscalationRoutesOnceAndStopsSafely(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "tracker-escalation")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Reminder", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	full := apidserver.IssueNativeStore(e)
	st := state.NewPgStore(*e.Pool())
	org, err := full.CreateOrg(t.Context(), state.Org{Slug: "escalation-" + uuid.NewString()[:8], Name: "Reminders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := full.CreateAccount(t.Context(), "reminder-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, app.AccountID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "reminders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID, member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	link, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, accepted.IssueID, app.AccountID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: "42", URL: link.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	noticePolicy, err := st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AppID: app.ID, AccountID: app.AccountID, TargetURL: "https://example.com/reminders", Enabled: true, EventFilter: []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}, SecretSealed: []byte("fake-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	backupMember, err := full.CreateAccount(t.Context(), "backup-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, backupMember.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	backup, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "backup", api.PutIssueTeamRequest{Name: "Backup responders", MemberAccountIDs: []string{backupMember.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rp, err := st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerReminderPolicy{Enabled: true, IntervalMinutes: api.IssueTrackerReminderIntervalMinMinutes})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := st.GetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || ep.Enabled || ep.Revision != 0 || ep.DelayMinutes != api.IssueTrackerEscalationDelayDefaultMinutes {
		t.Fatal(ep, err)
	}
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := (*e.Pool()).Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	complete := func(outcome string) {
		t.Helper()
		if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
			t.Fatal(err)
		}
		claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		claim.Policy.Outcome = outcome
		claim.Policy.Reason = ""
		if outcome == "error" {
			claim.Policy.Reason = "provider_unavailable"
		}
		if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	inbox := func(actor string) []state.IssueTrackerSyncNotification {
		t.Helper()
		p, err := st.ListIssueTrackerSyncNotificationsView(t.Context(), app.ID, app.AccountID, actor, "", "all", state.IssueCursor{})
		if err != nil {
			t.Fatal(err)
		}
		return p.Items
	}
	escalate := func(want bool) {
		t.Helper()
		ok, err := st.EnqueueIssueTrackerEscalation(t.Context())
		if err != nil || ok != want {
			t.Fatal("escalation", ok, err, "want", want)
		}
	}
	due := func() {
		t.Helper()
		execSQL("UPDATE issue_tracker_sync_escalation_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		execSQL("UPDATE issue_tracker_sync_incidents SET first_reminded_at=clock_timestamp()-interval '2 days' WHERE link_id=$1 AND reminder_count>0", link.ID)
	}
	remind := func() {
		t.Helper()
		execSQL("UPDATE issue_tracker_sync_reminder_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		execSQL("UPDATE issue_tracker_sync_incidents SET opened_at=clock_timestamp()-interval '2 days',last_reminded_at=CASE WHEN reminder_count>0 THEN clock_timestamp()-interval '2 days' ELSE NULL END WHERE link_id=$1", link.ID)
		ok, err := st.EnqueueIssueTrackerReminder(t.Context())
		if err != nil || !ok {
			t.Fatal("reminder", ok, err)
		}
	}
	save := func(enabled bool, team string) {
		t.Helper()
		ep.Enabled, ep.TeamID = enabled, team
		ep.DelayMinutes = api.IssueTrackerEscalationDelayMinMinutes
		ep, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID, ep)
		if err != nil {
			t.Fatal(err)
		}
	}
	receipt := func() bool {
		t.Helper()
		var v bool
		if err := (*e.Pool()).QueryRow(t.Context(), "SELECT escalated_at IS NOT NULL FROM issue_tracker_sync_incidents WHERE link_id=$1", link.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	complete("error")
	notice := inbox(app.AccountID)[0].ID
	escalate(false)
	save(true, backup.ID)
	if _, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, member.ID, ep); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member configured escalation", err)
	}
	stale := ep
	stale.Revision--
	if _, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID, stale); err == nil {
		t.Fatal("stale policy accepted")
	}
	for _, invalid := range []state.IssueTrackerEscalationPolicy{{Enabled: true, DelayMinutes: 15}, {Enabled: true, TeamID: "bad", DelayMinutes: 15}, {Enabled: true, TeamID: uuid.NewString(), DelayMinutes: 15}, {TeamID: backup.ID, DelayMinutes: 14}, {TeamID: backup.ID, DelayMinutes: 10081}} {
		invalid.Revision = ep.Revision
		if _, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid escalation accepted", invalid, err)
		}
	}
	other := apidserver.SeedPGApp(t, e, "other-escalation")
	otherTeam, err := st.PutIssueTeam(t.Context(), other.ID, other.AccountID, other.AccountID, "other", api.PutIssueTeamRequest{Name: "Other", MemberAccountIDs: []string{other.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	invalid := ep
	invalid.TeamID = otherTeam.ID
	if _, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cross-app escalation", err)
	}
	if p, err := st.GetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID); err != nil || p.Revision != noticePolicy.Revision {
		t.Fatal("escalation invalidated incident", p, err)
	}
	due()
	escalate(false) // the first reminder is required
	remind()
	escalate(false) // fresh first reminder gets the full delay
	due()
	save(true, backup.ID)
	escalate(false) // configuration starts a new delay
	due()
	until := time.Now().UTC().Add(time.Hour)
	if err = st.UpdateIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice, 0, state.IssueTrackerTriageAction{Action: "snooze", SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	escalate(false)
	execSQL("UPDATE issue_tracker_sync_triage SET snoozed_until=clock_timestamp()-interval '1 minute' WHERE incident_id=(SELECT incident_id FROM issue_tracker_sync_incidents WHERE link_id=$1)", link.ID)
	escalate(false)
	execSQL("UPDATE issue_tracker_sync_triage SET snoozed_until=clock_timestamp()-interval '2 days' WHERE incident_id=(SELECT incident_id FROM issue_tracker_sync_incidents WHERE link_id=$1)", link.ID)
	if err = st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice, 1, "claim"); err != nil {
		t.Fatal(err)
	}
	remind() // A later reminder must not move the first-reminder escalation deadline.
	execSQL(`CREATE FUNCTION reject_escalation_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload->>'kind'='escalation' THEN RAISE EXCEPTION 'outbox unavailable'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_escalation_outbox BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_escalation_outbox()`)
	if _, err = st.EnqueueIssueTrackerEscalation(t.Context()); err == nil {
		t.Fatal("outbox failure committed escalation")
	}
	if receipt() {
		t.Fatal("partial escalation receipt")
	}
	for _, n := range inbox(app.AccountID) {
		if n.Kind == "escalation" {
			t.Fatal("partial escalation notice")
		}
	}
	execSQL("DROP TRIGGER reject_escalation_outbox ON app_webhook_event_outbox; DROP FUNCTION reject_escalation_outbox()")
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() { ok, err := st.EnqueueIssueTrackerEscalation(t.Context()); results <- ok; errs <- err }()
	}
	wins := 0
	for range 2 {
		if <-results {
			wins++
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if wins != 1 || !receipt() {
		t.Fatal("concurrent escalation dedup", wins)
	}
	escalate(false)
	notices := inbox(backupMember.ID)
	if len(notices) != 1 || notices[0].Kind != "escalation" || notices[0].TeamID != backup.ID {
		t.Fatal("backup inbox", notices)
	}
	escalationNotice := notices[0].ID
	triage, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, backupMember.ID, escalationNotice)
	if err != nil || !triage.Active || !triage.Escalated || triage.ResponsibleTeamID != backup.ID || len(triage.Escalations) != 1 || triage.ClaimedBy != member.ID || triage.Revision != 2 {
		t.Fatal("escalation triage", triage, err)
	}
	original, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, member.ID, notice)
	if err != nil || len(original.Escalations) != 0 {
		t.Fatal("captured history scope", original, err)
	}
	execSQL("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, backupMember.ID)
	if len(inbox(backupMember.ID)) != 0 {
		t.Fatal("removed backup member retained access")
	}
	fallback, err := st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, escalationNotice)
	if err != nil || fallback.ResponsibleTeamID != "" {
		t.Fatal("empty backup responsibility", fallback, err)
	}
	execSQL("UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", org.ID, backupMember.ID)
	save(false, team.ID)
	save(true, team.ID)
	due()
	escalate(false) // completed escalation never replays or changes team
	remind()
	if got := inbox(backupMember.ID); got[0].Kind != "reminder" || got[0].TeamID != backup.ID {
		t.Fatal("escalated reminder route", got)
	}
	complete("conflict") // a new attention kind preserves escalation routing and receipt
	if got := inbox(backupMember.ID); got[0].Kind != "paused" || got[0].TeamID != backup.ID || !receipt() {
		t.Fatal("automatic check lost escalation", got)
	}
	if err = st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, backupMember.ID, escalationNotice, 2, "acknowledge"); err != nil {
		t.Fatal(err)
	}
	due()
	escalate(false)
	resumed, err := st.GetIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, resumed, resumed.Policy.Revision); err != nil {
		t.Fatal(err)
	}
	complete("matched")
	escalate(false)
	if got := inbox(backupMember.ID); got[0].Kind != "recovered" {
		t.Fatal("backup recovery", got)
	}
	triage, err = st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, backupMember.ID, escalationNotice)
	if err != nil || triage.Active || len(triage.Escalations) != 1 {
		t.Fatal("historical escalation", triage, err)
	}
	complete("error")
	if receipt() {
		t.Fatal("new incident inherited escalation")
	}
	remind()
	save(true, backup.ID)
	due()
	// Recheck eligibility at enqueue, even after an incident became overdue.
	for _, tc := range []struct{ block, restore string }{
		{"UPDATE accounts SET plan='free' WHERE id=$1", "UPDATE accounts SET plan='pro' WHERE id=$1"},
		{"UPDATE accounts SET status='suspended' WHERE id=$1", "UPDATE accounts SET status='active' WHERE id=$1"},
		{"UPDATE accounts SET abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", "UPDATE accounts SET abuse_hold_at=NULL,abuse_hold_reason=NULL WHERE id=$1"},
	} {
		execSQL(tc.block, app.AccountID)
		escalate(false)
		execSQL(tc.restore, app.AccountID)
	}
	execSQL("UPDATE issue_tracker_sync_notification_policies SET enabled=false WHERE app_id=$1", app.ID)
	escalate(false)
	execSQL("UPDATE issue_tracker_sync_notification_policies SET enabled=true,revision=revision+1 WHERE app_id=$1", app.ID)
	escalate(false)
	execSQL("UPDATE issue_tracker_sync_notification_policies SET revision=revision-1 WHERE app_id=$1", app.ID)
	rp.Enabled = false
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	escalate(false)
	rp.Enabled = true
	rp, err = st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, rp)
	if err != nil {
		t.Fatal(err)
	}
	save(false, backup.ID)
	due()
	escalate(false)
	save(true, backup.ID)
	escalate(false)
	due()
	if err = st.DeleteIssueTeam(t.Context(), app.ID, app.AccountID, backup.Key); err != nil {
		t.Fatal(err)
	}
	escalate(true)
	got := inbox(app.AccountID)[0]
	if got.Kind != "escalation" || got.TeamID != "" {
		t.Fatal("deleted backup owner fallback", got)
	}
	triage, err = st.GetIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, got.ID)
	if err != nil || triage.ResponsibleTeamID != "" || !triage.Escalated {
		t.Fatal("current fallback responsibility", triage, err)
	}
	remind()
	if got := inbox(app.AccountID)[0]; got.Kind != "reminder" || got.TeamID != "" {
		t.Fatal("fallback reminder", got)
	}
	if len(inbox(backupMember.ID)) != 0 {
		t.Fatal("deleted team retained access")
	}
	// Owner form uses existing CSRF and compare-and-swap controls.
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	req := httptest.NewRequest("GET", pageURL, nil)
	e.AddAdminSession(t, req)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, req)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "delay_minutes") {
		t.Fatal("escalation settings page", page.Code, page.Body.String())
	}
	post := func(valid bool) *httptest.ResponseRecorder {
		form := url.Values{"settings_kind": {"escalation"}, "enabled": {"1"}, "expected_revision": {fmt.Sprint(ep.Revision)}, "delay_minutes": {"15"}, "team_id": {team.ID}}
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if valid {
			r.AddCookie(csrf)
		}
		e.AddAdminSession(t, r)
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		return out
	}
	if out := post(false); out.Code != 400 {
		t.Fatal("escalation CSRF", out.Code)
	}
	if out := post(true); out.Code != 303 {
		t.Fatal("escalation save", out.Code, out.Body.String())
	}
	if out := post(true); out.Code != 409 {
		t.Fatal("escalation stale form", out.Code)
	}
	if _, err = st.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	deliveries, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	count := 0
	for _, d := range deliveries {
		var payload map[string]any
		if err = json.Unmarshal(d.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["kind"] == "escalation" {
			count++
			id, _ := payload["id"].(string)
			if id == "" || ids[id] || payload["triage_path"] == nil || payload["type"] != "issue.tracker_sync.attention" {
				t.Fatal("escalation payload", payload)
			}
			ids[id] = true
		}
	}
	if count != 2 {
		t.Fatal("escalation delivery count", count)
	}
	if _, err = st.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	again, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(again) != len(deliveries) {
		t.Fatal("duplicate delivery", len(again), len(deliveries), err)
	}
	execSQL("DELETE FROM issue_tracker_sync_notifications WHERE link_id=$1 AND kind='escalation' AND incident_id=(SELECT incident_id FROM issue_tracker_sync_incidents WHERE link_id=$1)", link.ID)
	due()
	escalate(false) // Retention never erases the durable completed-step receipt.
	complete("matched")
	complete("error")
	remind()
	due()
	latest := inbox(app.AccountID)[0].ID
	if err = st.ActOnIssueTrackerTriage(t.Context(), app.ID, app.AccountID, app.AccountID, latest, 0, "acknowledge"); err != nil {
		t.Fatal(err)
	}
	escalate(false) // acknowledgment before the escalation step stops it
	complete("matched")
	escalate(false)
	complete("error")
	remind()
	due()
	execSQL("UPDATE apps SET status='deleted',deleted_at=clock_timestamp() WHERE id=$1", app.ID)
	escalate(false)
}

func TestNativeIssueSlackDestinationsThreadRetryAndFenceDeliveries(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "tracker-slack")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "Reminder", Message: "failed"}, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	full := apidserver.IssueNativeStore(e)
	st := state.NewPgStore(*e.Pool())
	org, err := full.CreateOrg(t.Context(), state.Org{Slug: "slack-" + uuid.NewString()[:8], Name: "Reminders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := full.CreateAccount(t.Context(), "reminder-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, app.AccountID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = full.AddOrgMember(t.Context(), org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "reminders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID, member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ActOnIssue(t.Context(), app.ID, accepted.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: team.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	link, err := st.PutIssueTrackerLink(t.Context(), app.ID, app.AccountID, accepted.IssueID, app.AccountID, api.PutIssueTrackerLinkRequest{URL: "https://github.com/owner/repo/issues/42"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ConfigureIssueTrackerAutoSync(t.Context(), app.ID, app.AccountID, accepted.IssueID, state.IssueTrackerAutoSyncRecord{BindingFingerprint: strings.Repeat("a", 64), Baseline: api.IssueTrackerProviderStatus{Provider: "github", ID: "42", URL: link.URL, State: "open"}, Policy: api.IssueTrackerAutoSync{LinkID: link.ID, Enabled: true, SourceIssueID: accepted.IssueID, SourceState: "open"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	noticePolicy, err := st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerNotificationPolicy{Enabled: true, FailureThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := (*e.Pool()).Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = previous })
	if _, err = st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, team.Key, api.PutIssueTeamRequest{Name: team.Name, MemberAccountIDs: []string{member.ID}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	complete := func(outcome string) {
		t.Helper()
		if err := apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
			t.Fatal(err)
		}
		claim, err := st.ClaimIssueTrackerAutoSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		claim.Policy.Outcome = outcome
		claim.Policy.Reason = ""
		if outcome == "error" {
			claim.Policy.Reason = "provider_unavailable"
		}
		if _, err = st.FinishIssueTrackerAutoSync(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := func() []state.IssueSlackDelivery {
		t.Helper()
		out, err := st.ListIssueSlackDeliveries(t.Context(), app.ID, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	forceDue := func() {
		t.Helper()
		execSQL("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
		execSQL("UPDATE issue_team_slack_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE destination_id IN (SELECT id FROM issue_team_slack_destinations WHERE app_id=$1)", app.ID)
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	getAs := func(account state.Account) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", pageURL, nil)
		old := *e.Acct()
		*e.Acct() = account
		e.AddAdminSession(t, r)
		*e.Acct() = old
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		return out
	}
	csrfFor := func(account state.Account) *http.Cookie {
		t.Helper()
		p := getAs(account)
		if p.Code != 200 {
			t.Fatal("settings GET", p.Code, p.Body.String())
		}
		for _, c := range p.Result().Cookies() {
			if c.Name == apidserver.DashboardIssueCookie {
				return c
			}
		}
		t.Fatal("no CSRF cookie")
		return nil
	}
	postAs := func(account state.Account, form url.Values, csrf *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if csrf != nil {
			r.AddCookie(csrf)
		}
		old := *e.Acct()
		*e.Acct() = account
		e.AddAdminSession(t, r)
		*e.Acct() = old
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		return out
	}
	hook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://example.com/notices", EventFilter: []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}, Enabled: true, SecretSealed: []byte("fake-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	complete("error")
	complete("matched")
	if len(deliveries()) != 0 {
		t.Fatal("unconfigured Slack captured notices")
	}
	csrf := csrfFor(*e.Acct())
	form := url.Values{"settings_kind": {"slack_destination"}, "team_id": {team.ID}, "channel_id": {"C0123456789"}, "bot_token": {"xoxb-fake-token"}, "enabled": {"1"}, "expected_revision": {"0"}}
	if out := postAs(*e.Acct(), form, nil); out.Code != 400 {
		t.Fatal("Slack CSRF bypass", out.Code)
	}
	form.Set("csrf_token", csrf.Value)
	if out := postAs(*e.Acct(), form, csrf); out.Code != 303 {
		t.Fatal("Slack save", out.Code, out.Body.String())
	}
	if out := postAs(*e.Acct(), form, csrf); out.Code != 409 {
		t.Fatal("Slack stale save", out.Code)
	}
	destinations, err := st.ListIssueSlackDestinations(t.Context(), app.ID, app.AccountID)
	if err != nil || len(destinations) != 1 || !destinations[0].Enabled {
		t.Fatal(destinations, err)
	}
	destination := destinations[0]
	if len(deliveries()) != 0 {
		t.Fatal("destination opt-in replayed old notices")
	}
	var sealed []byte
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT token_sealed FROM issue_team_slack_destinations WHERE id=$1", destination.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "xoxb-fake-token") {
		t.Fatal("plaintext token stored")
	}
	if token, err := issueslack.OpenToken([]*age.X25519Identity{identity}, sealed); err != nil || token != "xoxb-fake-token" {
		t.Fatal("sealed token", err)
	}
	if page := getAs(*e.Acct()); page.Code != 200 || strings.Contains(page.Body.String(), "xoxb-fake-token") {
		t.Fatal("token leaked to settings", page.Code)
	}
	if _, err = st.SetIssueSlackDestination(t.Context(), app.ID, member.ID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: destination.ChannelID, Revision: destination.Revision}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member changed Slack destination", err)
	}
	for _, bad := range []state.PutIssueSlackDestination{{TeamID: uuid.NewString(), ChannelID: destination.ChannelID}, {TeamID: team.ID, ChannelID: "https://hooks.slack.com/services/secret"}, {TeamID: team.ID, ChannelID: "general"}} {
		bad.Revision = destination.Revision
		if _, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("bad Slack settings", err)
		}
	}
	// A delivery enqueue failure rolls back the source notice and incident as well.
	execSQL(`CREATE FUNCTION reject_slack_queue() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'Slack queue unavailable'; END $$; CREATE TRIGGER reject_slack_queue BEFORE INSERT ON issue_team_slack_deliveries FOR EACH ROW EXECUTE FUNCTION reject_slack_queue()`)
	if err = apidserver.IssueNativeAutoSyncDue(e, t.Context(), link.ID); err != nil {
		t.Fatal(err)
	}
	failedClaim, err := st.ClaimIssueTrackerAutoSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	failedClaim.Policy.Outcome = "error"
	failedClaim.Policy.Reason = "provider_unavailable"
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), failedClaim); err == nil {
		t.Fatal("source committed failed Slack enqueue")
	}
	if len(deliveries()) != 0 {
		t.Fatal("partial Slack delivery")
	}
	execSQL("DROP TRIGGER reject_slack_queue ON issue_team_slack_deliveries; DROP FUNCTION reject_slack_queue()")
	if _, err = st.FinishIssueTrackerAutoSync(t.Context(), failedClaim); err != nil {
		t.Fatal(err)
	}
	if got := deliveries(); len(got) != 1 || got[0].Kind != "failure" || got[0].Status != "pending" {
		t.Fatal("initial Slack enqueue", got)
	}
	memberCSRF := csrfFor(member)
	form.Set("csrf_token", memberCSRF.Value)
	if out := postAs(member, form, memberCSRF); out.Code != 403 {
		t.Fatal("member Slack config", out.Code, out.Body.String())
	}
	if page := getAs(member); page.Code != 200 || strings.Contains(page.Body.String(), `name="bot_token"`) || strings.Contains(page.Body.String(), "Recent Slack deliveries") {
		t.Fatal("member saw owner Slack controls", page.Code, page.Body.String())
	}
	// Claims serialize the destination and fence stale receipts.
	type slackClaimResult struct {
		claim state.IssueSlackClaim
		found bool
		err   error
	}
	claims := make(chan slackClaimResult, 2)
	for range 2 {
		go func() { c, ok, err := st.ClaimIssueSlackDelivery(t.Context()); claims <- slackClaimResult{c, ok, err} }()
	}
	wins := 0
	var first state.IssueSlackClaim
	for range 2 {
		result := <-claims
		c, ok, err := result.claim, result.found, result.err
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			wins++
			first = c
		}
	}
	if wins != 1 || first.Attempt != 1 || first.ThreadTS != "" {
		t.Fatal("concurrent Slack claim", wins, first.Status)
	}
	if err = st.FinishIssueSlackDelivery(t.Context(), first, state.IssueSlackResult{Status: "failed", Error: "transport_error", RetryAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = st.RetryIssueSlackDelivery(t.Context(), app.ID, member.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member retried delivery", err)
	}
	// Queue a reminder behind the failed root; it must not overtake the retry.
	rp, err := st.SetIssueTrackerReminderPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerReminderPolicy{Enabled: true, IntervalMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	remind := func() {
		t.Helper()
		execSQL("UPDATE issue_tracker_sync_reminder_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
		execSQL("UPDATE issue_tracker_sync_incidents SET opened_at=clock_timestamp()-interval '2 days',last_reminded_at=CASE WHEN reminder_count>0 THEN clock_timestamp()-interval '2 days' ELSE NULL END WHERE link_id=$1", link.ID)
		ok, err := st.EnqueueIssueTrackerReminder(t.Context())
		if err != nil || !ok {
			t.Fatal("reminder", ok, err)
		}
	}
	remind()
	execSQL("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE id=$1", destination.ID)
	if c, ok, err := st.ClaimIssueSlackDelivery(t.Context()); err != nil || ok {
		t.Fatal("reminder overtook failed root", c.Kind, ok, err)
	}
	// A fake Slack API receives all HTTP. The configured provider URL stays fixed.
	var mu sync.Mutex
	requests := []map[string]any{}
	mode := "ok"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-fake-token" {
			t.Error("Slack authentication")
		}
		mu.Lock()
		requests = append(requests, payload)
		m := mode
		n := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch m {
		case "rate":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
		case "auth":
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		default:
			_, _ = fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"1000.%06d"}`, payload["channel"], n)
		}
	}))
	defer fake.Close()
	target, _ := url.Parse(fake.URL)
	client := issueslack.NewClient(&http.Client{Transport: issueSlackTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			t.Error("nonfixed Slack endpoint")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Host, u.Scheme = target.Host, target.Scheme
		copy.URL = &u
		return fake.Client().Transport.RoundTrip(copy)
	})})
	worker := issueslack.Worker{Store: st, Client: client, OpenToken: func(blob []byte) (string, error) { return issueslack.OpenToken([]*age.X25519Identity{identity}, blob) }, DashboardBase: "https://gregale.example"}
	step := func(want bool) {
		t.Helper()
		ok, err := worker.Step(t.Context())
		if err != nil || ok != want {
			t.Fatal("Slack step", ok, err, "want", want)
		}
	}
	forceDue()
	step(true)
	if got := deliveries(); got[len(got)-1].Status != "succeeded" || got[len(got)-1].Attempts != 2 {
		t.Fatal("root success", got)
	}
	// Success persists the thread root; further posts are replies with independent stable IDs.
	forceDue()
	step(true)
	mu.Lock()
	root, replied := requests[0], requests[1]
	mu.Unlock()
	if root["thread_ts"] != nil || replied["thread_ts"] != "1000.000001" || root["client_msg_id"] != first.ID || root["client_msg_id"] == replied["client_msg_id"] {
		t.Fatal("threading", root, replied)
	}
	if err = st.FinishIssueSlackDelivery(t.Context(), first, state.IssueSlackResult{Status: "succeeded", MessageTS: "999.999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("stale Slack receipt accepted", err)
	}
	// Retries and rate limiting retain the root and event ID.
	remind()
	forceDue()
	mu.Lock()
	mode = "rate"
	mu.Unlock()
	// Anchor Retry-After to the send, allowing receipt/history database latency.
	rateStarted := time.Now()
	step(true)
	latest := deliveries()[0]
	if latest.Status != "failed" || latest.LastError != "rate_limited" || latest.NextAttemptAt.Before(rateStarted.Add(119*time.Second)) {
		t.Fatal("rate limit", latest)
	}
	step(false)
	forceDue()
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	step(true)
	mu.Lock()
	if requests[2]["client_msg_id"] != requests[3]["client_msg_id"] || requests[3]["thread_ts"] != "1000.000001" {
		t.Error("retry identity/thread", requests[2], requests[3])
	}
	mu.Unlock()
	// A permanent Slack error is visible, retryable by the owner, and never contains raw responses.
	complete("matched")
	forceDue()
	mu.Lock()
	mode = "auth"
	mu.Unlock()
	step(true)
	latest = deliveries()[0]
	if latest.Status != "dead" || latest.LastError != "invalid_auth" || !latest.CanRetry {
		t.Fatal("Slack permanent failure", latest)
	}
	csrf = csrfFor(*e.Acct())
	retryForm := url.Values{"settings_kind": {"slack_retry"}, "delivery_id": {latest.ID}, "csrf_token": {csrf.Value}}
	if out := postAs(*e.Acct(), retryForm, csrf); out.Code != 303 {
		t.Fatal("owner Slack retry", out.Code, out.Body.String())
	}
	forceDue()
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	step(true)
	if got := deliveries()[0]; got.Status != "succeeded" || got.Attempts != 2 {
		t.Fatal("manual retry history", got)
	}
	// New incidents start a new root; an expired worker cannot finish a replacement claim.
	complete("error")
	forceDue()
	old, ok, err := st.ClaimIssueSlackDelivery(t.Context())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	execSQL("UPDATE issue_team_slack_deliveries SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", old.ID)
	execSQL("UPDATE issue_team_slack_destinations SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", destination.ID)
	replacement, ok, err := st.ClaimIssueSlackDelivery(t.Context())
	if err != nil || !ok || replacement.ID != old.ID || replacement.ClaimID == old.ClaimID || replacement.ThreadTS != "" {
		t.Fatal("lease reclaim", ok, err)
	}
	if err = st.FinishIssueSlackDelivery(t.Context(), old, state.IssueSlackResult{Status: "succeeded", MessageTS: "999.999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("expired claim finished", err)
	}
	// Rotating to another channel cancels in-flight and queued jobs and fences their completion.
	destination, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C9876543210", Enabled: true, Revision: destination.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if ready, err := st.IssueSlackDeliveryReady(t.Context(), replacement); err != nil || ready {
		t.Fatal("rotated claim still ready", ready, err)
	}
	if err = st.FinishIssueSlackDelivery(t.Context(), replacement, state.IssueSlackResult{Status: "succeeded", MessageTS: "999.999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatal("rotated claim finished", err)
	}
	if got := deliveries()[0]; got.Status != "canceled" || got.ChannelID != "C0123456789" || got.CanRetry {
		t.Fatal("historical channel changed", got)
	}
	forceDue()
	step(false)
	// A new notice uses the new channel and a new root. Membership loss cancels before HTTP.
	remind()
	forceDue()
	held, ok, err := st.ClaimIssueSlackDelivery(t.Context())
	if err != nil || !ok || held.ChannelID != "C9876543210" || held.ThreadTS != "" {
		t.Fatal("new channel claim", ok, err, held.ChannelID)
	}
	execSQL("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	if ready, err := st.IssueSlackDeliveryReady(t.Context(), held); err != nil || ready {
		t.Fatal("removed member ready", ready, err)
	}
	if err = st.FinishIssueSlackDelivery(t.Context(), held, state.IssueSlackResult{Status: "canceled", Error: "destination_unavailable"}); err != nil {
		t.Fatal(err)
	}
	execSQL("UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	complete("matched")
	forceDue()
	step(true)
	mu.Lock()
	last := requests[len(requests)-1]
	mu.Unlock()
	if last["channel"] != "C9876543210" || last["thread_ts"] != nil {
		t.Fatal("new destination thread", last)
	}
	complete("error")
	forceDue()
	// Revoked eligibility and opt-out stop already queued Slack work.
	execSQL("UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID)
	step(true)
	if got := deliveries()[0]; got.Status != "canceled" {
		t.Fatal("ineligible owner delivered", got)
	}
	execSQL("UPDATE accounts SET plan='pro' WHERE id=$1", app.AccountID)
	complete("matched")
	forceDue()
	noticePolicy.Enabled = false
	noticePolicy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, noticePolicy)
	if err != nil {
		t.Fatal(err)
	}
	step(true)
	if got := deliveries()[0]; got.Status != "canceled" {
		t.Fatal("notification opt-out ignored", got)
	}
	noticePolicy.Enabled = true
	noticePolicy, err = st.SetIssueTrackerNotificationPolicy(t.Context(), app.ID, app.AccountID, noticePolicy)
	if err != nil {
		t.Fatal(err)
	}
	complete("error")
	forceDue()
	execSQL("UPDATE issue_team_slack_deliveries SET expires_at=clock_timestamp()-interval '1 second',next_attempt_at=clock_timestamp()+interval '1 day' WHERE status='pending'")
	execSQL("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()+interval '1 day' WHERE id=$1", destination.ID)
	step(true)
	if got := deliveries()[0]; got.Status != "dead" || got.LastError != "delivery_expired" {
		t.Fatal("expired delivery sent", got)
	}
	if err = st.RetryIssueSlackDelivery(t.Context(), app.ID, app.AccountID, deliveries()[0].ID); err != nil {
		t.Fatal(err)
	}
	forceDue()
	step(true)
	// At the retry cap an abandoned claim becomes terminal without another HTTP call.
	remind()
	forceDue()
	capClaim, ok, err := st.ClaimIssueSlackDelivery(t.Context())
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	execSQL("UPDATE issue_team_slack_deliveries SET attempt=$2,claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", capClaim.ID, api.IssueSlackMaxAttempts)
	execSQL("UPDATE issue_team_slack_destinations SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", destination.ID)
	mu.Lock()
	before := len(requests)
	mu.Unlock()
	step(true)
	mu.Lock()
	after := len(requests)
	mu.Unlock()
	if before != after || deliveries()[0].LastError != "attempts_exhausted" {
		t.Fatal("retry cap", before, after, deliveries()[0])
	}
	// Escalation creates a separate destination thread; recovery follows that route.
	backup, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "slack-backup", api.PutIssueTeamRequest{Name: "Backup", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: backup.ID, ChannelID: "C2222222222", Enabled: true, TokenSealed: sealed})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SetIssueTrackerEscalationPolicy(t.Context(), app.ID, app.AccountID, state.IssueTrackerEscalationPolicy{Enabled: true, TeamID: backup.ID, DelayMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	execSQL("UPDATE issue_tracker_sync_incidents SET first_reminded_at=clock_timestamp()-interval '2 days' WHERE link_id=$1", link.ID)
	execSQL("UPDATE issue_tracker_sync_escalation_policies SET updated_at=clock_timestamp()-interval '2 days' WHERE app_id=$1", app.ID)
	if ok, err := st.EnqueueIssueTrackerEscalation(t.Context()); err != nil || !ok {
		t.Fatal("Slack escalation", ok, err)
	}
	forceDue()
	step(true)
	mu.Lock()
	backupRoot := requests[len(requests)-1]
	backupRootTS := fmt.Sprintf("1000.%06d", len(requests))
	mu.Unlock()
	if backupRoot["channel"] != "C2222222222" || backupRoot["thread_ts"] != nil {
		t.Fatal("escalation Slack thread", backupRoot)
	}
	complete("matched")
	forceDue()
	step(true)
	mu.Lock()
	backupReply := requests[len(requests)-1]
	mu.Unlock()
	if backupReply["channel"] != "C2222222222" || backupReply["thread_ts"] != backupRootTS {
		t.Fatal("escalation Slack recovery", backupReply)
	}
	destination, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: destination.ChannelID, Revision: destination.Revision, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	oldCount := len(deliveries())
	complete("error")
	if len(deliveries()) != oldCount {
		t.Fatal("disabled destination captured new alert")
	}
	complete("matched")
	destination, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: destination.ChannelID, Revision: destination.Revision, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	complete("error")
	forceDue()
	// App webhook delivery remains independent of Slack: no destination URL or bot token enters its payload.
	if _, err = st.DrainAppWebhookEventOutbox(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	appDeliveries, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(appDeliveries) == 0 {
		t.Fatal("app webhook delivery", len(appDeliveries), err)
	}
	for _, d := range appDeliveries {
		if strings.Contains(string(d.Payload), "xoxb-fake-token") || strings.Contains(string(d.Payload), "C0123456789") {
			t.Fatal("Slack credential or destination leaked into app webhook")
		}
	}

	execSQL("UPDATE apps SET status='deleted',deleted_at=clock_timestamp() WHERE id=$1", app.ID)
	step(true)
	if got := deliveries()[0]; got.Status != "canceled" {
		t.Fatal("deleted app sent Slack", got)
	}

	// Retention removes dependent delivery records and orphan thread roots.
	execSQL("DELETE FROM issue_tracker_sync_notifications WHERE app_id=$1", app.ID)
	if err = st.CleanupIssueSlackThreads(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(deliveries()) != 0 {
		t.Fatal("delivery retention escaped source bounds")
	}
	var roots int
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT count(*) FROM issue_team_slack_threads WHERE destination_id=$1", destination.ID).Scan(&roots); err != nil || roots != 0 {
		t.Fatal("orphan roots", roots, err)
	}
	_ = rp
}

type issueSlackTestTransport func(*http.Request) (*http.Response, error)

func (f issueSlackTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
