package apidserver_test

// adr: 1700 — platform flags: per-account release gates written through the
// hot runtime-configuration path and evaluated without a restart.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// subjectOf decides for an account outside any organization context.
func subjectOf(acct state.Account) platformflags.Subject {
	return platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)}
}

func platformFlagRule(id string, value bool, mutate func(*platformflags.Rule)) platformflags.Rule {
	rule := platformflags.Rule{ID: id, Value: &value}
	mutate(&rule)
	return rule
}

// patchPlatformConfig writes a platform-flag row through the operator
// mutation path and returns the status code.
func patchPlatformConfig(t *testing.T, e apidserver.TestEnv, configKey string, value any, expected int64) int {
	t.Helper()
	return e.DoAdmin(t, http.MethodPatch, "/v1/admin/config/"+configKey, map[string]any{
		"value": value, "reason": "platform flag test", "expected_version": expected,
	}, nil).Code
}

func decodeBody[T any](t *testing.T, code int, body []byte, want int) T {
	t.Helper()
	var out T
	if code != want {
		t.Fatalf("status = %d, want %d: %s", code, want, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func TestPlatformFlagsAdminLifecycle(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	(*e.S()).EnableRuntimeConfigReceipts("apid")
	account := (*e.Acct()).ID

	list := e.Do(t, http.MethodGet, "/v1/admin/flags", nil, nil)
	initial := decodeBody[api.OperatorPlatformFlagList](t, list.Code, list.Body.Bytes(), http.StatusOK)
	if len(initial.Items) != len(platformflags.Catalog()) {
		t.Fatalf("items = %d, want every catalogued flag", len(initial.Items))
	}
	for _, item := range initial.Items {
		// Hand-written flags boot off; generated capability flags boot at their
		// tier default (ADR-20261023115741).
		spec, _ := platformflags.Lookup(item.Key)
		if item.Source != "default_or_environment" || item.Definition.Enabled != spec.Default.Enabled || item.Definition.Default != spec.Default.Default || item.ConfigKey != platformflags.ConfigKey(item.Key) {
			t.Fatalf("fresh flag = %+v, want its declared bootstrap value %+v", item, spec.Default)
		}
		if !spec.Generated && item.Definition.Enabled {
			t.Fatalf("hand-written flag %s must boot off", item.Key)
		}
	}

	groupRule := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("partners", true, func(r *platformflags.Rule) { r.Group = "partners" }),
	}}
	flagKey := platformflags.ConfigKey(platformflags.AIGateway)
	if code := patchPlatformConfig(t, e, flagKey, groupRule, 0); code != http.StatusConflict {
		t.Fatalf("a rule naming a missing group = %d, want 409", code)
	}
	missingValue := map[string]any{"enabled": true, "rules": []map[string]any{{"id": "r1", "accounts": []string{account}}}}
	if code := patchPlatformConfig(t, e, flagKey, missingValue, 0); code != http.StatusBadRequest {
		t.Fatalf("a rule without a value = %d, want 400", code)
	}
	if code := patchPlatformConfig(t, e, platformflags.GroupsConfigKey, platformflags.Groups{"partners": {account}}, 0); code != http.StatusOK {
		t.Fatalf("create groups = %d", code)
	}
	if code := patchPlatformConfig(t, e, flagKey, groupRule, 0); code != http.StatusOK {
		t.Fatalf("publish flag = %d", code)
	}
	if code := patchPlatformConfig(t, e, platformflags.GroupsConfigKey, platformflags.Groups{}, 1); code != http.StatusConflict {
		t.Fatalf("removing a group a flag uses = %d, want 409", code)
	}

	decision := e.Do(t, http.MethodGet, "/v1/admin/flags/ai_gateway/decision?account_id="+account, nil, nil)
	got := decodeBody[api.OperatorPlatformFlagDecision](t, decision.Code, decision.Body.Bytes(), http.StatusOK)
	if !got.Enabled || got.Reason != "rule_match" || got.RuleID != "partners" || got.Entitled == nil || !*got.Entitled || !got.Available || got.Version != 1 {
		t.Fatalf("decision = %+v", got)
	}
	view := e.Do(t, http.MethodGet, "/v1/admin/accounts/"+account+"/flags", nil, nil)
	accountFlags := decodeBody[api.OperatorAccountPlatformFlags](t, view.Code, view.Body.Bytes(), http.StatusOK)
	if accountFlags.Plan != string(api.PlanPro) || len(accountFlags.Items) != len(platformflags.Catalog()) {
		t.Fatalf("account view = %+v", accountFlags)
	}

	detail := e.Do(t, http.MethodGet, "/v1/admin/flags/ai_gateway", nil, nil)
	flag := decodeBody[api.OperatorPlatformFlag](t, detail.Code, detail.Body.Bytes(), http.StatusOK)
	if flag.Source != "operator" || flag.Version != 1 || len(flag.Definition.Rules) != 1 {
		t.Fatalf("flag detail = %+v", flag)
	}
	if !slices.Equal(flag.Convergence.Converged, []string{"apid"}) || !slices.Equal(flag.Convergence.Pending, []string{"outboundd"}) {
		t.Fatalf("convergence = %+v, want apid applied and outboundd pending", flag.Convergence)
	}
	if len(flag.Receipts) != 1 || flag.Receipts[0].Daemon != "apid" || flag.Receipts[0].Version != 1 || flag.Receipts[0].Status != "applied" {
		t.Fatalf("receipts = %+v", flag.Receipts)
	}
	after := e.Do(t, http.MethodGet, "/v1/admin/flags", nil, nil)
	groups := decodeBody[api.OperatorPlatformFlagList](t, after.Code, after.Body.Bytes(), http.StatusOK).Groups
	if groups.Version != 1 || !slices.Equal(groups.References["partners"], []string{platformflags.AIGateway}) {
		t.Fatalf("groups = %+v", groups)
	}

	for path, want := range map[string]int{
		"/v1/admin/flags/not_a_flag":                                         http.StatusNotFound,
		"/v1/admin/flags/ai_gateway/decision?account_id=acme":                http.StatusBadRequest,
		"/v1/admin/flags/ai_gateway/decision?account_id=" + uuid.NewString(): http.StatusNotFound,
		"/v1/admin/accounts/" + uuid.NewString() + "/flags":                  http.StatusNotFound,
	} {
		if res := e.Do(t, http.MethodGet, path, nil, nil); res.Code != want {
			t.Errorf("GET %s = %d, want %d", path, res.Code, want)
		}
	}
}

func TestPlatformFlagRoutesRefuseNonOperators(t *testing.T) {
	notAllowlisted := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "rogue@faas.dev")
	customer := apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	for _, path := range []string{"/v1/admin/flags", "/v1/admin/flags/ai_gateway", "/v1/admin/flags/ai_gateway/decision?account_id=" + uuid.NewString(), "/v1/admin/accounts/" + uuid.NewString() + "/flags"} {
		if res := notAllowlisted.Do(t, http.MethodGet, path, nil, nil); res.Code != http.StatusForbidden {
			t.Errorf("non-allowlisted admin GET %s = %d, want 403", path, res.Code)
		}
		if res := customer.Do(t, http.MethodGet, path, nil, nil); res.Code != http.StatusForbidden {
			t.Errorf("customer GET %s = %d, want 403", path, res.Code)
		}
	}
}

func TestAIGatewayFlagAppliesPerAccountWithoutRestart(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	account := (*e.Acct()).ID
	env := func(name string) string {
		return map[string]string{"FAAS_AI_GATEWAY_ENABLED": "1", "FAAS_AI_GATEWAY_ACCOUNTS": uuid.NewString()}[name]
	}
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{}, env); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil), http.StatusForbidden, "outbound_ai_gateway_unavailable")

	allow := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("partners", true, func(r *platformflags.Rule) { r.Accounts = []string{account} }),
	}}
	if _, err := (*e.S()).ApplyRuntimeConfig(platformflags.ConfigKey(platformflags.AIGateway), allow.Marshal(), 1); err != nil {
		t.Fatal(err)
	}
	if list := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil); list.Code != http.StatusOK {
		t.Fatalf("routes after the operator released the account = %d %s", list.Code, list.Body.String())
	}
	if capability := aiGatewayCapability(t, e); !capability.Enabled {
		t.Fatalf("capability after release = %+v", capability)
	}
	// A restart re-seeds the environment fallback at version 0; the durable
	// operator value must still win.
	if err := (*e.S()).ConfigureAIGateway(apidserver.Config{}, env); err != nil {
		t.Fatal(err)
	}
	if list := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil); list.Code != http.StatusOK {
		t.Fatalf("the boot fallback replaced the operator value: %d", list.Code)
	}
	if _, err := (*e.S()).ApplyRuntimeConfig(platformflags.ConfigKey(platformflags.AIGateway), platformflags.Definition{Default: true}.Marshal(), 2); err != nil {
		t.Fatal(err)
	}
	res := e.Do(t, http.MethodGet, "/v1/outbound/ai-routes", nil, nil)
	apidserver.AssertProblem(t, res, http.StatusForbidden, "outbound_ai_gateway_unavailable")
	if !containsDetail(res.Body.Bytes(), "not enabled on this platform") {
		t.Fatalf("the kill switch must report the platform gate: %s", res.Body.String())
	}
}

func TestCustomerFlagsReleasedPerAccount(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	// Project access (#4882) answers 404 for an unknown project before the
	// release gate, so the request must name a real one.
	if _, err := (*e.Store()).CreateProject(context.Background(), state.Project{AccountID: (*e.Acct()).ID, Slug: "released", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/released/environments/production/flags"
	off := e.Do(t, http.MethodGet, path, nil, nil)
	apidserver.AssertProblem(t, off, http.StatusForbidden, api.CodeForbidden)
	other := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("pilot", true, func(r *platformflags.Rule) { r.Accounts = []string{uuid.NewString()} }),
	}}
	(*e.S()).SeedPlatformFlag(platformflags.CustomerFlags, other)
	notYet := e.Do(t, http.MethodGet, path, nil, nil)
	apidserver.AssertProblem(t, notYet, http.StatusForbidden, api.CodeForbidden)
	if !containsDetail(notYet.Body.Bytes(), "not enabled for this account yet") {
		t.Fatalf("hint must name the per-account release: %s", notYet.Body.String())
	}
	other.Rules[0].Accounts = []string{(*e.Acct()).ID}
	(*e.S()).SeedPlatformFlag(platformflags.CustomerFlags, other)
	if res := e.Do(t, http.MethodGet, path, nil, nil); res.Code == http.StatusForbidden {
		t.Fatalf("released account still refused: %s", res.Body.String())
	}
}

// containsDetail reports whether a problem's detail or hint mentions want.
func containsDetail(body []byte, want string) bool {
	var problem struct {
		Detail string `json:"detail"`
		Hint   string `json:"hint"`
	}
	if json.Unmarshal(body, &problem) != nil {
		return false
	}
	return strings.Contains(problem.Detail, want) || strings.Contains(problem.Hint, want)
}

// The gates that shipped as environment variables before their platform
// flags keep deciding until an operator writes the flag; from then on the
// flag decides, per account, without a restart.
func TestEnvGatesYieldToOperatorPlatformFlags(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, acct := *e.S(), *e.Acct()
	other := state.Account{ID: uuid.NewString(), Plan: api.PlanPro}
	me, them := subjectOf(acct), subjectOf(other)
	only := func(id string) json.RawMessage {
		return platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
			platformFlagRule("pilot", true, func(r *platformflags.Rule) { r.Accounts = []string{id} }),
		}}.Marshal()
	}
	apply := func(flag string, value json.RawMessage, version int64) {
		t.Helper()
		if _, err := s.ApplyRuntimeConfig(platformflags.ConfigKey(flag), value, version); err != nil {
			t.Fatalf("apply %s v%d: %v", flag, version, err)
		}
	}
	off := platformflags.Definition{}.Marshal()

	t.Run("application_auth", func(t *testing.T) {
		t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
		if !s.ApplicationAuthEnabledFor(me) {
			t.Fatal("the environment gate must decide until an operator writes the flag")
		}
		apply(platformflags.ApplicationAuth, off, 1)
		if s.ApplicationAuthEnabledFor(me) {
			t.Fatal("an operator kill switch must override FAAS_APPLICATION_AUTH_ENABLED")
		}
		apply(platformflags.ApplicationAuth, only(acct.ID), 2)
		if !s.ApplicationAuthEnabledFor(me) || s.ApplicationAuthEnabledFor(them) {
			t.Fatal("the operator rule must release Auth to the listed account only")
		}
	})
	t.Run("change_timeline", func(t *testing.T) {
		if !s.ChangeTimelineEnabledFor(me, true) || s.ChangeTimelineEnabledFor(me, false) {
			t.Fatal("FAAS_CHANGE_TIMELINE_ENABLED must decide until an operator writes the flag")
		}
		apply(platformflags.ChangeTimeline, only(acct.ID), 1)
		if !s.ChangeTimelineEnabledFor(me, false) || s.ChangeTimelineEnabledFor(them, true) {
			t.Fatal("the operator rule must decide once written, whatever the environment says")
		}
	})
	t.Run("anomaly_alerts", func(t *testing.T) {
		if s.AnomalyAlertsAllowedFor(me, false) || !s.AnomalyAlertsAllowedFor(me, true) {
			t.Fatal("FAAS_ANOMALY_ALERTS_ENABLED must decide until an operator writes the flag")
		}
		apply(platformflags.AnomalyAlerts, only(acct.ID), 1)
		if !s.AnomalyAlertsAllowedFor(me, false) || s.AnomalyAlertsAllowedFor(them, true) {
			t.Fatal("the operator rule must decide once written, whatever the environment says")
		}
	})
	t.Run("trigger_batch_delivery", func(t *testing.T) {
		if s.TriggerBatchDeliveryAllowedFor(me, false) || !s.TriggerBatchDeliveryAllowedFor(me, true) {
			t.Fatal("FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED must decide until an operator writes the flag")
		}
		apply(platformflags.TriggerBatchDelivery, only(acct.ID), 1)
		if !s.TriggerBatchDeliveryAllowedFor(me, false) || s.TriggerBatchDeliveryAllowedFor(them, true) {
			t.Fatal("the operator rule must decide once written, whatever the environment says")
		}
	})
	t.Run("operation_schedules_and_children", func(t *testing.T) {
		t.Setenv("FAAS_OPERATION_SCHEDULES_ACCOUNTS", acct.ID)
		t.Setenv("FAAS_OPERATION_CHILDREN_ACCOUNTS", acct.ID)
		if !s.OperationSchedulesOpen(acct.ID) || s.OperationSchedulesOpen(other.ID) || !s.OperationChildrenOpen(acct.ID) {
			t.Fatal("the environment allowlists must decide until an operator writes the flags")
		}
		apply(platformflags.OperationSchedules, only(other.ID), 1)
		apply(platformflags.OperationChildren, off, 1)
		if s.OperationSchedulesOpen(acct.ID) || !s.OperationSchedulesOpen(other.ID) || s.OperationChildrenOpen(acct.ID) {
			t.Fatal("the operator values must decide once written, whatever the allowlists say")
		}
	})
	t.Run("service_map_guest_tracing_static_egress", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/service-map", nil)
		if !s.ServiceMapReleased(req, acct, true) || s.GuestTracingReleased(acct, false) {
			t.Fatal("the environment gates must decide until an operator writes the flags")
		}
		t.Setenv("FAAS_STATIC_EGRESS_IP_ENABLED", "1")
		if !s.StaticEgressIPReleased(me) {
			t.Fatal("FAAS_STATIC_EGRESS_IP_ENABLED must decide until an operator writes the flag")
		}
		apply(platformflags.ServiceMap, only(acct.ID), 1)
		apply(platformflags.GuestTracing, only(acct.ID), 1)
		apply(platformflags.StaticEgressIP, off, 1)
		if !s.ServiceMapReleased(req, acct, false) || s.ServiceMapReleased(req, other, true) {
			t.Fatal("service_map must follow the operator rule once written")
		}
		if !s.GuestTracingReleased(acct, false) || s.GuestTracingReleased(other, true) {
			t.Fatal("guest_tracing must follow the operator rule once written")
		}
		if s.StaticEgressIPReleased(me) {
			t.Fatal("an operator kill switch must override FAAS_STATIC_EGRESS_IP_ENABLED")
		}
	})
	t.Run("live_queries", func(t *testing.T) {
		if err := s.ConfigureLiveQueriesForTest(func(string) string { return "" }); err != nil {
			t.Fatal(err)
		}
		if !s.LiveGate().Off() {
			t.Fatal("an empty FAAS_LIVE_QUERIES_ACCOUNTS must keep live queries off")
		}
		apply(platformflags.LiveQueries, only(acct.ID), 1)
		livePath := httptest.NewRequest(http.MethodGet, "/v1/postgres/databases/db-1/live", nil)
		gate := s.LiveGate()
		if gate.Off() || !gate.Allows(acct.ID) || gate.Allows(other.ID) || !gate.Hidden(livePath) {
			t.Fatal("an account rule must release live queries to that account and keep the routes hidden")
		}
		apply(platformflags.LiveQueries, platformflags.Definition{Enabled: true, Default: true}.Marshal(), 2)
		if gate := s.LiveGate(); !gate.Allows(other.ID) || gate.Hidden(livePath) {
			t.Fatal("releasing to every account must stop hiding the routes")
		}
	})
}

// Company targeting (ADR 20261021080640): an org rule releases a feature to
// whoever acts under that organization, never to the same person outside it.
func TestPlatformFlagOrgRulesFollowTheRequestOrganization(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, acct := *e.S(), *e.Acct()
	org := uuid.NewString()
	def := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("acme", true, func(r *platformflags.Rule) { r.Orgs = []string{org} }),
	}}
	if _, err := s.ApplyRuntimeConfig(platformflags.ConfigKey(platformflags.ApplicationAuth), def.Marshal(), 1); err != nil {
		t.Fatal(err)
	}
	inside := platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: org}
	colleague := platformflags.Subject{AccountID: uuid.NewString(), Plan: string(api.PlanFree), OrgID: org}
	if !s.ApplicationAuthEnabledFor(inside) || !s.ApplicationAuthEnabledFor(colleague) {
		t.Fatal("an org rule must release the feature to everyone acting under the organization")
	}
	if s.ApplicationAuthEnabledFor(subjectOf(acct)) {
		t.Fatal("the same person outside the organization must not match an org rule")
	}
}

func TestFlagSubjectReadsTheRequestOrganization(t *testing.T) {
	acct := state.Account{ID: uuid.NewString(), Plan: api.PlanPro}
	keyOrg, activeOrg := uuid.NewString(), uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	if got := apidserver.FlagSubject(req, acct); got.OrgID != "" {
		t.Fatalf("no principal: org = %q, want none", got.OrgID)
	}
	key := &state.APIKey{ID: uuid.NewString(), OrgID: keyOrg}
	withKey := req.WithContext(middleware.WithPrincipal(req.Context(), acct, key, nil))
	if got := apidserver.FlagSubject(withKey, acct); got.OrgID != keyOrg || got.AccountID != acct.ID || got.Plan != string(api.PlanPro) {
		t.Fatalf("API key request subject = %+v, want the key's organization", got)
	}
	member := &state.OrgMembership{OrgID: activeOrg, AccountID: acct.ID}
	active := req.WithContext(middleware.WithPrincipal(req.Context(), acct, key, member))
	if got := apidserver.FlagSubject(active, acct); got.OrgID != activeOrg {
		t.Fatalf("active-org request org = %q, want the active organization over the key's", got.OrgID)
	}
}

// A rule using an attribute some consumer cannot supply would silently match
// nobody there, so the write is refused.
func TestPlatformFlagWritesRefuseUnsupportedTargeting(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	orgRule := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("acme", true, func(r *platformflags.Rule) { r.Orgs = []string{uuid.NewString()} }),
	}}
	planRule := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		platformFlagRule("pro", true, func(r *platformflags.Rule) { r.Plans = []string{string(api.PlanPro)} }),
	}}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.AIGateway), orgRule, 0); code != http.StatusConflict {
		t.Fatalf("org rule on ai_gateway = %d, want 409: outboundd cannot see the organization", code)
	}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.LiveQueries), planRule, 0); code != http.StatusConflict {
		t.Fatalf("plan rule on live_queries = %d, want 409: the realtime path knows only the account", code)
	}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.ApplicationAuth), orgRule, 0); code != http.StatusOK {
		t.Fatalf("org rule on application_auth = %d, want 200", code)
	}
}

func TestEnvGatesSeedTheirPlatformFlags(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, acct := *e.S(), *e.Acct()
	s.SeedEnvPlatformFlags(func(key string) string {
		if key == "FAAS_APPLICATION_AUTH_ACCOUNTS" {
			return " " + acct.ID + " ,"
		}
		return ""
	})
	def, version, ok := s.PlatformFlagSet().Snapshot().Definition(platformflags.ApplicationAuth)
	want := []string{uuid.MustParse(acct.ID).String()} // stored in canonical form
	if !ok || version != 0 || !def.Enabled || def.Default || len(def.Rules) != 1 || !slices.Equal(def.Rules[0].Accounts, want) {
		t.Fatalf("application_auth boot fallback = %+v (v%d), want the listed account only", def, version)
	}
	if def, _, _ := s.PlatformFlagSet().Snapshot().Definition(platformflags.ChangeTimeline); def.Enabled {
		t.Fatal("an unset FAAS_CHANGE_TIMELINE_ENABLED must seed change_timeline off")
	}
}

func TestRetireByGaugeCoversOnlyDatedPreviews(t *testing.T) {
	registry := prometheus.NewRegistry()
	apidserver.RegisterPlatformFlagRetireBy(registry)
	families, err := registry.Gather()
	if err != nil || len(families) != 1 || families[0].GetName() != "apid_platform_flag_retire_by_timestamp_seconds" {
		t.Fatalf("gather = %v, %v", families, err)
	}
	got := map[string]float64{}
	for _, metric := range families[0].GetMetric() {
		got[metric.GetLabel()[0].GetValue()] = metric.GetGauge().GetValue()
	}
	for _, spec := range platformflags.Catalog() {
		at, dated := spec.RetireByTime()
		value, exported := got[spec.Key]
		if exported != dated || (dated && value != float64(at.Unix())) {
			t.Errorf("%s: exported=%t value=%v, want exported=%t at %v", spec.Key, exported, value, dated, at)
		}
	}
}
