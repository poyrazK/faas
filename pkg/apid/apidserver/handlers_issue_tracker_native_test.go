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

// adr: 1216
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

// adr: 1217
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

// adr: 1218
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

// adr: 1221
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

// adr: 1225
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

// adr: 1226
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
			configure("both", 1)
			act(api.IssueActionRequest{Action: "resolve", FixedDeploymentID: fixed.ID})
			run()
			if out := policy(); out.Outcome != "applied" || out.ProviderState != "resolved" || writes() != 1 {
				t.Fatal("both direction local push failed", out, writes())
			}
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
			detail = apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", issuePath, nil, nil), 200)
			if detail.Issue.State != "open" {
				t.Fatal("provider closure resolved without deployment")
			}
			paused := apidserver.IssueDecode[api.IssueTrackerAutoSync](t, e.Do(t, "PUT", trackerPath+"/auto-sync", api.UpdateIssueTrackerAutoSyncRequest{Enabled: false, ExpectedRevision: 2}, nil), 200)
			if paused.Enabled || paused.Direction != "both" || writes() != 1 {
				t.Fatal("pause failed", paused)
			}
		})
	}
}

// adr: 1226
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
