package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrgOffboardingCreatorInventories(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "offboard-team", "Offboard Team", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: "company-api"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateBuild(t.Context(), dep.ID, state.DeploymentKindDockerfile, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "personal-api"}); err != nil {
		t.Fatal(err)
	}
	newOwner, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, (*e.Acct()).ID, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, (*e.Acct()).ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/apps", "/v1/deployments", "/v1/builds"} {
		t.Run(path, func(t *testing.T) {
			rec := e.DoAdmin(t, "GET", path, nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("inventory: %d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), app.ID) || strings.Contains(rec.Body.String(), dep.ID) || strings.Contains(rec.Body.String(), "company-api") {
				t.Fatalf("removed creator sees company resources: %s", rec.Body)
			}
			if path == "/v1/apps" {
				var apps []api.AppResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil || len(apps) != 1 || apps[0].Slug != "personal-api" {
					t.Fatalf("personal inventory lost: %+v %v", apps, err)
				}
			}
		})
	}
}

// TestOrgOffboardingCreatorLosesNamedAndIndirectAppRoutes covers the routes
// that name a company app outside /v1/apps/{slug}: in the body, the query
// string or another resource's URL. Their handlers checked only that the
// caller created the app, so a creator removed from the company kept reading,
// cancelling and reshaping its work, attaching domains and restoring it.
func TestOrgOffboardingCreatorLosesNamedAndIndirectAppRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := (*e.Acct()).ID
	org := apidserver.SeedSharedOrgWithOwner(t, e, "offboard-routes", "Offboard Routes", api.PlanPro)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: creator, OrgID: org.ID, Slug: "company-api"})
	if err != nil {
		t.Fatal(err)
	}
	retired, err := store.CreateApp(t.Context(), state.App{AccountID: creator, OrgID: org.ID, Slug: "company-old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDeleteAppCascade(t.Context(), retired.ID); err != nil {
		t.Fatal(err)
	}
	run := &state.WorkflowRun{AppID: app.ID, WorkflowName: "billing", DefinitionSnapshot: json.RawMessage(`{"name":"billing","steps":[{"name":"main","path":"/run"}]}`), Input: json.RawMessage(`{"customer":"private-input"}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	task, err := store.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: creator, Source: state.InvocationDelayedTask, Payload: json.RawMessage(`{}`), DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(t.Context(), app.ID, "nats", "orders", false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	triggerID := uuid.UUID(trigger.ID.Bytes).String()

	// While the creator still owns the company, every route keeps working.
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/v1/workflows/runs/" + run.ID, nil, http.StatusOK},
		{"GET", "/v1/delayed-tasks/" + task.ID, nil, http.StatusOK},
		// Legacy /v1/triggers administration covers personal apps only
		// (docs/organization-queues.md); company triggers are unreviewed.
		{"POST", "/v1/triggers/" + triggerID + "/pause", nil, http.StatusForbidden},
		{"GET", "/v1/crons?slug=company-api", nil, http.StatusOK},
		{"POST", "/v1/crons", api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/"}, http.StatusCreated},
		{"POST", "/v1/domains", api.CreateCustomDomainRequest{AppID: "company-api", Domain: "api.company.example"}, http.StatusAccepted},
	} {
		if rec := e.DoAdmin(t, c.method, c.path, c.body, nil); rec.Code != c.want {
			t.Fatalf("owner %s %s = %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
	freeze := e.DoAdmin(t, "POST", "/v1/freezes", api.CreateReleaseFreezeRequest{App: "company-api", Kind: api.ReleaseFreezeKindWindow, Reason: "launch"}, nil)
	var created api.ReleaseFreeze
	if freeze.Code != http.StatusCreated || json.Unmarshal(freeze.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatalf("owner freeze = %d: %s", freeze.Code, freeze.Body)
	}

	newOwner, _ := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator, newOwner.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, creator); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/workflows/runs/" + run.ID, nil},
		{"POST", "/v1/workflows/runs/" + run.ID + "/cancel", nil},
		{"GET", "/v1/delayed-tasks/" + task.ID, nil},
		{"DELETE", "/v1/delayed-tasks/" + task.ID, nil},
		{"POST", "/v1/triggers/" + triggerID + "/resume", nil},
		{"DELETE", "/v1/triggers/" + triggerID, nil},
		{"DELETE", "/v1/freezes/" + created.ID, nil},
		{"POST", "/v1/freezes", api.CreateReleaseFreezeRequest{App: "company-api", Kind: api.ReleaseFreezeKindWindow, Reason: "again"}},
		{"GET", "/v1/crons?slug=company-api", nil},
		{"POST", "/v1/crons", api.CreateCronRequest{AppID: app.ID, Schedule: "*/5 * * * *", Path: "/"}},
		{"POST", "/v1/domains", api.CreateCustomDomainRequest{AppID: "company-api", Domain: "takeover.attacker.example"}},
		{"POST", "/v1/deleted-apps/" + retired.ID + "/restore", nil},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if rec := e.DoAdmin(t, c.method, c.path, c.body, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("removed creator = %d, want 404: %s", rec.Code, rec.Body)
			}
		})
	}
	if got, err := store.GetWorkflowRun(t.Context(), run.ID); err != nil || got.CancelledAt != nil {
		t.Fatalf("run after refused cancel: %+v %v", got, err)
	}
	if got, err := store.InvocationByID(t.Context(), task.ID); err != nil || got.State == state.InvocationCancelled {
		t.Fatalf("delayed task after refused cancel: %+v %v", got, err)
	}
	if got, err := store.AppByID(t.Context(), retired.ID); err != nil || got.Status != state.AppDeleted {
		t.Fatalf("retired app after refused restore: %+v %v", got, err)
	}
}
