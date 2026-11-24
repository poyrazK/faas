package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/flags"
)

// fakeFlagsAPI keeps one environment's configuration and enforces
// expected_version like apid. conflictOnce makes the next PUT answer 409
// after a concurrent writer bumped the version.
type fakeFlagsAPI struct {
	mu           sync.Mutex
	version      int64
	config       flags.Config
	puts         int
	conflictOnce bool
}

func (f *fakeFlagsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/v1/projects/exports/environments/production/flags" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var body struct {
			ExpectedVersion int64        `json:"expected_version"`
			Config          flags.Config `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.puts++
		if f.conflictOnce {
			f.conflictOnce = false
			f.version++
		}
		if body.ExpectedVersion != f.version {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"type":"about:blank","status":409,"code":"flags_version_conflict","title":"Configuration changed","detail":"refresh"}`))
			return
		}
		if err := flags.Validate(body.Config); err != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"about:blank","status":400,"code":"validation_error","title":"Invalid","detail":` + jsonString(err.Error()) + `}`))
			return
		}
		f.version++
		f.config = body.Config
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	cfg := f.config
	if cfg.Flags == nil {
		cfg.Flags = []flags.Flag{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"version": f.version, "flags": cfg.Flags, "groups": cfg.Groups})
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func (f *fakeFlagsAPI) flag(t *testing.T, key string) flags.Flag {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fl := range f.config.Flags {
		if fl.Key == key {
			return fl
		}
	}
	t.Fatalf("flag %s missing from %+v", key, f.config.Flags)
	return flags.Flag{}
}

func runFlagsCLI(t *testing.T, api *fakeFlagsAPI, args ...string) (int, string) {
	t.Helper()
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var out bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	code := main.CmdFlags(append(args, "--project", "exports"))
	return code, out.String()
}

func TestFlagsEditCommandsBooleanLifecycle(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, out := runFlagsCLI(t, api, "create", "new-export", "--description", "new pipeline"); code != 0 || !strings.Contains(out, "Created new-export") {
		t.Fatalf("create: %d %q", code, out)
	}
	if f := api.flag(t, "new-export"); !f.Enabled || f.Default != false || f.Description != "new pipeline" {
		t.Fatalf("created %+v", f)
	}
	if code, _ := runFlagsCLI(t, api, "create", "new-export"); code == 0 {
		t.Fatal("duplicate create accepted")
	}
	if code, out := runFlagsCLI(t, api, "percent", "new-export", "12.5%"); code != 0 || !strings.Contains(out, "12.5% of customers") {
		t.Fatalf("percent: %d %q", code, out)
	}
	f := api.flag(t, "new-export")
	if len(f.Rules) != 1 || f.Rules[0].ID != "everyone-percent" || *f.Rules[0].Rollout != 1250 || f.Rules[0].Value != true {
		t.Fatalf("percent rule %+v", f.Rules)
	}
	// A targeted rule goes before the catch-all percentage.
	if code, out := runFlagsCLI(t, api, "rule-add", "new-export", "--id", "pilot", "--customer", "11111111-1111-4111-8111-111111111111"); code != 0 || !strings.Contains(out, "position 1") {
		t.Fatalf("rule-add: %d %q", code, out)
	}
	f = api.flag(t, "new-export")
	if len(f.Rules) != 2 || f.Rules[0].ID != "pilot" || f.Rules[0].Value != true || f.Rules[1].ID != "everyone-percent" {
		t.Fatalf("rules %+v", f.Rules)
	}
	// Changing the percentage keeps it last.
	if code, _ := runFlagsCLI(t, api, "percent", "new-export", "50"); code != 0 {
		t.Fatal("percent 50")
	}
	if f = api.flag(t, "new-export"); f.Rules[1].ID != "everyone-percent" || *f.Rules[1].Rollout != 5000 {
		t.Fatalf("rules %+v", f.Rules)
	}
	if code, _ := runFlagsCLI(t, api, "set", "new-export", "--default", "true"); code != 0 || api.flag(t, "new-export").Default != true {
		t.Fatal("set default true")
	}
	// off on a boolean flag also turns its default off.
	if code, out := runFlagsCLI(t, api, "off", "new-export"); code != 0 || !strings.Contains(out, "default to false") {
		t.Fatalf("off: %d %q", code, out)
	}
	if f = api.flag(t, "new-export"); f.Enabled || f.Default != false {
		t.Fatalf("off left %+v", f)
	}
	if code, _ := runFlagsCLI(t, api, "on", "new-export"); code != 0 || !api.flag(t, "new-export").Enabled {
		t.Fatal("on")
	}
	if code, _ := runFlagsCLI(t, api, "rule-rm", "new-export", "--id", "pilot"); code != 0 || len(api.flag(t, "new-export").Rules) != 1 {
		t.Fatal("rule-rm")
	}
	if code, _ := runFlagsCLI(t, api, "percent", "new-export", "0"); code != 0 || len(api.flag(t, "new-export").Rules) != 0 {
		t.Fatal("percent 0 should remove the rule")
	}
	if code, out := runFlagsCLI(t, api, "list"); code != 0 || !strings.Contains(out, "new-export") || !strings.Contains(out, "boolean") {
		t.Fatalf("list: %d %q", code, out)
	}
	if code, _ := runFlagsCLI(t, api, "rm", "new-export"); code != 0 || len(api.config.Flags) != 0 {
		t.Fatal("rm")
	}
}

func TestFlagsEditCommandsVariants(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, _ := runFlagsCLI(t, api, "create", "pipeline", "--variants", "legacy,new,next", "--default", "new"); code != 0 {
		t.Fatal("create variants")
	}
	f := api.flag(t, "pipeline")
	if f.Type != "variant" || f.Default != "new" || len(f.Variants) != 3 || f.Variants[0].Weight != 3334 || f.Variants[1].Weight != 3333 {
		t.Fatalf("variant flag %+v", f)
	}
	if code, _ := runFlagsCLI(t, api, "set", "pipeline", "--default", "nope"); code == 0 {
		t.Fatal("unknown variant default accepted")
	}
	if code, _ := runFlagsCLI(t, api, "percent", "pipeline", "10"); code != 0 {
		t.Fatal("variant percent")
	}
	if f = api.flag(t, "pipeline"); f.Rules[0].Value != nil {
		t.Fatalf("variant percentage rule should use weights, got %+v", f.Rules[0])
	}
	if code, _ := runFlagsCLI(t, api, "rule-add", "pipeline", "--id", "pinned", "--percent", "5", "--value", "next"); code != 0 {
		t.Fatal("variant rule-add")
	}
	if f = api.flag(t, "pipeline"); f.Rules[0].ID != "pinned" || f.Rules[0].Value != "next" {
		t.Fatalf("rules %+v", f.Rules)
	}
	// off on a variant flag leaves its default alone.
	if code, _ := runFlagsCLI(t, api, "off", "pipeline"); code != 0 || api.flag(t, "pipeline").Default != "new" {
		t.Fatal("variant off")
	}
}

func TestFlagsRuleAddAttributeConditions(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, _ := runFlagsCLI(t, api, "create", "eu"); code != 0 {
		t.Fatal("create")
	}
	if code, out := runFlagsCLI(t, api, "rule-add", "eu", "--id", "eu-pro", "--attr", "country=DE,FR", "--attr", "plan!=free", "--attr", "email$=@example.com"); code != 0 {
		t.Fatalf("rule-add: %d %q", code, out)
	}
	r := api.flag(t, "eu").Rules[0]
	want := []flags.Condition{
		{Attribute: "gregale.country", Op: "in", Values: []string{"DE", "FR"}},
		{Attribute: "app.plan", Op: "not_in", Values: []string{"free"}},
		{Attribute: "app.email", Op: "ends_with", Values: []string{"@example.com"}},
	}
	if len(r.Attributes) != len(want) {
		t.Fatalf("conditions %+v", r.Attributes)
	}
	for i, c := range want {
		got := r.Attributes[i]
		if got.Attribute != c.Attribute || got.Op != c.Op || strings.Join(got.Values, ",") != strings.Join(c.Values, ",") {
			t.Errorf("condition %d = %+v, want %+v", i, got, c)
		}
	}
	for _, bad := range []string{"country", "=DE", "Plan=pro", "country=de"} {
		if code, _ := runFlagsCLI(t, api, "rule-add", "eu", "--id", "bad", "--attr", bad); code == 0 {
			t.Errorf("--attr %q accepted", bad)
		}
	}
}

// ADR-20261124212701: edge routes are added per path and variant, listed,
// and removed one path or all at once.
func TestFlagsEdgeSetListDelete(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, _ := runFlagsCLI(t, api, "create", "pricing", "--variants", "a,b"); code != 0 {
		t.Fatal("create")
	}
	if code, out := runFlagsCLI(t, api, "edge-set", "pricing", "--path", "/pricing", "--variant", "b", "--rewrite", "/pricing-b"); code != 0 || !strings.Contains(out, "variant b serves /pricing-b") {
		t.Fatalf("edge-set: %d %q", code, out)
	}
	if code, _ := runFlagsCLI(t, api, "edge-set", "pricing", "--path", "/docs/*", "--variant", "b", "--rewrite", "/docs-b"); code != 0 {
		t.Fatal("edge-set docs")
	}
	if edge := api.flag(t, "pricing").Edge; edge == nil || len(edge.Routes) != 2 || edge.Routes[0].Variants["b"].Rewrite != "/pricing-b" {
		t.Fatalf("edge routes = %+v", edge)
	}
	if code, out := runFlagsCLI(t, api, "edge-list"); code != 0 || !strings.Contains(out, "/docs/*") || !strings.Contains(out, "/pricing-b") {
		t.Fatalf("edge-list: %d %q", code, out)
	}
	if code, _ := runFlagsCLI(t, api, "edge-set", "pricing", "--path", "/x", "--variant", "c", "--rewrite", "/y"); code == 0 {
		t.Fatal("routed an unknown variant")
	}
	if code, _ := runFlagsCLI(t, api, "edge-set", "pricing", "--path", "/x", "--variant", "b"); code == 0 {
		t.Fatal("edge-set without --rewrite")
	}
	if code, _ := runFlagsCLI(t, api, "edge-rm", "pricing", "--path", "/pricing"); code != 0 || len(api.flag(t, "pricing").Edge.Routes) != 1 {
		t.Fatal("edge-rm one path")
	}
	if code, _ := runFlagsCLI(t, api, "edge-rm", "pricing"); code != 0 || api.flag(t, "pricing").Edge != nil {
		t.Fatal("edge-rm every path")
	}
}

func TestFlagsKillAndUnkill(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, _ := runFlagsCLI(t, api, "create", "risky", "--default", "true"); code != 0 {
		t.Fatal("create")
	}
	if code, out := runFlagsCLI(t, api, "kill", "risky"); code != 0 || !strings.Contains(out, "Killed risky") || !api.flag(t, "risky").Killed {
		t.Fatalf("kill: %d %q", code, out)
	}
	if code, out := runFlagsCLI(t, api, "list"); code != 0 || !strings.Contains(out, "killed") {
		t.Fatalf("list: %q", out)
	}
	if code, _ := runFlagsCLI(t, api, "unkill", "risky"); code != 0 || api.flag(t, "risky").Killed {
		t.Fatal("unkill")
	}
	if code, _ := runFlagsCLI(t, api, "kill", "missing"); code == 0 {
		t.Fatal("killed a missing flag")
	}
}

func TestFlagsOwnerAndExpiry(t *testing.T) {
	api := &fakeFlagsAPI{}
	if code, out := runFlagsCLI(t, api, "create", "launch", "--owner", "growth", "--expires", "2027-01-31"); code != 0 || !strings.Contains(out, "owner growth") {
		t.Fatalf("create: %d %q", code, out)
	}
	if f := api.flag(t, "launch"); f.Owner != "growth" || f.ExpiresAt != "2027-01-31" {
		t.Fatalf("created %+v", f)
	}
	if code, _ := runFlagsCLI(t, api, "set", "launch", "--expires", "never"); code != 0 || api.flag(t, "launch").ExpiresAt != "" {
		t.Fatal("expires never did not clear")
	}
	if code, _ := runFlagsCLI(t, api, "set", "launch", "--expires", "31/01/2027"); code == 0 {
		t.Fatal("bad date accepted")
	}
	if code, out := runFlagsCLI(t, api, "list"); code != 0 || !strings.Contains(out, "growth") {
		t.Fatalf("list: %q", out)
	}
}

// The tree names (gregale flag rollout set, flag kill set, ...) reach the
// same commands as the short verbs.
func TestFlagsTreeNamesDispatch(t *testing.T) {
	api := &fakeFlagsAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var out bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	for _, args := range [][]string{
		{"flag", "create", "tree"},
		{"flag", "rollout", "set", "tree", "25"},
		{"flag", "rule", "create", "tree", "--id", "eu", "--attr", "country=DE"},
		{"flag", "kill", "set", "tree"},
		{"flag", "disable", "tree"},
	} {
		if code := main.Run(append(args, "--project", "exports")); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, out.String())
		}
	}
	f := api.flag(t, "tree")
	if f.Enabled || !f.Killed || len(f.Rules) != 2 || f.Rules[0].ID != "eu" || *f.Rules[1].Rollout != 2500 {
		t.Fatalf("flag after tree commands = %+v", f)
	}
}

func TestFlagsEditRetriesAfterVersionConflict(t *testing.T) {
	api := &fakeFlagsAPI{conflictOnce: true}
	if code, _ := runFlagsCLI(t, api, "create", "retry-me"); code != 0 {
		t.Fatal("create after one conflict")
	}
	if api.puts != 2 || api.version != 2 {
		t.Fatalf("puts=%d version=%d", api.puts, api.version)
	}
}

func TestFlagsEditValidation(t *testing.T) {
	api := &fakeFlagsAPI{}
	for _, args := range [][]string{
		{"create"},
		{"create", "Bad-Key"},
		{"percent", "x", "101"},
		{"percent", "x", "1.234"},
		{"set", "x"},
		{"on", "missing"},
		{"rule-add", "x"},
		{"rule-rm", "x"},
	} {
		if code, _ := runFlagsCLI(t, api, args...); code == 0 {
			t.Errorf("%v accepted", args)
		}
	}
	if api.puts != 0 {
		t.Fatalf("invalid commands published %d times", api.puts)
	}
}
