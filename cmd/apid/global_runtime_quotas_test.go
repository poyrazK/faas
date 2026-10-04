package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGlobalRuntimeQuotaHTTPAndSDK(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) {
			d := globalDirectoryHarness(t)
			caller := d
			if shared {
				org := seedSharedOrgWithOwner(t, d, "quota-team", "Quota Team", api.PlanPro)
				caller = directoryOrgKey(t, d, org, d.acct, api.ScopesAdminOnly)
			}
			srv := httptest.NewServer(d.h)
			defer srv.Close()
			client := api.NewClient(srv.URL, caller.key).SetCompletionCache(nil)
			app, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "quota-global"})
			if err != nil {
				t.Fatal(err)
			}
			for _, region := range []string{"eu-fsn", "eu-hel"} {
				cell := registerDirectoryCell(t, d, globaldirectory.Cell{ID: uuid.NewString(), Region: region, APIBase: "https://" + region + ".example.com", Enabled: true})
				binding := globaldirectory.Binding{CellID: cell.ID, Region: region, CellGeneration: cell.Generation, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "quota-regional"}
				if shared {
					binding.RegionalOrgID = uuid.NewString()
					_, err = d.s.globalDirectory.(globaldirectory.OrgTargetStore).AttachOrgTarget(t.Context(), app.Owner.ID, d.acct.ID, app.Slug, binding)
				} else {
					_, err = d.s.globalDirectory.AttachTarget(t.Context(), d.acct.ID, app.Slug, binding)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			id := uuid.NewString()
			req := api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-hel", Concurrency: 2}, {Region: "eu-fsn", Concurrency: 2}}}
			first, err := client.PutGlobalRuntimeQuota(t.Context(), app.Slug, id, req)
			if err != nil || first.Generation != 1 || first.Status != "reserved" || first.Policy.Plan != api.PlanPro || len(first.Targets) != 2 || first.Targets[0].Region != "eu-fsn" || first.Targets[0].RolloutConcurrency != api.RolloutConcurrencyGrant || first.RetirementID != nil {
				t.Fatalf("reservation: %+v %v", first, err)
			}
			quota := d.s.globalDirectory.(globaldirectory.RuntimeQuotaStore)
			ownerID := app.Owner.ID
			if !shared {
				ownerID = d.acct.ID
			}
			issued, err := quota.IssueRuntimeQuotaTarget(t.Context(), ownerID, app.Slug, id, "eu-fsn")
			if err != nil {
				t.Fatal(err)
			}
			read, err := client.GetGlobalRuntimeQuota(t.Context(), app.Slug, id)
			if err != nil || read.Targets[0].IssuedAt == nil || read.Targets[0].ReleasedAt != nil {
				t.Fatalf("issued state: %+v %v", read, err)
			}
			retired, err := client.RetireGlobalRuntimeQuota(t.Context(), app.Slug, id)
			if err != nil || retired.Status != "retiring" || retired.RetirementID == nil || retired.Targets[0].ReleasedAt != nil || retired.Targets[1].ReleasedAt == nil {
				t.Fatalf("issued charge lost: %+v %v", retired, err)
			}
			if retry, err := client.PutGlobalRuntimeQuota(t.Context(), app.Slug, id, req); err != nil || retry.Status != "retiring" || retry.Generation != first.Generation {
				t.Fatalf("tombstone revived: %+v %v", retry, err)
			}
			full := api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 2}, {Region: "eu-hel", Concurrency: 3}}}
			path := "/v1/global-apps/" + app.Slug + "/runtime-quotas/" + uuid.NewString()
			r := caller.do(t, http.MethodPut, path, full, nil)
			var problem api.Problem
			if err := json.Unmarshal(r.Body.Bytes(), &problem); err != nil || r.Code != http.StatusConflict || problem.Code != api.CodeGlobalRuntimeQuotaLimit || problem.Limit == nil || *problem.Limit != 5 || problem.Observed == nil || *problem.Observed != 7 || problem.DocsURL != "https://gregale.dev/docs/global-app-directory#runtime-quota-reservations" {
				t.Fatalf("quota problem: %d %s %v", r.Code, r.Body, err)
			}
			// No customer route accepts the internal trusted drain assertion.
			ack := globaldirectory.RuntimeQuotaRetirement{AllocationID: id, RetirementID: *retired.RetirementID, Generation: first.Generation, Binding: issued.Binding, APIBase: issued.APIBase, AdmissionFrozen: true}
			if r := caller.do(t, http.MethodPost, "/v1/global-apps/"+app.Slug+"/runtime-quotas/"+id+"/drain", ack, nil); r.Code != http.StatusNotFound {
				t.Fatalf("customer drain assertion accepted: %d", r.Code)
			}
			if err := quota.ConfirmRuntimeQuotaRetirement(t.Context(), ownerID, app.Slug, ack); err != nil {
				t.Fatal(err)
			}
			if final, err := client.GetGlobalRuntimeQuota(t.Context(), app.Slug, id); err != nil || final.Status != "retired" || final.Targets[0].ReleasedAt == nil {
				t.Fatalf("confirmed drain state: %+v %v", final, err)
			}
			if shared {
				if r := d.do(t, http.MethodGet, "/v1/global-apps/"+app.Slug+"/runtime-quotas/"+id, nil, nil); r.Code != http.StatusNotFound {
					t.Fatalf("personal principal read shared quota: %d", r.Code)
				}
			}
		})
	}
}

func TestGlobalRuntimeQuotaHTTPValidationAndConsent(t *testing.T) {
	d := globalDirectoryHarness(t)
	org := seedSharedOrgWithOwner(t, d, "quota-consent", "Quota Consent", api.PlanPro)
	owner := directoryOrgKey(t, d, org, d.acct, api.ScopesAdminOnly)
	if r := owner.do(t, http.MethodPost, "/v1/global-apps", api.CreateGlobalAppRequest{Slug: "quota-consent"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	developer, err := d.store.CreateAccount(t.Context(), "quota-developer@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.store.MarkAccountEmailVerified(t.Context(), developer.ID); err != nil {
		t.Fatal(err)
	}
	if err = d.store.AddOrgMember(t.Context(), org.ID, developer.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	member := directoryOrgKey(t, d, org, developer, api.ScopesAdminOnly)
	path := "/v1/global-apps/quota-consent/runtime-quotas/" + uuid.NewString()
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		assertProblem(t, member.do(t, method, path, map[string]any{}, nil), http.StatusForbidden, api.CodeOrgRoleForbidden)
	}
	for _, body := range []any{
		map[string]any{"shares": []any{}, "admission_frozen": true},
		api.PutGlobalRuntimeQuotaRequest{},
		api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 0}}},
		api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 1}, {Region: "eu-fsn", Concurrency: 1}}},
		api.PutGlobalRuntimeQuotaRequest{Shares: []api.GlobalRuntimeQuotaShare{{Region: "eu-fsn", Concurrency: 1}}, RolloutRegion: "eu-hel"},
	} {
		assertProblem(t, owner.do(t, http.MethodPut, path, body, nil), http.StatusBadRequest, api.CodeValidation)
	}
	for _, method := range []string{http.MethodPut, http.MethodGet, http.MethodDelete} {
		assertProblem(t, owner.do(t, method, "/v1/global-apps/quota-consent/runtime-quotas/"+uuid.Nil.String(), nil, nil), http.StatusBadRequest, api.CodeValidation)
	}
}
