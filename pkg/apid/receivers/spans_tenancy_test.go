package receivers

// Tracing tenancy (2026-10-31 logs/traces audit): T4 producers name the app,
// T5 OTLP keys are scoped and resolved to their tenant, T6 plans come from
// the app's resource owner, and the tracing capability gates ingest.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// spansOwnerFake answers the account, app, organization and key reads the
// tracing receivers resolve against, and records span writes.
type spansOwnerFake struct {
	mu       sync.Mutex
	accounts map[string]state.Account
	apps     map[string]state.App
	orgs     map[string]state.Org
	roles    map[[2]string]state.OrgRole
	reads    int

	holder state.Account
	key    state.APIKey

	writes  []state.SpansSummaryTarget
	maxSpan []int
}

func (f *spansOwnerFake) AccountByID(_ context.Context, id string) (state.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if acct, ok := f.accounts[id]; ok {
		return acct, nil
	}
	return state.Account{}, state.ErrNotFound
}

func (f *spansOwnerFake) AppByID(_ context.Context, id string) (state.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if app, ok := f.apps[id]; ok {
		return app, nil
	}
	return state.App{}, state.ErrNotFound
}

func (f *spansOwnerFake) OrgByID(_ context.Context, id string) (state.Org, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if org, ok := f.orgs[id]; ok {
		return org, nil
	}
	return state.Org{}, state.ErrNotFound
}

func (f *spansOwnerFake) OrgAccessSnapshot(_ context.Context, orgID, accountID string) (state.OrgAccessSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if role, ok := f.roles[[2]string{orgID, accountID}]; ok {
		return state.OrgAccessSnapshot{Role: role, Revision: 1}, nil
	}
	return state.OrgAccessSnapshot{}, state.ErrNotFound
}

func (f *spansOwnerFake) CheckOrgAccess(context.Context) error { return nil }

func (f *spansOwnerFake) AuthenticateKey(context.Context, []byte) (state.Account, state.APIKey, error) {
	return f.holder, f.key, nil
}

func (f *spansOwnerFake) AuthenticateOIDCBearer(context.Context, []byte) (state.Account, state.APIKey, error) {
	return state.Account{}, state.APIKey{}, state.ErrNotFound
}

func (f *spansOwnerFake) UpdateSpansSummary(_ context.Context, target state.SpansSummaryTarget, _ []byte, maxSpans int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, target)
	f.maxSpan = append(f.maxSpan, maxSpans)
	return nil
}

func (f *spansOwnerFake) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// spansTenancyWorld is a Free creator who made a personal app and a company
// app; the company is on Scale and owns its resources, and a teammate works
// there as a developer, a viewer as a viewer.
type spansTenancyWorld struct {
	store                     *spansOwnerFake
	creator, teammate, viewer state.Account
	company, personalOrg      state.Org
	personalApp, companyApp   state.App
}

func newSpansTenancyWorld() spansTenancyWorld {
	creator := state.Account{ID: uuid.NewString(), Plan: api.PlanFree}
	teammate := state.Account{ID: uuid.NewString(), Plan: api.PlanHobby}
	viewer := state.Account{ID: uuid.NewString(), Plan: api.PlanPro}
	personalOrg := state.Org{ID: uuid.NewString(), Personal: true, PersonalOwnerAccountID: &creator.ID, Plan: api.PlanFree}
	company := state.Org{ID: uuid.NewString(), Plan: api.PlanScale, Status: state.OrgStatusActive, ResourceOwnershipEnabled: true}
	w := spansTenancyWorld{
		creator: creator, teammate: teammate, viewer: viewer, company: company, personalOrg: personalOrg,
		personalApp: state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: personalOrg.ID},
		companyApp:  state.App{ID: uuid.NewString(), AccountID: creator.ID, OrgID: company.ID},
	}
	w.store = &spansOwnerFake{
		accounts: map[string]state.Account{creator.ID: creator, teammate.ID: teammate, viewer.ID: viewer},
		apps:     map[string]state.App{w.personalApp.ID: w.personalApp, w.companyApp.ID: w.companyApp},
		orgs:     map[string]state.Org{company.ID: company, personalOrg.ID: personalOrg},
		roles: map[[2]string]state.OrgRole{
			{company.ID, creator.ID}:  state.OrgRoleDeveloper,
			{company.ID, teammate.ID}: state.OrgRoleDeveloper,
			{company.ID, viewer.ID}:   state.OrgRoleViewer,
		},
	}
	return w
}

func TestSpansTelemetryPlanUsesTheAppsResourceOwner(t *testing.T) {
	w := newSpansTenancyWorld()
	limiter := peraccount.NewLimiter()
	plans := newTelemetryPlans(w.store, limiter)
	ctx := context.Background()
	creator := uuid.MustParse(w.creator.ID)

	company, err := plans.forApp(ctx, creator, uuid.MustParse(w.companyApp.ID))
	if err != nil {
		t.Fatal(err)
	}
	// The company app runs on the company's Scale plan and draws from the
	// company's bucket, not its Free creator's.
	if company.Limits.Plan != api.PlanScale || company.Subject != uuid.MustParse(w.company.ID) || company.Flag.OrgID != w.company.ID {
		t.Fatalf("company app plan = %+v, want the company's Scale plan and bucket", company)
	}
	personal, err := plans.forApp(ctx, creator, uuid.MustParse(w.personalApp.ID))
	if err != nil {
		t.Fatal(err)
	}
	if personal.Limits.Plan != api.PlanFree || personal.Subject != creator {
		t.Fatalf("personal app plan = %+v, want the creator's Free plan and bucket", personal)
	}
	if cached, ok := limiter.CachedLimits(uuid.MustParse(w.company.ID)); !ok || cached.Plan != api.PlanScale {
		t.Fatalf("limiter capacity for the company = %+v %v, want Scale", cached, ok)
	}
	reads := w.store.readCount()
	if _, err := plans.forApp(ctx, creator, uuid.MustParse(w.companyApp.ID)); err != nil {
		t.Fatal(err)
	}
	if w.store.readCount() != reads {
		t.Fatal("steady-state resolution read the store again")
	}
	if _, err := plans.forApp(ctx, creator, uuid.New()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown app: %v, want ErrNotFound", err)
	}
}

func TestSpansRequestTelemetryAdmitsCompanyAppOfFreeCreator(t *testing.T) {
	w := newSpansTenancyWorld()
	store := &spansRequestTelemetryStore{owners: w.store, rows: &consumerTelemetryStore{}}
	r := NewRequestTelemetryReceiver(store, nil, nil, true)
	row := func(app state.App) *apidpb.IncrementRequestTelemetryRequest {
		return &apidpb.IncrementRequestTelemetryRequest{
			AccountId: app.AccountID, AppId: app.ID, DeploymentId: uuid.NewString(),
			RouteTemplate: "GET /orders", Method: "GET", HttpStatus: 200, LatencyMs: 5, ReceivedAtUnixMs: 1,
		}
	}
	// Retention keeps a company app's rows for the company's plan; admission
	// now agrees, so the Free creator's plan no longer drops them.
	if out := r.HandleOne(context.Background(), row(w.companyApp)); out.GetOutcome() != RtOutcomeInserted {
		t.Fatalf("company app row outcome = %q, want inserted", out.GetOutcome())
	}
	if out := r.HandleOne(context.Background(), row(w.personalApp)); out.GetOutcome() != rtOutcomeRateLimited {
		t.Fatalf("Free personal app row outcome = %q, want rate_limited", out.GetOutcome())
	}
}

// spansRequestTelemetryStore is consumerTelemetryStore with real owners.
type spansRequestTelemetryStore struct {
	owners *spansOwnerFake
	rows   *consumerTelemetryStore
}

func (s *spansRequestTelemetryStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
	return s.owners.AccountByID(ctx, id)
}

func (s *spansRequestTelemetryStore) AppByID(ctx context.Context, id string) (state.App, error) {
	return s.owners.AppByID(ctx, id)
}

func (s *spansRequestTelemetryStore) OrgByID(ctx context.Context, id string) (state.Org, error) {
	return s.owners.OrgByID(ctx, id)
}

func (s *spansRequestTelemetryStore) InsertRequestTelemetryWithLogEvent(ctx context.Context, arg sqlc.InsertRequestTelemetryParams, eventID string) error {
	return s.rows.InsertRequestTelemetryWithLogEvent(ctx, arg, eventID)
}

func (s *spansRequestTelemetryStore) RecordAPIConsumerUsage(ctx context.Context, event state.APIConsumerUsageEvent) (bool, error) {
	return s.rows.RecordAPIConsumerUsage(ctx, event)
}

func TestSpansWriterForwardsTargetAndBoundsByResourceOwner(t *testing.T) {
	w := newSpansTenancyWorld()
	r := NewSpansWriterReceiver(w.store, nil, peraccount.NewLimiter(), true)
	ctx := context.Background()
	const trace = "7bf92f3577b34da6a3ce929d0e0e4736"

	resp, err := r.WriteSpansSummary(ctx, &apidpb.WriteSpansSummaryRequest{
		TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.creator.ID, AppId: w.companyApp.ID,
	})
	if err != nil || resp.GetOutcome() != swOutcomeInserted {
		t.Fatalf("company app write: %v %v", resp, err)
	}
	got := w.store.writes[0]
	if got.TraceID != trace || got.AccountID.String() != w.creator.ID || got.AppID.String() != w.companyApp.ID || got.OrgID != uuid.Nil || got.CustomerCredential {
		t.Fatalf("forwarded target = %+v", got)
	}
	if want := api.MustLimitsFor(api.PlanScale).DebugTelemetrySpansPerTrace; w.store.maxSpan[0] != want {
		t.Fatalf("company app bounded to %d spans, want the company's Scale cap %d", w.store.maxSpan[0], want)
	}

	resp, err = r.WriteSpansSummary(ctx, &apidpb.WriteSpansSummaryRequest{
		TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.teammate.ID, OrgId: w.company.ID, CustomerCredential: true,
	})
	if err != nil || resp.GetOutcome() != swOutcomeInserted {
		t.Fatalf("company key write: %v %v", resp, err)
	}
	if got := w.store.writes[1]; got.OrgID.String() != w.company.ID || !got.CustomerCredential || got.AppID != uuid.Nil {
		t.Fatalf("company key target = %+v", got)
	}

	// The Free personal app keeps no rows: nothing to merge into.
	resp, err = r.WriteSpansSummary(ctx, &apidpb.WriteSpansSummaryRequest{
		TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.creator.ID, AppId: w.personalApp.ID,
	})
	if err != nil || resp.GetOutcome() != swOutcomeRateLimited || len(w.store.writes) != 2 {
		t.Fatalf("Free app write = %v %v (writes %d), want dropped as rate_limited", resp, err, len(w.store.writes))
	}

	for name, req := range map[string]*apidpb.WriteSpansSummaryRequest{
		"bad app": {TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.creator.ID, AppId: "nope"},
		"bad org": {TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.creator.ID, OrgId: "nope"},
		"no app":  {TraceId: trace, SummaryJson: []byte(`[]`), AccountId: w.creator.ID, AppId: uuid.NewString()},
	} {
		if _, err := r.WriteSpansSummary(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: %v, want InvalidArgument", name, err)
		}
	}
}

func TestSpansTraceKeyPrincipal(t *testing.T) {
	w := newSpansTenancyWorld()
	for _, tc := range []struct {
		name     string
		holder   state.Account
		key      state.APIKey
		released bool
		code     codes.Code
		org      string
		plan     api.Plan
	}{
		{"read-only key", w.creator, state.APIKey{Scopes: []string{api.ScopeAppsRead}, OrgID: w.personalOrg.ID}, true, codes.PermissionDenied, "", ""},
		{"metrics key", w.creator, state.APIKey{Scopes: []string{api.ScopeMetricsWrite}, OrgID: w.personalOrg.ID}, true, codes.OK, "", api.PlanFree},
		{"deploy key", w.creator, state.APIKey{Scopes: []string{api.ScopeDeployWrite}}, true, codes.OK, "", api.PlanFree},
		{"not released", w.creator, state.APIKey{Scopes: []string{api.ScopeAdmin}}, false, codes.NotFound, "", ""},
		// A teammate's company key writes the company's apps on its plan.
		{"company key", w.teammate, state.APIKey{Scopes: []string{api.ScopeAdmin}, OrgID: w.company.ID}, true, codes.OK, w.company.ID, api.PlanScale},
		{"company viewer", w.viewer, state.APIKey{Scopes: []string{api.ScopeAdmin}, OrgID: w.company.ID}, true, codes.PermissionDenied, "", ""},
		{"company non-member", state.Account{ID: uuid.NewString(), Plan: api.PlanPro}, state.APIKey{Scopes: []string{api.ScopeAdmin}, OrgID: w.company.ID}, true, codes.PermissionDenied, "", ""},
		{"app-bound key", w.creator, state.APIKey{Scopes: []string{api.ScopeAdmin}, AppID: w.companyApp.ID}, true, codes.OK, "", api.PlanScale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w.store.holder, w.store.key = tc.holder, tc.key
			var subject platformflags.Subject
			cli := dialAuthBufconnWith(t, w.store, func(s platformflags.Subject) bool { subject = s; return tc.released })
			resp, err := cli.AuthenticateKey(context.Background(), &apidpb.AuthenticateKeyRequest{Token: "faas_live_trace"})
			if status.Code(err) != tc.code {
				t.Fatalf("code = %v (%v), want %v", status.Code(err), err, tc.code)
			}
			if tc.code == codes.PermissionDenied && tc.name == "read-only key" && subject != (platformflags.Subject{}) {
				t.Fatal("tracing release was decided before the scope check")
			}
			if tc.code != codes.OK {
				return
			}
			if subject != (platformflags.Subject{AccountID: tc.holder.ID, Plan: string(tc.holder.Plan), OrgID: tc.key.OrgID}) {
				t.Fatalf("release subject = %+v, want the holder's, as GET /v1/account/traces decides", subject)
			}
			if resp.GetAccountId() != tc.holder.ID || resp.GetOrgId() != tc.org || resp.GetPlan() != string(tc.plan) || resp.GetAppId() != tc.key.AppID {
				t.Fatalf("principal = %+v, want org %q plan %q app %q", resp, tc.org, tc.plan, tc.key.AppID)
			}
		})
	}
}

func TestSpansGuestIngestTargetsTheAppAndHonorsRelease(t *testing.T) {
	w := newSpansTenancyWorld()
	released := true
	g := NewGuestSpansIngester(w.store, peraccount.NewLimiter(), discardLogger()).
		WithTracingRelease(func(s platformflags.Subject) bool {
			if s.OrgID != w.company.ID || s.Plan != string(api.PlanScale) {
				t.Errorf("guest release subject = %+v, want the company app's resource owner", s)
			}
			return released
		})
	req := &apidpb.IngestGuestSpansRequest{
		AccountId: w.creator.ID, AppId: w.companyApp.ID, DeploymentId: uuid.NewString(), InstanceId: "i-1",
		Codec: uint32(api.TraceCodecProtobuf), Payload: guestSpansPayload(t, 3),
	}
	// The Free creator's plan would refuse; the company's Scale plan admits.
	if got := g.ingest(context.Background(), req).GetOutcome(); got != guestSpansAccepted {
		t.Fatalf("company app export = %q, want accepted", got)
	}
	target := gateway.SpanTarget{AccountID: uuid.MustParse(w.creator.ID), AppID: uuid.MustParse(w.companyApp.ID)}
	if spans, _ := g.acc.DrainAndRemoveTarget("00000000000000000000000000000003", target); len(spans) != 1 {
		t.Fatalf("spans buffered for the app's target = %d, want 1", len(spans))
	}
	released = false
	if got := g.ingest(context.Background(), req).GetOutcome(); got != guestSpansDisabled {
		t.Fatalf("export with tracing withheld = %q, want disabled", got)
	}
	bad := &apidpb.IngestGuestSpansRequest{
		AccountId: w.creator.ID, AppId: "not-a-uuid", DeploymentId: uuid.NewString(), InstanceId: "i-1",
		Codec: uint32(api.TraceCodecProtobuf), Payload: guestSpansPayload(t, 4),
	}
	if got := g.ingest(context.Background(), bad).GetOutcome(); got != guestSpansInvalid {
		t.Fatalf("export with a malformed app = %q, want invalid", got)
	}
}
