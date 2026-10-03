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

func TestOrgActivitySummaryDeploymentLifecycle(t *testing.T) {
	for _, tc := range []struct {
		kind string
		want string
		data string
	}{
		{kind: "app.created", want: "Bahadir created app payments"},
		{kind: "app.deleted", want: "Bahadir deleted app payments"},
		{kind: "app.restored", want: "Bahadir restored app payments"},
		{kind: "app.config_updated", want: "Bahadir updated payments settings (ram_mb, egress_allowlist)", data: `{"changes":[{"field":"ram_mb","old":256,"new":512},{"field":"egress_allowlist","values_redacted":true}]}`},
		{kind: "deploy.requested", want: "Bahadir requested a deployment of payments"},
		{kind: "app.deployed", want: "Bahadir deployed payments"},
		{kind: "deploy.failed", want: "Deployment of payments failed (requested by Bahadir)"},
		{kind: "deploy.cancelled", want: "Bahadir cancelled deployment payments"},
		{kind: "api_key.created", want: "Bahadir created API key ci-deploy"},
		{kind: "api_key.rotated", want: "Bahadir rotated API key ci-deploy"},
		{kind: "api_key.revoked", want: "Bahadir revoked API key ci-deploy"},
		{kind: "org.invitation.created", want: "Bahadir invited invitee@example.com (developer)", data: `{"role":"developer"}`},
		{kind: "org.invitation.accepted", want: "Bahadir accepted an invitation for invitee@example.com (developer)", data: `{"role":"developer"}`},
		{kind: "org.invitation.revoked", want: "Bahadir revoked the invitation for invitee@example.com"},
		{kind: "org.member.added", want: "invitee@example.com joined the workspace (developer)", data: `{"role":"developer"}`},
		{kind: "org.member.role_changed", want: "Bahadir changed invitee@example.com's role (admin)", data: `{"new_role":"admin"}`},
		{kind: "org.member.removed", want: "Bahadir removed invitee@example.com (admin)", data: `{"role":"admin"}`},
		{kind: "org.ownership_transferred", want: "Bahadir transferred ownership to invitee@example.com"},
	} {
		resourceLabel := "payments"
		if strings.HasPrefix(tc.kind, "api_key.") {
			resourceLabel = "ci-deploy"
		} else if strings.HasPrefix(tc.kind, "org.") {
			resourceLabel = "invitee@example.com"
		}
		if got := orgActivitySummary(state.OrgActivity{Kind: tc.kind, ActorLabel: "Bahadir", ResourceLabel: resourceLabel, Data: []byte(tc.data)}); got != tc.want {
			t.Errorf("orgActivitySummary(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestOrgInvitationCreationAppearsInOrgActivity(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "timeline-access", "Activity Access", api.PlanPro)
	const invitee = "timeline-invitee@example.com"
	rec := e.do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/members", api.InviteMemberRequest{
		Email: invitee, Role: string(state.OrgRoleDeveloper),
	}, map[string]string{"X-Active-Org": org.Slug})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create invitation: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows := assertOrgActivityKinds(t, e.store, org.ID, "org.invitation.created")
	if len(rows) != 1 || rows[0].ResourceLabel != invitee || rows[0].ActorType != state.OrgActivityActorAPIKey ||
		rows[0].ActorLabel != "test" || orgActivitySummary(rows[0]) != "test invited "+invitee+" (developer)" {
		t.Fatalf("invitation activity = %#v", rows)
	}
	if strings.Contains(string(rows[0].Data), "token") || strings.Contains(rows[0].ResourceLabel, "token") {
		t.Fatalf("invitation activity contains token material: %+v", rows[0])
	}
}

func assertOrgActivityKinds(t *testing.T, store *state.MemStore, orgID string, kinds ...string) []state.OrgActivity {
	t.Helper()
	parsedOrgID, err := uuid.Parse(orgID)
	if err != nil {
		t.Fatalf("parse org id: %v", err)
	}
	rows, err := store.ListOrgActivity(context.Background(), state.OrgActivityFilter{OrgID: parsedOrgID, Limit: 100})
	if err != nil {
		t.Fatalf("ListOrgActivity: %v", err)
	}
	counts := make(map[string]int, len(kinds))
	for _, kind := range kinds {
		counts[kind]++
	}
	for _, row := range rows {
		if counts[row.Kind] > 0 {
			counts[row.Kind]--
		}
	}
	for kind, missing := range counts {
		if missing > 0 {
			t.Errorf("timeline is missing %d %q event(s); rows=%+v", missing, kind, rows)
		}
	}
	return rows
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
	if len(page.Items) != 2 || page.Items[0].Kind != "env.set" || page.Items[0].Resource.Label != "DATABASE_URL" || page.Items[1].Kind != "app.created" {
		t.Fatalf("activity = %#v", page.Items)
	}
}

func TestOrgAPIKeyMutationsProjectSafeTimelineActivity(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	ctx := context.Background()

	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	request := httptest.NewRequest(http.MethodPost, "/v1/orgs/"+org.Slug+"/keys", nil)
	createdEntry := newOrgAPIKeyActivity(request, e.acct, org.ID, "api_key.created", map[string]any{
		"scopes": []string{api.ScopeDeployWrite}, "expires_at": expiresAt.Format(time.RFC3339),
	})
	created, err := e.s.createOrgAPIKeyWithActivity(ctx, e.acct, org.ID, hash, "ci-deploy", []string{api.ScopeDeployWrite}, &expiresAt, "", "", nil, createdEntry)
	if err != nil {
		t.Fatalf("create org API key with activity: %v", err)
	}

	rotatedPlaintext, rotatedHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey for rotation: %v", err)
	}
	rotatedEntry := newOrgAPIKeyActivity(request, e.acct, org.ID, "api_key.rotated", map[string]any{
		"old_key_id": created.ID, "grace_window_days": 1,
	})
	rotated, _, err := e.s.rotateOrgAPIKeyWithActivity(ctx, org.ID, created.ID, rotatedHash, "ci-deploy-rotated", 24*time.Hour, "", "", nil, rotatedEntry)
	if err != nil {
		t.Fatalf("rotate org API key with activity: %v", err)
	}

	revokedEntry := newOrgAPIKeyActivity(request, e.acct, org.ID, "api_key.revoked", map[string]any{"reason": "manual"})
	if _, err := e.s.revokeOrgAPIKeyWithActivity(ctx, org.ID, rotated.ID, revokedEntry); err != nil {
		t.Fatalf("revoke org API key with activity: %v", err)
	}

	rows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("API-key activity rows = (%#v, %v), want three", rows, err)
	}
	want := map[string]string{
		"api_key.created": e.acct.Email + " created API key ci-deploy",
		"api_key.rotated": e.acct.Email + " rotated API key ci-deploy-rotated",
		"api_key.revoked": e.acct.Email + " revoked API key ci-deploy-rotated",
	}
	for _, row := range rows {
		if got := orgActivitySummary(row); got != want[row.Kind] {
			t.Errorf("summary for %s = %q, want %q", row.Kind, got, want[row.Kind])
		}
		if strings.Contains(string(row.Data), plaintext) || strings.Contains(string(row.Data), rotatedPlaintext) ||
			strings.Contains(row.ResourceLabel, plaintext) || strings.Contains(row.ResourceLabel, rotatedPlaintext) {
			t.Errorf("API-key activity contains plaintext credential: %+v", row)
		}
	}
}

func TestAppLifecycleAppearsInOrgActivity(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "lifecycle-history"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.Code, created.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	deleted := e.do(t, http.MethodDelete, "/v1/apps/"+app.Slug, nil, nil)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete app: %d %s", deleted.Code, deleted.Body.String())
	}
	restored := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/restore", nil, nil)
	if restored.Code != http.StatusOK {
		t.Fatalf("restore app: %d %s", restored.Code, restored.Body.String())
	}

	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list activity: %d %s", rec.Code, rec.Body.String())
	}
	var page api.ListOrgActivityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("activity = %#v, want create/delete/restore", page.Items)
	}
	for i, wantKind := range []string{"app.restored", "app.deleted", "app.created"} {
		if got := page.Items[i]; got.Kind != wantKind || got.Resource.Label != app.Slug || got.AppID != uuidStringOf(app.ID) {
			t.Errorf("activity[%d] = %#v, want %s for app %s", i, got, wantKind, app.Slug)
		}
	}
}

func TestAppConfigUpdateAppearsInOrgActivityWithoutLeakingPolicyValues(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	created := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "config-history"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", created.Code, created.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}

	ram := 256
	if app.RAMMB == ram {
		ram = 384
	}
	egress := []string{"203.0.113.0/24"}
	update := api.UpdateAppRequest{RAMMB: &ram, EgressAllowlist: &egress}
	updated := e.do(t, http.MethodPatch, "/v1/apps/"+app.Slug, update, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update app: %d %s", updated.Code, updated.Body.String())
	}
	rows, err := e.store.ListOrgActivity(context.Background(), state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("activity after update = (%#v, %v), want create plus config update", rows, err)
	}
	entry := rows[0]
	if entry.Kind != "app.config_updated" || entry.ResourceLabel != app.Slug || entry.ActorLabel != "test" ||
		orgActivitySummary(entry) != "test updated config-history settings (ram_mb, egress_allowlist)" {
		t.Fatalf("config activity = %+v, summary=%q", entry, orgActivitySummary(entry))
	}
	if strings.Contains(string(entry.Data), egress[0]) {
		t.Fatalf("activity leaked egress CIDR: %s", entry.Data)
	}
	var payload struct {
		Changes []struct {
			Field          string `json:"field"`
			Old            any    `json:"old"`
			New            any    `json:"new"`
			ValuesRedacted bool   `json:"values_redacted"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(entry.Data, &payload); err != nil {
		t.Fatalf("decode config activity data: %v", err)
	}
	if len(payload.Changes) != 2 || payload.Changes[0].Field != "ram_mb" || payload.Changes[0].New != float64(ram) ||
		payload.Changes[1].Field != "egress_allowlist" || !payload.Changes[1].ValuesRedacted ||
		payload.Changes[1].Old != nil || payload.Changes[1].New != nil {
		t.Fatalf("config activity changes = %#v", payload.Changes)
	}

	noOp := e.do(t, http.MethodPatch, "/v1/apps/"+app.Slug, update, nil)
	if noOp.Code != http.StatusOK {
		t.Fatalf("repeat unchanged update: %d %s", noOp.Code, noOp.Body.String())
	}
	rows, err = e.store.ListOrgActivity(context.Background(), state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("activity after no-op update = (%#v, %v), want no duplicate event", rows, err)
	}
}

func TestDomainLifecycleAppearsInOrgActivity(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedActivityPersonalOrg(t, e)
	createdApp := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "domain-history"}, nil)
	if createdApp.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", createdApp.Code, createdApp.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(createdApp.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	const domain = "history.example.com"
	createdDomain := e.do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{AppID: app.ID, Domain: domain}, nil)
	if createdDomain.Code != http.StatusAccepted {
		t.Fatalf("create domain: %d %s", createdDomain.Code, createdDomain.Body.String())
	}
	deletedDomain := e.do(t, http.MethodDelete, "/v1/domains/"+domain, nil, nil)
	if deletedDomain.Code != http.StatusNoContent {
		t.Fatalf("delete domain: %d %s", deletedDomain.Code, deletedDomain.Body.String())
	}

	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/activity", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list activity: %d %s", rec.Code, rec.Body.String())
	}
	var page api.ListOrgActivityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("activity = %#v, want app create plus domain add and remove", page.Items)
	}
	if got := page.Items[0]; got.Kind != "domain.removed" || got.Summary != domain+" removed" || got.Resource.ID != domain || got.AppID != uuidStringOf(app.ID) {
		t.Errorf("remove activity = %#v", got)
	}
	if got := page.Items[1]; got.Kind != "domain.added" || got.Summary != domain+" added" || got.Resource.ID != domain || got.AppID != uuidStringOf(app.ID) {
		t.Errorf("add activity = %#v", got)
	}
	if got := page.Items[2]; got.Kind != "app.created" || got.Resource.Label != app.Slug || got.AppID != uuidStringOf(app.ID) {
		t.Errorf("app creation activity = %#v", got)
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
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, OrgID: shared.ID, Slug: "owner-app"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	key := &state.APIKey{OrgID: personal.ID, Label: "personal key"}
	mem := &state.OrgMembership{OrgID: personal.ID, AccountID: e.acct.ID, Role: state.OrgRoleOwner}
	r := httptest.NewRequest(http.MethodPut, "/v1/apps/owner-app/env/KEY", nil)
	r = r.WithContext(authmw.WithPrincipal(r.Context(), e.acct, key, mem))
	e.s.recordAppActivity(ctx, r, e.acct, app, state.OrgActivity{
		Kind: "env.set", ResourceType: "environment_variable", ResourceLabel: "KEY",
		SourceType: "test", SourceID: "owner-only",
	})
	sharedRows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(shared.ID), Limit: 10})
	if err != nil || len(sharedRows) != 1 {
		t.Fatalf("shared activity = %v, err = %v", sharedRows, err)
	}

	// Rows written before org_id attribution remain visible via the legacy
	// account-to-personal-org fallback.
	app.OrgID = ""
	e.s.recordAppActivity(ctx, r, e.acct, app, state.OrgActivity{
		Kind: "env.set", ResourceType: "environment_variable", ResourceLabel: "LEGACY_KEY",
		SourceType: "test", SourceID: "legacy-fallback",
	})
	personalRows, err := e.store.ListOrgActivity(ctx, state.OrgActivityFilter{OrgID: uuid.MustParse(personal.ID), Limit: 10})
	if err != nil || len(personalRows) != 1 {
		t.Fatalf("legacy personal activity = %v, err = %v", personalRows, err)
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
