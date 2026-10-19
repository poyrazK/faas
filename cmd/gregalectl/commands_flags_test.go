package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
