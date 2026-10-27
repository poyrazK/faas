package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// adr: 1700 — gregalectl flags edits a platform flag with the version it read.

const (
	testFlagsAccount = "11111111-1111-4111-8111-111111111111"
	testFlagsOther   = "22222222-2222-4222-8222-222222222222"
)

func testFlagsList(def api.PlatformFlagDefinition, source string, version int64) api.OperatorPlatformFlagList {
	return api.OperatorPlatformFlagList{
		Items: []api.OperatorPlatformFlag{{
			Key: platformflags.AIGateway, ConfigKey: platformflags.ConfigKey(platformflags.AIGateway), Label: "AI gateway",
			ADR: "ADR-998", Consumers: []string{"apid", "outboundd"}, Definition: def, Source: source, Version: version, Status: "applied",
			Convergence: api.OperatorPlatformFlagConvergence{Converged: []string{"apid"}, Pending: []string{"outboundd"}, Failed: []string{}},
		}},
		Groups: api.OperatorPlatformFlagGroups{ConfigKey: platformflags.GroupsConfigKey, Groups: map[string][]string{"internal": {testFlagsOther}}, Version: 2},
	}
}

// flagsServer serves one flag list and captures the single write.
func flagsServer(t *testing.T, list api.OperatorPlatformFlagList, writePath string, captured *operatorConfigPatchRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/flags":
			writeTestJSON(w, http.StatusOK, list)
		case r.Method == http.MethodPatch && r.URL.Path == writePath:
			assertConfigMutationHeaders(t, r)
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			writeTestJSON(w, http.StatusOK, api.OperatorRuntimeConfig{Key: strings.TrimPrefix(writePath, "/v1/admin/config/"), Version: 9, Status: "applied", EffectiveValue: captured.Value})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func runFlagsCommand(t *testing.T, serverURL string, args ...string) (int, string, string) {
	t.Helper()
	installTestOperatorSession(t, serverURL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	code := cmdFlagsDispatch(args)
	return code, out.String(), stderr.String()
}

func decodeFlagValue(t *testing.T, raw json.RawMessage) platformflags.Definition {
	t.Helper()
	def, err := platformflags.ParseDefinition(raw)
	if err != nil {
		t.Fatalf("written definition is invalid: %v (%s)", err, raw)
	}
	return def
}

func TestFlagsAllowDenyAndRevokeKeepExplicitRulesFirst(t *testing.T) {
	value := true
	start := api.PlatformFlagDefinition{Enabled: true, Rules: []api.PlatformFlagRule{{ID: "internal", Group: "internal", Value: &value}}}
	mutation := []string{"--key", platformflags.AIGateway, "--account-id", testFlagsAccount, "--reason", "design partner", "--trace-id", testConfigTraceID, "--yes"}

	var allow operatorConfigPatchRequest
	server := flagsServer(t, testFlagsList(start, "operator", 4), "/v1/admin/config/platform_flag.ai_gateway", &allow)
	code, out, stderr := runFlagsCommand(t, server.URL, append([]string{"allow"}, mutation...)...)
	server.Close()
	if code != 0 {
		t.Fatalf("allow exit = %d, stderr=%s", code, stderr)
	}
	if allow.ExpectedVersion == nil || *allow.ExpectedVersion != 4 || allow.Reason != "design partner" {
		t.Fatalf("allow request = %+v", allow)
	}
	def := decodeFlagValue(t, allow.Value)
	if len(def.Rules) != 2 || def.Rules[0].ID != flagsAllowRuleID || def.Rules[0].Accounts[0] != testFlagsAccount || !*def.Rules[0].Value {
		t.Fatalf("allow rules = %+v", def.Rules)
	}
	if !strings.Contains(out, "version=9") || !strings.Contains(out, "trace_id="+testConfigTraceID) {
		t.Fatalf("stdout = %q", out)
	}

	var deny operatorConfigPatchRequest
	allowed := api.PlatformFlagDefinition{Enabled: true, Rules: []api.PlatformFlagRule{
		{ID: flagsAllowRuleID, Accounts: []string{testFlagsAccount}, Value: &value}, {ID: "internal", Group: "internal", Value: &value},
	}}
	server = flagsServer(t, testFlagsList(allowed, "operator", 5), "/v1/admin/config/platform_flag.ai_gateway", &deny)
	if code, _, stderr := runFlagsCommand(t, server.URL, append([]string{"deny"}, mutation...)...); code != 0 {
		t.Fatalf("deny exit = %d, stderr=%s", code, stderr)
	}
	server.Close()
	def = decodeFlagValue(t, deny.Value)
	if len(def.Rules) != 2 || def.Rules[0].ID != flagsDenyRuleID || *def.Rules[0].Value || def.Rules[1].ID != "internal" {
		t.Fatalf("deny must move the account to a leading deny rule and drop the empty allow rule: %+v", def.Rules)
	}

	var revoke operatorConfigPatchRequest
	server = flagsServer(t, testFlagsList(allowed, "operator", 5), "/v1/admin/config/platform_flag.ai_gateway", &revoke)
	if code, _, stderr := runFlagsCommand(t, server.URL, append([]string{"revoke"}, mutation...)...); code != 0 {
		t.Fatalf("revoke exit = %d, stderr=%s", code, stderr)
	}
	server.Close()
	if def = decodeFlagValue(t, revoke.Value); len(def.Rules) != 1 || def.Rules[0].ID != "internal" {
		t.Fatalf("revoke rules = %+v", def.Rules)
	}
}

// adr: 20261021080640 — a person's explicit decision outranks their
// company's, and a deny outranks an allow at each level.
func TestFlagsOrgDecisionsSitAfterAccountDecisions(t *testing.T) {
	const org = "44444444-4444-4444-8444-444444444444"
	def := platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{
		{ID: flagsRolloutRuleID, Rollout: new(int), Value: new(bool)},
	}}
	addSubjectToRule(&def, flagsOrgAllowRuleID, org, true, true)
	addSubjectToRule(&def, flagsAllowRuleID, testFlagsAccount, false, true)
	addSubjectToRule(&def, flagsOrgDenyRuleID, org, true, false)
	addSubjectToRule(&def, flagsDenyRuleID, testFlagsOther, false, false)
	var order []string
	for _, rule := range def.Rules {
		order = append(order, rule.ID)
	}
	want := []string{flagsDenyRuleID, flagsAllowRuleID, flagsOrgDenyRuleID, flagsOrgAllowRuleID, flagsRolloutRuleID}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("rule order = %v, want %v", order, want)
	}
	if got := def.Rules[3].Orgs; len(got) != 1 || got[0] != org || len(def.Rules[3].Accounts) != 0 {
		t.Fatalf("org allow rule = %+v, want only the organization", def.Rules[3])
	}
	removeSubjectFromRule(&def, flagsOrgAllowRuleID, org, true)
	for _, rule := range def.Rules {
		if rule.ID == flagsOrgAllowRuleID {
			t.Fatal("revoking the last organization must drop its now-empty rule")
		}
	}
	if flagsRequireSubject("allow", testFlagsAccount, org) || flagsRequireSubject("allow", "", "") || !flagsRequireSubject("allow", "", org) {
		t.Fatal("allow needs exactly one of --account-id or --org-id")
	}
}

// adr: 20261021080640 — schedule boundaries: empty keeps, none clears,
// anything else is RFC 3339 stored in UTC.
func TestFlagsScheduleBoundaries(t *testing.T) {
	if at, keep, err := parseScheduleBoundary(""); at != nil || !keep || err != nil {
		t.Fatalf("empty = %v %v %v, want keep", at, keep, err)
	}
	if at, keep, err := parseScheduleBoundary("none"); at != nil || keep || err != nil {
		t.Fatalf("none = %v %v %v, want clear", at, keep, err)
	}
	at, keep, err := parseScheduleBoundary("2026-11-02T12:00:00+03:00")
	if err != nil || keep || at == nil || at.Location() != time.UTC || at.Hour() != 9 {
		t.Fatalf("offset time = %v %v %v, want 09:00 UTC", at, keep, err)
	}
	if _, _, err := parseScheduleBoundary("next monday"); err == nil {
		t.Fatal("a non-RFC 3339 time must be refused")
	}
}

func TestFlagsRetireByMarksOverdueFlags(t *testing.T) {
	now := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ retireBy, want string }{
		{"", ""},
		{"2027-02-01", " retire_by=2027-02-01"},
		{"2027-01-31", " retire_by=2027-01-31 overdue=true"},
	} {
		if got := flagsRetireBy(tc.retireBy, now); got != tc.want {
			t.Errorf("flagsRetireBy(%q) = %q, want %q", tc.retireBy, got, tc.want)
		}
	}
}

func TestFlagsRampDefinesResumesAndRemoves(t *testing.T) {
	rollout, value := 1000, true
	current := api.PlatformFlagDefinition{Enabled: true, Rules: []api.PlatformFlagRule{{ID: "rollout", Rollout: &rollout, Value: &value,
		Ramp: &api.PlatformFlagRamp{Stages: []int{1000, 5000}, IntervalSeconds: 300, OnBreach: "pause", Paused: true, PausedReason: "breach"}}}}
	run := func(extra ...string) platformflags.Rule {
		t.Helper()
		var write operatorConfigPatchRequest
		server := flagsServer(t, testFlagsList(current, "operator", 4), "/v1/admin/config/platform_flag.ai_gateway", &write)
		defer server.Close()
		args := append([]string{"ramp", "--key", platformflags.AIGateway, "--reason", "staged release", "--trace-id", testConfigTraceID, "--yes"}, extra...)
		if code, _, stderr := runFlagsCommand(t, server.URL, args...); code != 0 {
			t.Fatalf("ramp %v exit = %d, stderr=%s", extra, code, stderr)
		}
		return decodeFlagValue(t, write.Value).Rules[0]
	}
	defined := run("--stages", "10,50,100", "--interval", "15m", "--max-5xx-percent", "0.5", "--on-breach", "rollback")
	if r := defined.Ramp; r == nil || strings.Trim(fmt.Sprint(r.Stages), "[]") != "1000 5000 10000" || r.IntervalSeconds != 900 || r.Max5xxBasisPoints != 50 || r.OnBreach != "rollback" || r.Paused {
		t.Fatalf("defined ramp = %+v", defined.Ramp)
	}
	if resumed := run("--resume"); resumed.Ramp == nil || resumed.Ramp.Paused || resumed.Ramp.PausedReason != "" || len(resumed.Ramp.Stages) != 2 {
		t.Fatalf("resumed ramp = %+v", resumed.Ramp)
	}
	if removed := run("--remove"); removed.Ramp != nil || *removed.Rollout != 1000 {
		t.Fatalf("removed ramp = %+v rollout %d", removed.Ramp, *removed.Rollout)
	}
	if _, err := parseRampOptions("10", time.Minute, 1, 0, "pause", false, false); err == nil {
		t.Fatal("one stage must be refused")
	}
}

func TestFlagsSelfServeKeepsUnchangedSettings(t *testing.T) {
	current := api.PlatformFlagDefinition{Enabled: true, SelfServe: &api.PlatformFlagSelfServe{Plans: []string{"pro"}, MaxSubjects: 50}}
	for _, tc := range []struct {
		name  string
		extra []string
		want  platformflags.SelfServe
	}{
		{"open keeps plans and cap", nil, platformflags.SelfServe{Enabled: true, Plans: []string{"pro"}, MaxSubjects: 50}},
		{"every plan, platform cap", []string{"--plans", "all", "--max-subjects", "0"}, platformflags.SelfServe{Enabled: true}},
		{"new plans", []string{"--plans", "pro, scale"}, platformflags.SelfServe{Enabled: true, Plans: []string{"pro", "scale"}, MaxSubjects: 50}},
	} {
		var write operatorConfigPatchRequest
		server := flagsServer(t, testFlagsList(current, "operator", 3), "/v1/admin/config/platform_flag.ai_gateway", &write)
		args := append([]string{"self-serve", "--key", platformflags.AIGateway, "--enabled", "true", "--reason", "open preview", "--trace-id", testConfigTraceID, "--yes"}, tc.extra...)
		code, _, stderr := runFlagsCommand(t, server.URL, args...)
		server.Close()
		if code != 0 {
			t.Fatalf("%s: exit = %d, stderr=%s", tc.name, code, stderr)
		}
		got := decodeFlagValue(t, write.Value).SelfServe
		if got == nil || got.Enabled != tc.want.Enabled || got.MaxSubjects != tc.want.MaxSubjects || strings.Join(got.Plans, ",") != strings.Join(tc.want.Plans, ",") {
			t.Fatalf("%s: self_serve = %+v, want %+v", tc.name, got, tc.want)
		}
		if write.ExpectedVersion == nil || *write.ExpectedVersion != 3 {
			t.Fatalf("%s: the write must expect the current version", tc.name)
		}
	}
}

func TestFlagsFirstWriteCreatesTheRowAndRolloutTargetsPlans(t *testing.T) {
	var enable operatorConfigPatchRequest
	server := flagsServer(t, testFlagsList(api.PlatformFlagDefinition{}, "default_or_environment", 0), "/v1/admin/config/platform_flag.ai_gateway", &enable)
	code, _, stderr := runFlagsCommand(t, server.URL, "enable", "--key", platformflags.AIGateway, "--reason", "open preview", "--trace-id", testConfigTraceID, "--yes")
	server.Close()
	if code != 0 {
		t.Fatalf("enable exit = %d, stderr=%s", code, stderr)
	}
	if enable.ExpectedVersion == nil || *enable.ExpectedVersion != 0 || !decodeFlagValue(t, enable.Value).Enabled {
		t.Fatalf("first write must expect version 0 and enable the flag: %+v", enable)
	}

	var rollout operatorConfigPatchRequest
	server = flagsServer(t, testFlagsList(api.PlatformFlagDefinition{Enabled: true}, "operator", 1), "/v1/admin/config/platform_flag.ai_gateway", &rollout)
	code, _, stderr = runFlagsCommand(t, server.URL, "rollout", "--key", platformflags.AIGateway, "--percent", "25", "--plans", "pro,scale",
		"--reason", "widen preview", "--trace-id", testConfigTraceID, "--yes")
	server.Close()
	if code != 0 {
		t.Fatalf("rollout exit = %d, stderr=%s", code, stderr)
	}
	def := decodeFlagValue(t, rollout.Value)
	if len(def.Rules) != 1 || *def.Rules[0].Rollout != 2500 || strings.Join(def.Rules[0].Plans, ",") != "pro,scale" {
		t.Fatalf("rollout rules = %+v", def.Rules)
	}
}

func TestFlagsGroupAddWritesTheGroupsRow(t *testing.T) {
	var write operatorConfigPatchRequest
	server := flagsServer(t, testFlagsList(api.PlatformFlagDefinition{Enabled: true}, "operator", 1), "/v1/admin/config/platform_flag_groups", &write)
	defer server.Close()
	code, _, stderr := runFlagsCommand(t, server.URL, "group-add", "--group", "partners", "--account-id", strings.ToUpper(testFlagsAccount),
		"--reason", "new partner", "--trace-id", testConfigTraceID, "--yes")
	if code != 0 {
		t.Fatalf("group-add exit = %d, stderr=%s", code, stderr)
	}
	groups, err := platformflags.ParseGroups(write.Value)
	if err != nil {
		t.Fatal(err)
	}
	if write.ExpectedVersion == nil || *write.ExpectedVersion != 2 || len(groups["partners"]) != 1 || groups["partners"][0] != testFlagsAccount || len(groups["internal"]) != 1 {
		t.Fatalf("groups write = %+v (%s)", write, write.Value)
	}
}

func TestFlagsReadCommands(t *testing.T) {
	value := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/admin/flags":
			writeTestJSON(w, http.StatusOK, testFlagsList(api.PlatformFlagDefinition{Enabled: true}, "operator", 3))
		case "/v1/admin/flags/ai_gateway/decision":
			if r.URL.Query().Get("account_id") != testFlagsAccount {
				t.Errorf("decision query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.OperatorPlatformFlagDecision{Flag: "ai_gateway", AccountID: testFlagsAccount, Plan: "pro", Enabled: true, Reason: "rule_match", RuleID: "accounts", Entitled: &value, Available: true, Version: 3})
		case "/v1/admin/accounts/" + testFlagsAccount + "/flags":
			writeTestJSON(w, http.StatusOK, api.OperatorAccountPlatformFlags{AccountID: testFlagsAccount, Plan: "free", Items: []api.OperatorPlatformFlagDecision{{Flag: "ai_gateway", AccountID: testFlagsAccount, Plan: "free", Reason: "default"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list"}, "key=ai_gateway enabled=true"},
		{[]string{"list"}, "pending=outboundd"},
		{[]string{"groups"}, "group=internal accounts=" + testFlagsOther},
		{[]string{"inspect", "--key", "ai_gateway", "--account-id", testFlagsAccount}, "reason=rule_match rule=accounts entitled=true available=true"},
		{[]string{"account", "--account-id", testFlagsAccount}, "plan=free enabled=false reason=default"},
	} {
		code, out, stderr := runFlagsCommand(t, server.URL, tc.args...)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Errorf("%v: exit=%d stdout=%q stderr=%q, want %q", tc.args, code, out, stderr, tc.want)
		}
	}
}

func TestFlagsMutationsRefuseBadInputBeforeAnyRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"allow", "--key", "ai_gateway", "--account-id", testFlagsAccount, "--yes"},
		{"allow", "--key", "ai_gateway", "--account-id", testFlagsAccount, "--reason", "no confirmation"},
		{"rollout", "--key", "ai_gateway", "--percent", "120", "--reason", "too wide", "--yes"},
		{"default", "--key", "ai_gateway", "--value", "maybe", "--reason", "bad value", "--yes"},
		{"show", "--key", "not_a_flag"},
		{"inspect", "--key", "ai_gateway"},
		{"history", "--key", "ai_gateway", "--limit", "0"},
		{"bogus"},
	} {
		if code, _, _ := runFlagsCommand(t, server.URL, args...); code == 0 {
			t.Errorf("%v succeeded", args)
		}
	}
}

// adr: 20261023115741 — --force confirms turning a core capability flag off.
func TestFlagsDisableSendsForceOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--force"}, true},
	} {
		var captured operatorConfigPatchRequest
		server := flagsServer(t, testFlagsList(api.PlatformFlagDefinition{Enabled: true, Default: true}, "operator", 3), "/v1/admin/config/platform_flag.ai_gateway", &captured)
		args := append([]string{"disable", "--key", platformflags.AIGateway, "--reason", "incident", "--trace-id", testConfigTraceID, "--yes"}, tc.args...)
		code, _, stderr := runFlagsCommand(t, server.URL, args...)
		server.Close()
		if code != 0 {
			t.Fatalf("disable %v exit = %d, stderr=%s", tc.args, code, stderr)
		}
		if captured.Force != tc.want {
			t.Fatalf("disable %v sent force=%v, want %v", tc.args, captured.Force, tc.want)
		}
	}
}

// adr: 20261023115741 — bulk selectors write every matching capability flag.
func bulkFlagsList() api.OperatorPlatformFlagList {
	item := func(key, capability, parent, maturity string) api.OperatorPlatformFlag {
		return api.OperatorPlatformFlag{Key: key, ConfigKey: platformflags.ConfigKey(key), Capability: capability, Parent: parent, Maturity: maturity,
			Definition: api.PlatformFlagDefinition{Enabled: true}, Source: "default_or_environment"}
	}
	return api.OperatorPlatformFlagList{Items: []api.OperatorPlatformFlag{
		item("realtime", "realtime", "", "preview"),
		item("realtime_channels", "realtime-channels", "realtime", "preview"),
		item("realtime_push", "realtime-push", "realtime", "preview"),
		item("crons", "crons", "", "ga"),
		item("route_insights", "route-insights", "", "internal"),
	}}
}

func bulkFlagsServer(t *testing.T, list api.OperatorPlatformFlagList, writes map[string]operatorConfigPatchRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/flags":
			writeTestJSON(w, http.StatusOK, list)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/admin/config/"):
			var req operatorConfigPatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			key := strings.TrimPrefix(r.URL.Path, "/v1/admin/config/")
			writes[key] = req
			writeTestJSON(w, http.StatusOK, api.OperatorRuntimeConfig{Key: key, Version: 1, Status: "applied", EffectiveValue: req.Value})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestFlagsBulkAllowByTierAndCapability(t *testing.T) {
	tests := []struct {
		name     string
		selector []string
		want     []string
	}{
		{"tier", []string{"--tier", "preview"}, []string{"realtime", "realtime_channels", "realtime_push"}},
		{"capability subtree", []string{"--capability", "realtime"}, []string{"realtime", "realtime_channels", "realtime_push"}},
		{"leaf capability", []string{"--capability", "realtime-push"}, []string{"realtime_push"}},
		{"internal tier", []string{"--tier", "internal"}, []string{"route_insights"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writes := map[string]operatorConfigPatchRequest{}
			server := bulkFlagsServer(t, bulkFlagsList(), writes)
			args := append([]string{"allow", "--account-id", testFlagsAccount, "--reason", "design partner", "--trace-id", testConfigTraceID, "--yes"}, tc.selector...)
			code, stdout, stderr := runFlagsCommand(t, server.URL, args...)
			server.Close()
			if code != 0 {
				t.Fatalf("exit = %d, stderr=%s", code, stderr)
			}
			if len(writes) != len(tc.want) {
				t.Fatalf("wrote %d flags %v, want %v", len(writes), writes, tc.want)
			}
			for _, key := range tc.want {
				req, ok := writes[platformflags.ConfigKey(key)]
				if !ok {
					t.Fatalf("flag %s was not written", key)
				}
				def := decodeFlagValue(t, req.Value)
				if len(def.Rules) == 0 || !slicesContain(def.Rules[0].Accounts, testFlagsAccount) {
					t.Fatalf("flag %s rules = %+v, want the account allowed", key, def.Rules)
				}
			}
			if !strings.Contains(stdout, fmt.Sprintf("flags=%d written=%d failed=0", len(tc.want), len(tc.want))) {
				t.Fatalf("summary missing: %s", stdout)
			}
		})
	}
}

func TestFlagsBulkSelectorsAreExclusive(t *testing.T) {
	code, _, stderr := runFlagsCommand(t, "http://127.0.0.1:0", "enable", "--key", "crons", "--tier", "ga", "--reason", "x y z", "--trace-id", testConfigTraceID, "--yes")
	if code != 2 || !strings.Contains(stderr, "exactly one of --key, --tier or --capability") {
		t.Fatalf("exit = %d stderr=%s, want a usage error", code, stderr)
	}
}

func slicesContain(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
