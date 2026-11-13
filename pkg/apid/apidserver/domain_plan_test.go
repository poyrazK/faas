package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDomainPlanHTTPAtomicApplyAndReplay(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store
			var account state.Account
			var call func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder
			if backend == "pg" {
				e := apidserver.SetupPGHandler(t, api.PlanPro)
				store = *e.Store()
				account = *e.Acct()
				call = e.Do
			} else {
				e := apidserver.Setup(t, api.PlanPro)
				store = *e.Store()
				account = *e.Acct()
				call = e.Do
			}
			source, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "domain-source", Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			target, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, OrgID: source.OrgID, Slug: "domain-target", Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: target.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
				t.Fatal(err)
			}
			d, err := store.CreateCustomDomain(t.Context(), "move-http.example.com", source.ID, "keep-proof")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDomainVerified(t.Context(), d.Domain); err != nil {
				t.Fatal(err)
			}
			manifest := api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: d.Domain, App: target.Slug}, {Domain: "new-http.example.com", App: target.Slug}}}
			r := call(t, http.MethodPost, "/v1/domains/plan", manifest, nil)
			var p api.DomainPlan
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &p) != nil {
				t.Fatalf("plan=%d %s", r.Code, r.Body)
			}
			if len(p.Changes) != 2 || p.Changes[0].Action != "move" || p.Changes[1].Action != "create" {
				t.Fatalf("plan=%+v", p)
			}
			if current, _ := store.DomainByName(t.Context(), d.Domain); current.AppID != source.ID {
				t.Fatal("HTTP plan mutated the binding")
			}
			request := api.DomainApplyRequest{Manifest: p.Manifest, ExpectedPlanSHA256: p.SHA256}
			if r := call(t, http.MethodPost, "/v1/domains/apply", request, nil); r.Code != 400 {
				t.Fatalf("missing confirmation=%d %s", r.Code, r.Body)
			}
			request.Confirm = true
			headers := map[string]string{"Idempotency-Key": p.SHA256}
			r = call(t, http.MethodPost, "/v1/domains/apply", request, headers)
			var applied api.DomainApplyResponse
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &applied) != nil {
				t.Fatalf("apply=%d %s", r.Code, r.Body)
			}
			if len(applied.Domains) != 2 || !applied.Domains[0].Verified || applied.Domains[0].AppID != target.ID || applied.Rollback == nil || applied.Rollback.Domains[0].App != source.Slug {
				t.Fatalf("apply=%+v", applied)
			}
			replay := call(t, http.MethodPost, "/v1/domains/apply", request, headers)
			if replay.Code != 200 || replay.Body.String() != r.Body.String() {
				t.Fatalf("replay=%d %s", replay.Code, replay.Body)
			}
			if stale := call(t, http.MethodPost, "/v1/domains/apply", request, nil); stale.Code != 409 {
				t.Fatalf("stale review=%d %s", stale.Code, stale.Body)
			}
		})
	}
}

func TestDomainPlanDoesNotTransferForeignOwnership(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	apidserver.MustSeedApp(t, e, "domain-destination")
	other, err := store.CreateAccount(t.Context(), "foreign-domain-plan@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "foreign-domain-plan", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(t.Context(), "foreign-plan.example.com", foreign.ID, "private"); err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/domains/plan", api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "foreign-plan.example.com", App: "domain-destination"}}}, nil)
	if r.Code != 404 {
		t.Fatalf("foreign domain plan=%d %s", r.Code, r.Body)
	}
}

func TestDomainPlanSharedRolesAreCheckedBeforeReceiptReplay(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "domain-team", "Domain Team", api.PlanPro)
	if r := e.Do(t, "POST", "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "domain-team-api"}, nil); r.Code != 201 {
		t.Fatalf("create app=%d %s", r.Code, r.Body)
	}
	store := *e.Store()
	actor, developer := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	m := api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "team-plan.example.com", App: "domain-team-api"}}}
	if r := e.Do(t, "POST", "/v1/domains/plan", m, viewer); r.Code != 403 {
		t.Fatalf("viewer plan=%d %s", r.Code, r.Body)
	}
	r := e.Do(t, "POST", "/v1/domains/plan", m, developer)
	var p api.DomainPlan
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &p) != nil {
		t.Fatalf("developer plan=%d %s", r.Code, r.Body)
	}
	developer["Idempotency-Key"] = p.SHA256
	request := api.DomainApplyRequest{Manifest: p.Manifest, ExpectedPlanSHA256: p.SHA256, Confirm: true}
	if r := e.Do(t, "POST", "/v1/domains/apply", request, developer); r.Code != 200 {
		t.Fatalf("developer apply=%d %s", r.Code, r.Body)
	}
	if err := store.UpdateOrgMemberRole(t.Context(), org.ID, actor.ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "POST", "/v1/domains/apply", request, developer); r.Code != 403 {
		t.Fatalf("downgraded member replayed a write receipt=%d %s", r.Code, r.Body)
	}
}

func TestDomainPlanFailedPublicationCanRetrySameFingerprint(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	apidserver.MustSeedApp(t, e, "retry-domain-plan")
	other, err := store.CreateAccount(t.Context(), "wildcard-plan-holder@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "plan-wildcard-holder", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(t.Context(), "*.reserved.example.com", foreign.ID, "foreign-proof"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), "*.reserved.example.com"); err != nil {
		t.Fatal(err)
	}
	m := api.DomainManifest{Version: 1, Domains: []api.DomainBindingSpec{{Domain: "api.reserved.example.com", App: "retry-domain-plan"}}}
	r := e.Do(t, "POST", "/v1/domains/plan", m, nil)
	var p api.DomainPlan
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &p) != nil {
		t.Fatalf("plan=%d %s", r.Code, r.Body)
	}
	request := api.DomainApplyRequest{Manifest: p.Manifest, ExpectedPlanSHA256: p.SHA256, Confirm: true}
	headers := map[string]string{"Idempotency-Key": p.SHA256}
	if r := e.Do(t, "POST", "/v1/domains/apply", request, headers); r.Code != 409 {
		t.Fatalf("conflicting publication=%d %s", r.Code, r.Body)
	}
	if err := store.DeleteCustomDomain(t.Context(), "*.reserved.example.com"); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, "POST", "/v1/domains/apply", request, headers); r.Code != 200 {
		t.Fatalf("repaired publication replayed a cached refusal=%d %s", r.Code, r.Body)
	}
}
