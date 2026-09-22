package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListOrgActivityScopesOrdersAndPaginates(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	org := seedActivityPersonalOrg(t, e)
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "payments"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	orgID := uuid.MustParse(org.ID)
	appID := uuid.MustParse(app.ID)
	at := time.Date(2026, 9, 22, 14, 32, 0, 0, time.UTC)
	rows := []state.OrgActivity{
		{OrgID: orgID, OccurredAt: at.Add(-time.Minute), Kind: "domain.added", ActorType: state.OrgActivityActorUser, ActorLabel: "Bahadir", ResourceType: "domain", ResourceLabel: "production.example.com", AppID: &appID, SourceType: "test", SourceID: "1"},
		{OrgID: orgID, OccurredAt: at, Kind: "env.set", ActorType: state.OrgActivityActorUser, ActorLabel: "Poyraz", ResourceType: "environment_variable", ResourceLabel: "DATABASE_URL", AppID: &appID, SourceType: "test", SourceID: "2"},
		{OrgID: orgID, OccurredAt: at.Add(time.Minute), Kind: "app.deployed", ActorType: state.OrgActivityActorGitHub, ActorLabel: "GitHub Actions", ResourceType: "app", ResourceID: app.ID, ResourceLabel: app.Slug, AppID: &appID, SourceType: "test", SourceID: "3"},
		{OrgID: uuid.New(), OccurredAt: at.Add(time.Hour), Kind: "app.deployed", ActorType: state.OrgActivityActorSystem, ActorLabel: "foreign", ResourceType: "app", ResourceLabel: "foreign", SourceType: "test", SourceID: "foreign"},
	}
	for _, row := range rows {
		if _, err := e.store.AppendOrgActivity(ctx, row); err != nil {
			t.Fatalf("AppendOrgActivity: %v", err)
		}
	}

	first := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity?limit=2", nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("page 1: status=%d body=%s", first.Code, first.Body.String())
	}
	var page api.ListOrgActivityResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Summary != "GitHub Actions deployed payments" ||
		page.Items[1].Summary != "Poyraz changed DATABASE_URL" || page.NextBefore == "" {
		t.Fatalf("page 1 = %#v", page)
	}

	second := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity?limit=2&before="+url.QueryEscape(page.NextBefore), nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("page 2: status=%d body=%s", second.Code, second.Body.String())
	}
	page = api.ListOrgActivityResponse{}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Summary != "production.example.com added" || page.NextBefore != "" {
		t.Fatalf("page 2 = %#v", page)
	}
}

func TestListOrgActivityRejectsMalformedCursor(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity?before=not-a-cursor", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestSetEnvActivityNeverContainsValue(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "redaction"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.Code, created.Body.String())
	}
	const secretValue = "postgres://user:super-secret@example/db"
	set := e.do(t, http.MethodPut, "/v1/apps/redaction/env/DATABASE_URL", api.PutAppEnvRequest{Value: secretValue}, nil)
	if set.Code != http.StatusOK {
		t.Fatalf("set env: %d %s", set.Code, set.Body.String())
	}
	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list activity: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretValue) || strings.Contains(rec.Body.String(), "super-secret") {
		t.Fatalf("activity leaked environment value: %s", rec.Body.String())
	}
	var page api.ListOrgActivityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Kind != "env.set" || page.Items[0].Resource.Label != "DATABASE_URL" {
		t.Fatalf("activity = %#v", page.Items)
	}
}

func TestAppActivityUsesOwnerOrgNotCallerOrg(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	personal := seedActivityPersonalOrg(t, e)
	shared, err := e.store.CreateOrg(ctx, state.Org{Slug: "shared-activity", Name: "Shared", Plan: e.acct.Plan, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "owner-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	key := &state.APIKey{OrgID: shared.ID, Label: "shared key"}
	mem := &state.OrgMembership{OrgID: shared.ID, AccountID: e.acct.ID, Role: state.OrgRoleOwner}
	r := httptest.NewRequest(http.MethodPut, "/v1/apps/owner-app/env/KEY", nil)
	r = r.WithContext(authmw.WithPrincipal(r.Context(), e.acct, key, mem))
	e.s.recordAppActivity(ctx, r, e.acct, app, state.OrgActivity{
		Kind: "env.set", ResourceType: "environment_variable", ResourceLabel: "KEY",
		SourceType: "test", SourceID: "owner-only",
	})
	personalRows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(personal.ID), Limit: 10})
	if err != nil || len(personalRows) != 1 {
		t.Fatalf("personal activity = %v, err = %v", personalRows, err)
	}
	sharedRows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(shared.ID), Limit: 10})
	if err != nil || len(sharedRows) != 0 {
		t.Fatalf("shared activity = %v, err = %v", sharedRows, err)
	}
}

func seedActivityPersonalOrg(t *testing.T, e testEnv) state.Org {
	t.Helper()
	ownerID := e.acct.ID
	org, err := e.store.CreateOrg(context.Background(), state.Org{
		Slug: state.PersonalOrgSlug(ownerID), Name: "Personal", Personal: true,
		PersonalOwnerAccountID: &ownerID, Plan: e.acct.Plan, Status: state.OrgStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if err := e.store.AddOrgMember(context.Background(), org.ID, ownerID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember: %v", err)
	}
	return org
}
