package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardOnboardingBoundaryStore interface {
	state.Store
	state.ApplicationStandardEnrollmentStore
	state.ApplicationStandardOperationStore
	applicationStandardWorkerStore
}

type standardOnboardingBoundaryEnv struct {
	store       standardOnboardingBoundaryStore
	server      *server
	handler     http.Handler
	owner       state.CreateAccountWithPersonalOrgResult
	key         string
	assignment  string
	destination api.ApplicationStandardLogDestination
	publisher   api.ApplicationStandardPublisher
	github      *appsNewFake
}

func standardOnboardingBoundaryRequest(t *testing.T, e standardOnboardingBoundaryEnv, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+e.key)
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, r)
	return response
}

func standardOnboardingBoundaryResponse[T any](t *testing.T, r *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if r.Code != status {
		t.Fatalf("onboarding status=%d, want=%d: %s", r.Code, status, r.Body.String())
	}
	var value T
	if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func newStandardOnboardingBoundary(t *testing.T, store standardOnboardingBoundaryStore) standardOnboardingBoundaryEnv {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	owner, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: "onboarding-boundary@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), owner.Account.ID, hash, "onboarding", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	gh := &appsNewFake{repos: []Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", defaultBranch: "main", bindReturn: "boundary-binding"}
	s := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, gh, nil, nil, 0, "")
	s.WithApplicationStandardMutationsEnabled(true)
	e := standardOnboardingBoundaryEnv{store: store, owner: owner, server: s, handler: s.handler(), key: plain, github: gh}
	orgPath := "/v1/orgs/" + owner.PersonalOrg.Slug
	e.destination = standardOnboardingBoundaryResponse[api.ApplicationStandardLogDestination](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, orgPath+"/application-standard-log-destinations", api.CreateApplicationStandardLogDestinationRequest{Name: "Company logs", Kind: "http_json", TargetURL: "https://company.example/logs"}), http.StatusCreated)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	e.publisher = standardOnboardingBoundaryResponse[api.ApplicationStandardPublisher](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, orgPath+"/application-standard-publishers", api.CreateApplicationStandardPublisherRequest{Name: "Company CI", PublicKeyDER: base64.StdEncoding.EncodeToString(der)}), http.StatusCreated)
	destinations, _ := json.Marshal([]string{e.destination.ID})
	publishers, _ := json.Marshal([]string{e.publisher.ID})
	definition := appstandards.Definition{
		appstandards.LogDestinations:   {Mode: appstandards.Mandatory, Value: destinations},
		appstandards.TrustedPublishers: {Mode: appstandards.Mandatory, Value: publishers},
		appstandards.RequireSigned:     {Mode: appstandards.Mandatory, Value: json.RawMessage(`true`)},
		appstandards.SecurityPolicy:    {Mode: appstandards.Mandatory, Value: json.RawMessage(`"warn"`)},
		appstandards.EgressCIDRs:       {Mode: appstandards.Mandatory, Value: json.RawMessage(`["8.8.8.0/24"]`)},
		appstandards.EgressExtraPorts:  {Mode: appstandards.Mandatory, Value: json.RawMessage(`[8443]`)},
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	v := standardOnboardingBoundaryResponse[api.ApplicationStandardVersion](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, orgPath+"/application-standards/company-baseline/versions", api.CreateApplicationStandardVersionRequest{Definition: encoded}), http.StatusCreated)
	p := standardOnboardingBoundaryResponse[api.ApplicationStandardReview](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, orgPath+"/application-standard-reviews", api.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: v.StandardID, AdmissionVersion: v.Version, Active: true, BatchSize: 1}), http.StatusCreated)
	operation := standardOnboardingBoundaryResponse[api.ApplicationStandardOperation](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, orgPath+"/application-standard-reviews/"+p.ID+"/approve", api.ApproveApplicationStandardReviewRequest{ApprovalHash: p.ApprovalHash}), http.StatusCreated)
	e.assignment = operation.AssignmentID
	s.runApplicationStandardPass(t.Context(), store, "initial-onboarding-admission")
	completed, err := store.GetApplicationStandardOperation(t.Context(), owner.PersonalOrg.ID, operation.ID)
	if err != nil || completed.State != "completed" || len(completed.Targets) != 0 {
		t.Fatalf("empty admission operation: %+v %v", completed, err)
	}
	return e
}

func TestMemApplicationStandardOnboardingBoundary(t *testing.T) {
	standardOnboardingBoundary(t, state.NewMemStore())
}

// HTTP adapters and the production repair pass prove persisted intent and
// admission here; no native execution, registry or log provider is simulated.
func standardOnboardingBoundary(t *testing.T, store standardOnboardingBoundaryStore) {
	t.Helper()
	e := newStandardOnboardingBoundary(t, store)
	parent := standardOnboardingBoundaryResponse[api.AppResponse](t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "boundary-parent"}), http.StatusCreated)
	type createCase struct {
		name, method, path string
		body               any
		dev                bool
	}
	cases := []createCase{
		{name: "account", method: http.MethodPost, path: "/v1/apps", body: api.CreateAppRequest{Slug: "boundary-account"}},
		{name: "organization", method: http.MethodPost, path: "/v1/orgs/" + e.owner.PersonalOrg.Slug + "/apps", body: api.CreateAppRequest{Slug: "boundary-org"}},
		{name: "pull request preview", method: http.MethodPost, path: "/v1/apps/" + parent.Slug + "/previews", body: api.CreatePreviewRequest{PRNumber: 31}},
		{name: "developer session", method: http.MethodPut, path: "/v1/dev/sessions/boundary-project", body: api.UpsertDevSessionRequest{WorkspaceID: strings.Repeat("a", 32)}, dev: true},
	}
	apps := []api.AppResponse{parent}
	standardOnboardingPending(t, e, parent)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := standardOnboardingBoundaryRequest(t, e, tc.method, tc.path, tc.body)
			app := api.AppResponse{}
			if tc.dev {
				app = standardOnboardingBoundaryResponse[api.DevSessionResponse](t, r, http.StatusCreated).App
			} else {
				app = standardOnboardingBoundaryResponse[api.AppResponse](t, r, http.StatusCreated)
			}
			standardOnboardingPending(t, e, app)
			if tc.dev || tc.name == "pull request preview" {
				retry := standardOnboardingBoundaryRequest(t, e, tc.method, tc.path, tc.body)
				if tc.dev {
					if got := standardOnboardingBoundaryResponse[api.DevSessionResponse](t, retry, http.StatusOK).App.ID; got != app.ID {
						t.Fatal("developer retry changed identity")
					}
				} else if got := standardOnboardingBoundaryResponse[api.AppResponse](t, retry, http.StatusOK).ID; got != app.ID {
					t.Fatal("preview retry changed identity")
				}
				standardOnboardingPending(t, e, app)
			}
			apps = append(apps, app)
		})
	}
	t.Run("GitHub dashboard wizard", func(t *testing.T) {
		apps = append(apps, standardOnboardingWizard(t, e))
	})
	restarted := &server{store: store, log: e.server.log}
	restarted.runApplicationStandardPass(t.Context(), store, "restarted-onboarding-worker")
	for _, app := range apps {
		standardOnboardingPersisted(t, e, app)
	}
}

func standardOnboardingPending(t *testing.T, e standardOnboardingBoundaryEnv, app api.AppResponse) {
	t.Helper()
	row, err := e.store.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, app.ID)
	if err != nil || row.State != "pending" || row.DesiredRevision != 1 || row.PersistedRevision != 0 || row.ObservedRevision != 0 || len(row.Adoptions) != 1 || row.Adoptions[0].AssignmentID != e.assignment || row.Adoptions[0].Version != 1 {
		t.Fatalf("creation missed pinned enrollment: %+v %v", row, err)
	}
	body := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("1", 64)}
	assertProblem(t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", body), http.StatusConflict, api.CodeApplicationStandardsPending)
	deployments, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil || len(deployments) != 0 {
		t.Fatalf("pending creation admitted deployment: %+v %v", deployments, err)
	}
}

func standardOnboardingPersisted(t *testing.T, e standardOnboardingBoundaryEnv, app api.AppResponse) {
	t.Helper()
	row, err := e.store.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, app.ID)
	if err != nil || row.State != "persisted" || row.DesiredRevision != 1 || row.PersistedRevision != 1 || row.ObservedRevision != 0 || len(row.Adoptions) != 1 || row.Adoptions[0].AssignmentID != e.assignment || row.Adoptions[0].Version != 1 {
		t.Fatalf("repair did not retain enrollment: %+v %v", row, err)
	}
	actual, err := e.store.AppByID(t.Context(), app.ID)
	if err != nil || actual.OrgID != e.owner.PersonalOrg.ID || !actual.RequireSigned || actual.SecurityPolicy != api.AppSecurityPolicyWarn || len(actual.EgressAllowlist) != 1 || actual.EgressAllowlist[0].String() != "8.8.8.0/24" || !reflect.DeepEqual(actual.EgressPorts, []int{8443}) {
		t.Fatalf("repair missed company controls: %+v %v", actual, err)
	}
	drains, err := e.store.ListAppLogDrainsForApp(t.Context(), app.ID)
	if err != nil || len(drains) != 1 || !drains[0].Enabled || drains[0].TargetURL != e.destination.TargetURL {
		t.Fatalf("repair missed company logs: %+v %v", drains, err)
	}
	der, err := base64.StdEncoding.DecodeString(e.publisher.PublicKeyDER)
	if err != nil {
		t.Fatal(err)
	}
	signers, err := e.store.ListAppTrustedSignersForApp(t.Context(), app.ID)
	if err != nil || len(signers) != 1 || !bytes.Equal(signers[0].CosignPublicKey, der) {
		t.Fatalf("repair missed approved publisher: %+v %v", signers, err)
	}
	optOut := false
	denied := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("1", 64), RequireSigned: &optOut}
	assertProblem(t, standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", denied), http.StatusForbidden, api.CodeDeploySignatureInvalid)
	body := api.CreateDeploymentRequest{Image: "registry.example/app@sha256:" + strings.Repeat("1", 64)}
	r := standardOnboardingBoundaryRequest(t, e, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", body)
	standardOnboardingBoundaryResponse[api.DeploymentResponse](t, r, http.StatusAccepted)
	rowAfter, err := e.store.GetApplicationStandardEnrollment(t.Context(), e.owner.PersonalOrg.ID, app.ID)
	if err != nil || rowAfter.ObservedRevision != 0 {
		t.Fatalf("deployment acceptance fabricated observation: %+v %v", rowAfter, err)
	}
}

func standardOnboardingWizard(t *testing.T, e standardOnboardingBoundaryEnv) api.AppResponse {
	t.Helper()
	mgr := e.server.sessions
	raw, err := mintDashboardSession(t.Context(), e.store, mgr, e.owner.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, githubWizardCreateAction, e.owner.Account.ID, githubWizardCreateCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf_token": {csrf}, "installation_id": {"42"}, "repo_full_name": {"octocat/hello"}, "production_branch": {"main"}, "slug": {"boundary-wizard"}}
	r := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: stampCookie(t, mgr, raw, "alice")})
	r.AddCookie(&http.Cookie{Name: githubWizardCreateCSRFCookie, Value: csrf})
	response := httptest.NewRecorder()
	e.handler.ServeHTTP(response, r)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard/apps/boundary-wizard?github=connected" || e.github.bindCalls != 1 {
		t.Fatalf("wizard creation/binding failed: status=%d location=%s calls=%d", response.Code, response.Header().Get("Location"), e.github.bindCalls)
	}
	app, err := e.store.AppBySlug(t.Context(), "boundary-wizard")
	if err != nil {
		t.Fatal(err)
	}
	result := api.AppResponse{ID: app.ID, Slug: app.Slug}
	standardOnboardingPending(t, e, result)
	return result
}
