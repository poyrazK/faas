package apidserver_test

// adr: 20261021080640 — self-serve previews: customers opt in to a platform
// flag through their own runtime-configuration row.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func selfServe(plans ...string) platformflags.Definition {
	return platformflags.Definition{Enabled: true, SelfServe: &platformflags.SelfServe{Enabled: true, Plans: plans}}
}

func problemCode(t *testing.T, body []byte) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &problem)
	return problem.Code
}

func optInRow(t *testing.T, e apidserver.TestEnv, flagKey string) (platformflags.OptIns, int64) {
	t.Helper()
	row, err := (*e.Store()).GetRuntimeConfig(context.Background(), platformflags.OptInsConfigKey(flagKey), state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		return platformflags.OptIns{}, 0
	}
	optIns, err := platformflags.ParseOptIns(row.DesiredValue)
	if err != nil {
		t.Fatalf("stored opt-ins: %v", err)
	}
	return optIns, row.Version
}

func TestSelfServePreviewJoinAndLeave(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).SeedPlatformFlag(platformflags.ChangeTimeline, selfServe("pro", "scale"))
	acct := (*e.Acct()).ID

	list := e.Do(t, http.MethodGet, "/v1/previews", nil, nil)
	previews := decodeBody[api.PreviewList](t, list.Code, list.Body.Bytes(), http.StatusOK)
	if len(previews.Items) != 1 || previews.Items[0].Key != platformflags.ChangeTimeline || !previews.Items[0].Eligible || previews.Items[0].Joined || previews.Items[0].Enabled {
		t.Fatalf("previews = %+v, want change_timeline open and not joined", previews.Items)
	}

	joined := e.Do(t, http.MethodPut, "/v1/previews/"+platformflags.ChangeTimeline, nil, nil)
	if item := decodeBody[api.Preview](t, joined.Code, joined.Body.Bytes(), http.StatusOK); !item.Joined || !item.Enabled {
		t.Fatalf("after join = %+v, want joined and on", item)
	}
	if optIns, version := optInRow(t, e, platformflags.ChangeTimeline); version != 1 || !optIns.HasAccount(acct) {
		t.Fatalf("opt-in row = %+v v%d, want the account at version 1", optIns, version)
	}
	if again := e.Do(t, http.MethodPut, "/v1/previews/"+platformflags.ChangeTimeline, nil, nil); again.Code != http.StatusOK {
		t.Fatalf("second join = %d", again.Code)
	}
	if _, version := optInRow(t, e, platformflags.ChangeTimeline); version != 1 {
		t.Fatalf("joining twice wrote version %d, want no write", version)
	}

	closed := e.Do(t, http.MethodPut, "/v1/previews/"+platformflags.ServiceMap, nil, nil)
	if closed.Code != http.StatusNotFound || problemCode(t, closed.Body.Bytes()) != api.CodePreviewNotOffered {
		t.Fatalf("join a closed preview = %d %s, want 404 preview_not_offered", closed.Code, closed.Body)
	}

	left := e.Do(t, http.MethodDelete, "/v1/previews/"+platformflags.ChangeTimeline, nil, nil)
	if item := decodeBody[api.Preview](t, left.Code, left.Body.Bytes(), http.StatusOK); item.Joined || item.Enabled {
		t.Fatalf("after leave = %+v, want left and off", item)
	}
	if optIns, version := optInRow(t, e, platformflags.ChangeTimeline); version != 2 || optIns.Count() != 0 {
		t.Fatalf("opt-in row after leave = %+v v%d", optIns, version)
	}
}

func TestSelfServePreviewRefusesIneligiblePlansAndFullPreviews(t *testing.T) {
	free := apidserver.Setup(t, api.PlanFree)
	(*free.S()).SeedPlatformFlag(platformflags.ChangeTimeline, selfServe("pro", "scale"))
	refused := free.Do(t, http.MethodPut, "/v1/previews/"+platformflags.ChangeTimeline, nil, nil)
	if refused.Code != http.StatusForbidden || problemCode(t, refused.Body.Bytes()) != api.CodePreviewPlanNotEligible {
		t.Fatalf("free join = %d %s, want 403 preview_plan_not_eligible", refused.Code, refused.Body)
	}

	e := apidserver.Setup(t, api.PlanPro)
	full := selfServe()
	full.SelfServe.MaxSubjects = 1
	(*e.S()).SeedPlatformFlag(platformflags.ChangeTimeline, full)
	other := platformflags.OptIns{}.With(uuid.NewString(), false)
	expected := int64(0)
	if _, err := (*e.Store()).UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: platformflags.OptInsConfigKey(platformflags.ChangeTimeline), Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: other.Marshal(), ApplyMode: state.RuntimeConfigApplyHot, Reason: "seed", ExpectedVersion: &expected,
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPut, "/v1/previews/"+platformflags.ChangeTimeline, nil, nil)
	if rec.Code != http.StatusConflict || problemCode(t, rec.Body.Bytes()) != api.CodePreviewFull {
		t.Fatalf("join a full preview = %d %s, want 409 preview_full", rec.Code, rec.Body)
	}
}

func TestOrganizationPreviewsNeedOwnerOrAdminAndApplyUnderTheCompany(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.S()
	s.SeedPlatformFlag(platformflags.ApplicationAuth, selfServe())
	s.SeedPlatformFlag(platformflags.LiveQueries, selfServe())
	org := apidserver.SeedSharedOrgWithOwner(t, e, "acme", "Acme", api.PlanPro)

	joined := e.Do(t, http.MethodPut, "/v1/orgs/acme/previews/"+platformflags.ApplicationAuth, nil, nil)
	if item := decodeBody[api.Preview](t, joined.Code, joined.Body.Bytes(), http.StatusOK); !item.Joined || !item.Enabled {
		t.Fatalf("org join = %+v", item)
	}
	member := uuid.NewString()
	if !s.PlatformFlagSet().Enabled(platformflags.ApplicationAuth, platformflags.Subject{AccountID: member, Plan: "pro", OrgID: org.ID}) {
		t.Fatal("a member acting for the company must get the preview")
	}
	if s.PlatformFlagSet().Enabled(platformflags.ApplicationAuth, platformflags.Subject{AccountID: member, Plan: "pro"}) {
		t.Fatal("the same member outside the company must not")
	}

	unsupported := e.Do(t, http.MethodPut, "/v1/orgs/acme/previews/"+platformflags.LiveQueries, nil, nil)
	if unsupported.Code != http.StatusConflict || problemCode(t, unsupported.Body.Bytes()) != api.CodePreviewOrgUnsupported {
		t.Fatalf("org join of an account-only preview = %d %s, want 409 preview_org_unsupported", unsupported.Code, unsupported.Body)
	}
	list := e.Do(t, http.MethodGet, "/v1/orgs/acme/previews", nil, nil)
	previews := decodeBody[api.PreviewList](t, list.Code, list.Body.Bytes(), http.StatusOK)
	keys := []string{}
	for _, item := range previews.Items {
		keys = append(keys, item.Key)
	}
	if !slices.Equal(keys, []string{platformflags.ApplicationAuth}) {
		t.Fatalf("org previews = %v, want only application_auth", keys)
	}

	ctx := context.Background()
	beta, err := (*e.Store()).CreateOrg(ctx, state.Org{Slug: "beta", Name: "Beta", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).AddOrgMember(ctx, beta.ID, (*e.Acct()).ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if denied := e.Do(t, http.MethodPut, "/v1/orgs/beta/previews/"+platformflags.ApplicationAuth, nil, nil); denied.Code != http.StatusForbidden {
		t.Fatalf("developer join = %d %s, want 403", denied.Code, denied.Body)
	}
	if view := e.Do(t, http.MethodGet, "/v1/orgs/beta/previews", nil, nil); view.Code != http.StatusOK {
		t.Fatalf("developer list = %d %s, want 200", view.Code, view.Body)
	}
}

func TestRealtimePreviewDecisionsWaitForAnOperatorWrite(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	acct := *e.Acct()
	app, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	s := *e.S()
	if previews, err := s.RealtimeEndpointPreviews(ctx, acct.ID, app.ID); err != nil || previews != nil {
		t.Fatalf("before any operator write = %+v, %v; want none so older realtime nodes accept the registration", previews, err)
	}
	denied := platformflags.Definition{Enabled: true, Default: true, Rules: []platformflags.Rule{
		platformFlagRule("denied", false, func(r *platformflags.Rule) { r.Accounts = []string{uuid.MustParse(acct.ID).String()} }),
	}}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.RealtimeTickets), denied, 0); code != http.StatusOK {
		t.Fatalf("write realtime_tickets = %d", code)
	}
	previews, err := s.RealtimeEndpointPreviews(ctx, acct.ID, app.ID)
	if err != nil || previews == nil {
		t.Fatalf("after a write = %+v, %v", previews, err)
	}
	if previews.Tickets || !previews.Resume || !previews.Access || !previews.Occupancy {
		t.Fatalf("previews = %+v, want tickets denied and the unwritten flags deferring to the node", previews)
	}
}

func TestOperatorOptInAndSelfServeWritesAreChecked(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	orgOptIn := platformflags.OptIns{Orgs: []string{uuid.NewString()}}
	if code := patchPlatformConfig(t, e, platformflags.OptInsConfigKey(platformflags.AIGateway), orgOptIn, 0); code != http.StatusConflict {
		t.Fatalf("org opt-in on ai_gateway = %d, want 409: outboundd cannot see the organization", code)
	}
	freeGateway := platformflags.Definition{Enabled: true, SelfServe: &platformflags.SelfServe{Enabled: true, Plans: []string{"free"}}}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.AIGateway), freeGateway, 0); code != http.StatusConflict {
		t.Fatalf("self-serve ai_gateway for free = %d, want 409: Free is not entitled to the AI gateway", code)
	}
	if code := patchPlatformConfig(t, e, platformflags.OptInsConfigKey(platformflags.ApplicationAuth), orgOptIn, 0); code != http.StatusOK {
		t.Fatalf("org opt-in on application_auth = %d, want 200", code)
	}
	list := e.Do(t, http.MethodGet, "/v1/admin/flags/"+platformflags.ApplicationAuth, nil, nil)
	if item := decodeBody[api.OperatorPlatformFlag](t, list.Code, list.Body.Bytes(), http.StatusOK); item.OptIns.Orgs != 1 || item.OptIns.Version != 1 {
		t.Fatalf("operator opt-in counts = %+v", item.OptIns)
	}
}
