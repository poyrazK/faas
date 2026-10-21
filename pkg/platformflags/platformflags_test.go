package platformflags

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/flags"
)

// adr: 1700 — platform flags: per-account release gates with a kill switch.

const (
	acctA = "11111111-1111-4111-8111-111111111111"
	acctB = "22222222-2222-4222-8222-222222222222"
	acctC = "33333333-3333-4333-8333-333333333333"
)

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func compileOne(def Definition, groups Groups) *Snapshot {
	return Compile(map[string]Definition{AIGateway: def}, map[string]int64{AIGateway: 7}, groups, 3)
}

func TestEvaluateRulesKillSwitchAndDefault(t *testing.T) {
	def := Definition{Enabled: true, Default: false, Rules: []Rule{
		{ID: "deny-b", Accounts: []string{acctB}, Value: boolPtr(false)},
		{ID: "internal", Group: "internal", Value: boolPtr(true)},
		{ID: "pro-only", Accounts: []string{acctC}, Plans: []string{"pro"}, Value: boolPtr(true)},
	}}
	groups := Groups{"internal": {acctA, acctB}}
	snap := compileOne(def, groups)
	cases := []struct {
		name    string
		subject Subject
		enabled bool
		reason  string
		rule    string
	}{
		{"group member", Subject{AccountID: acctA}, true, ReasonRuleMatch, "internal"},
		{"first match wins over a later group", Subject{AccountID: acctB}, false, ReasonRuleMatch, "deny-b"},
		{"plan matches", Subject{AccountID: acctC, Plan: "pro"}, true, ReasonRuleMatch, "pro-only"},
		{"plan mismatch falls to default", Subject{AccountID: acctC, Plan: "hobby"}, false, ReasonDefault, ""},
		{"unknown plan never matches a plan rule", Subject{AccountID: acctC}, false, ReasonDefault, ""},
		{"no account", Subject{}, false, ReasonAccountMissing, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := snap.Evaluate(AIGateway, tc.subject)
			if d.Enabled != tc.enabled || d.Reason != tc.reason || d.RuleID != tc.rule || d.Version != 7 {
				t.Fatalf("decision = %+v, want enabled=%v reason=%s rule=%s version=7", d, tc.enabled, tc.reason, tc.rule)
			}
		})
	}

	def.Enabled = false
	def.Default = true
	if d := compileOne(def, groups).Evaluate(AIGateway, Subject{AccountID: acctA}); d.Enabled || d.Reason != ReasonFlagDisabled {
		t.Fatalf("kill switch must override rules and default: %+v", d)
	}
	if d := snap.Evaluate("missing", Subject{AccountID: acctA}); d.Enabled || d.Reason != ReasonUnknownFlag {
		t.Fatalf("unknown flag must be off: %+v", d)
	}
	var nilSnap *Snapshot
	if nilSnap.Enabled(AIGateway, Subject{AccountID: acctA}) || nilSnap.GloballyEnabled(AIGateway) {
		t.Fatal("a nil snapshot must evaluate every flag as off")
	}
}

// Rules store canonical UUIDs; callers may hold another accepted spelling
// (uppercase, or the 32-hex form some stores use) and must still match.
func TestEvaluateCanonicalizesTheAccount(t *testing.T) {
	snap := compileOne(Definition{Enabled: true, Rules: []Rule{{ID: "one", Accounts: []string{acctA}, Value: boolPtr(true)}}}, nil)
	for _, spelling := range []string{acctA, strings.ToUpper(acctA), strings.ReplaceAll(acctA, "-", ""), " " + acctA + " "} {
		if !snap.Enabled(AIGateway, Subject{AccountID: spelling}) {
			t.Errorf("account spelled %q did not match its rule", spelling)
		}
	}
	if snap.Enabled(AIGateway, Subject{AccountID: "acme"}) {
		t.Fatal("a non-UUID account matched")
	}
}

func TestDanglingGroupFailsClosed(t *testing.T) {
	def := Definition{Enabled: true, Rules: []Rule{{ID: "partners", Group: "partners", Value: boolPtr(true)}}}
	if compileOne(def, Groups{}).Enabled(AIGateway, Subject{AccountID: acctA}) {
		t.Fatal("a rule naming a missing group must not match")
	}
}

// The rollout bucket is the customer-Flags hash with the platform seed, so a
// cohort is stable and widening the percentage keeps every earlier account.
func TestRolloutIsStableAndMonotonic(t *testing.T) {
	accounts := make([]string, 2000)
	for i := range accounts {
		accounts[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(i), byte(i >> 8)}).String()
	}
	enabledAt := func(bp int) map[string]bool {
		snap := compileOne(Definition{Enabled: true, Rules: []Rule{{ID: "pct", Rollout: intPtr(bp), Value: boolPtr(true)}}}, nil)
		out := map[string]bool{}
		for _, a := range accounts {
			d := snap.Evaluate(AIGateway, Subject{AccountID: a})
			if d.Enabled {
				out[a] = true
				if d.Bucket == nil || *d.Bucket != flags.Bucket(bucketSeed, AIGateway, a) {
					t.Fatalf("bucket for %s = %v, want the shared Flags hash", a, d.Bucket)
				}
			}
		}
		return out
	}
	ten, fifty := enabledAt(1000), enabledAt(5000)
	for a := range ten {
		if !fifty[a] {
			t.Fatalf("account %s left the cohort when the rollout widened", a)
		}
	}
	if n := len(ten); n < 140 || n > 260 {
		t.Fatalf("10%% rollout admitted %d of 2000 accounts", n)
	}
	if len(enabledAt(0)) != 0 || len(enabledAt(10000)) != len(accounts) {
		t.Fatal("0 and 10000 basis points must admit none and all")
	}
}

func TestParseDefinitionValidates(t *testing.T) {
	valid := `{"enabled":true,"default":false,"rules":[{"id":"r1","accounts":["` + strings.ToUpper(acctA) + `"],"value":true}]}`
	def, err := ParseDefinition(json.RawMessage(valid))
	if err != nil {
		t.Fatal(err)
	}
	if def.Rules[0].Accounts[0] != acctA {
		t.Fatalf("account not canonicalized: %q", def.Rules[0].Accounts[0])
	}
	for name, raw := range map[string]string{
		"unknown field":        `{"enabled":true,"rules":[{"id":"r1","acounts":["` + acctA + `"],"value":true}]}`,
		"missing value":        `{"enabled":true,"rules":[{"id":"r1","accounts":["` + acctA + `"]}]}`,
		"no targeting":         `{"enabled":true,"rules":[{"id":"r1","value":true}]}`,
		"bad account":          `{"enabled":true,"rules":[{"id":"r1","accounts":["acme"],"value":true}]}`,
		"duplicate account":    `{"enabled":true,"rules":[{"id":"r1","accounts":["` + acctA + `","` + acctA + `"],"value":true}]}`,
		"duplicate rule id":    `{"enabled":true,"rules":[{"id":"r1","rollout":1,"value":true},{"id":"r1","rollout":2,"value":true}]}`,
		"bad rule id":          `{"enabled":true,"rules":[{"id":"R 1","rollout":1,"value":true}]}`,
		"bad plan":             `{"enabled":true,"rules":[{"id":"r1","plans":["gold"],"value":true}]}`,
		"rollout out of range": `{"enabled":true,"rules":[{"id":"r1","rollout":10001,"value":true}]}`,
		"not an object":        `true`,
		"trailing data":        `{"enabled":true} {}`,
	} {
		if _, err := ParseDefinition(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted %s", name, raw)
		}
	}
	withGroup := Definition{Enabled: true, Rules: []Rule{{ID: "g", Group: "partners", Value: boolPtr(true)}}}
	if err := ValidateDefinition(withGroup, Groups{}); err == nil {
		t.Fatal("a rule referencing an unknown group must be rejected when groups are known")
	}
	if err := ValidateDefinition(withGroup, nil); err != nil {
		t.Fatalf("nil groups skip the cross-reference check: %v", err)
	}
}

func TestParseGroupsAndReferences(t *testing.T) {
	groups, err := ParseGroups(json.RawMessage(`{"internal":["` + acctA + `"],"partners":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(groups["internal"]) != 1 || len(groups["partners"]) != 0 {
		t.Fatalf("groups = %+v", groups)
	}
	if _, err := ParseGroups(json.RawMessage(`{"Bad Name":[]}`)); err == nil {
		t.Fatal("invalid group name accepted")
	}
	refs := GroupReferences(map[string]Definition{
		AIGateway:     {Rules: []Rule{{ID: "a", Group: "internal"}}},
		CustomerFlags: {Rules: []Rule{{ID: "b", Group: "internal"}, {ID: "c", Group: "partners"}}},
	})
	if strings.Join(refs["internal"], ",") != AIGateway+","+CustomerFlags || len(refs["partners"]) != 1 {
		t.Fatalf("references = %+v", refs)
	}
}

func TestSetAppliesInVersionOrder(t *testing.T) {
	set := NewSet(map[string]Definition{AIGateway: FromAccountList(true, true, nil)})
	if !set.Enabled(AIGateway, Subject{AccountID: acctA}) {
		t.Fatal("bootstrap definition not installed")
	}
	off := Definition{Enabled: false}.Marshal()
	if err := set.Apply(ConfigKey(AIGateway), off, 4); err != nil {
		t.Fatal(err)
	}
	if set.Enabled(AIGateway, Subject{AccountID: acctA}) {
		t.Fatal("durable value did not replace the bootstrap value")
	}
	stale := FromAccountList(true, true, nil).Marshal()
	for _, version := range []int64{0, 3} {
		if err := set.Apply(ConfigKey(AIGateway), stale, version); err != nil {
			t.Fatal(err)
		}
		if set.Enabled(AIGateway, Subject{AccountID: acctA}) {
			t.Fatalf("version %d rolled back a newer durable value", version)
		}
	}
	if err := set.Apply(ConfigKey(AIGateway), json.RawMessage(`{"enabled":"yes"}`), 9); err == nil {
		t.Fatal("malformed value accepted")
	}
	if err := set.Apply(ConfigKey("not_catalogued"), off, 9); err == nil {
		t.Fatal("uncatalogued flag accepted")
	}
	if err := set.Apply("hsts_enabled", json.RawMessage(`true`), 9); err != nil {
		t.Fatalf("non-flag keys must be ignored: %v", err)
	}

	group := Definition{Enabled: true, Rules: []Rule{{ID: "g", Group: "internal", Value: boolPtr(true)}}}
	if err := set.Apply(ConfigKey(AIGateway), group.Marshal(), 5); err != nil {
		t.Fatal(err)
	}
	if err := set.Apply(GroupsConfigKey, Groups{"internal": {acctB}}.Marshal(), 2); err != nil {
		t.Fatal(err)
	}
	if !set.Enabled(AIGateway, Subject{AccountID: acctB}) || set.Enabled(AIGateway, Subject{AccountID: acctA}) {
		t.Fatal("group membership not applied")
	}
	if groups, version := set.Snapshot().Groups(); version != 2 || len(groups["internal"]) != 1 {
		t.Fatalf("groups snapshot = %v v%d", groups, version)
	}
	var nilSet *Set
	if nilSet.Enabled(AIGateway, Subject{AccountID: acctA}) || nilSet.Apply(GroupsConfigKey, nil, 1) != nil {
		t.Fatal("a nil set must be inert")
	}
}

func TestFromAccountList(t *testing.T) {
	if d := FromAccountList(false, true, []string{acctA}); d.Enabled {
		t.Fatalf("gate off must disable: %+v", d)
	}
	all := Compile(map[string]Definition{AIGateway: FromAccountList(true, true, nil)}, nil, nil, 0)
	if !all.Enabled(AIGateway, Subject{AccountID: acctC}) {
		t.Fatal("all=true must admit every account")
	}
	listed := Compile(map[string]Definition{AIGateway: FromAccountList(true, false, []string{strings.ToUpper(acctA)})}, nil, nil, 0)
	if !listed.Enabled(AIGateway, Subject{AccountID: acctA}) || listed.Enabled(AIGateway, Subject{AccountID: acctB}) {
		t.Fatal("listed accounts only")
	}
	none := Compile(map[string]Definition{AIGateway: FromAccountList(true, false, nil)}, nil, nil, 0)
	if none.Enabled(AIGateway, Subject{AccountID: acctA}) {
		t.Fatal("an empty list admits no account")
	}
	if _, err := ParseDefinition(FromAccountList(true, false, []string{acctA}).Marshal()); err != nil {
		t.Fatalf("bootstrap definitions must validate: %v", err)
	}
}

// adr: 20261021080640 — company targeting: org rules match the organization a
// request acts under and fail closed without one.
func TestOrgRulesMatchTheRequestOrganization(t *testing.T) {
	const orgX, orgY = "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	snap := compileOne(Definition{Enabled: true, Rules: []Rule{
		{ID: "x-pro", Orgs: []string{orgX}, Plans: []string{"pro"}, Value: boolPtr(true)},
	}}, nil)
	cases := []struct {
		name    string
		subject Subject
		enabled bool
	}{
		{"member of x on pro", Subject{AccountID: acctA, Plan: "pro", OrgID: orgX}, true},
		{"org id in another spelling", Subject{AccountID: acctA, Plan: "pro", OrgID: strings.ToUpper(orgX)}, true},
		{"other organization", Subject{AccountID: acctA, Plan: "pro", OrgID: orgY}, false},
		{"no organization", Subject{AccountID: acctA, Plan: "pro"}, false},
		{"x but plan mismatch", Subject{AccountID: acctA, Plan: "hobby", OrgID: orgX}, false},
	}
	for _, tc := range cases {
		if got := snap.Enabled(AIGateway, tc.subject); got != tc.enabled {
			t.Errorf("%s: enabled = %v, want %v", tc.name, got, tc.enabled)
		}
	}
}

func TestOrgRulesValidateAndRespectTargetingSupport(t *testing.T) {
	const org = "44444444-4444-4444-8444-444444444444"
	def, err := ParseDefinition(json.RawMessage(`{"enabled":true,"rules":[{"id":"acme","orgs":["` + strings.ToUpper(org) + `"],"value":true}]}`))
	if err != nil {
		t.Fatalf("an org-only rule must be valid: %v", err)
	}
	if got := def.Rules[0].Orgs; len(got) != 1 || got[0] != org {
		t.Fatalf("orgs = %v, want the canonical UUID", got)
	}
	for name, raw := range map[string]string{
		"not a uuid": `{"enabled":true,"rules":[{"id":"acme","orgs":["acme-inc"],"value":true}]}`,
		"duplicate":  `{"enabled":true,"rules":[{"id":"acme","orgs":["` + org + `","` + org + `"],"value":true}]}`,
	} {
		if _, err := ParseDefinition(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	orgRule := Definition{Enabled: true, Rules: []Rule{{ID: "acme", Orgs: []string{org}, Value: boolPtr(true)}}}
	planRule := Definition{Enabled: true, Rules: []Rule{{ID: "pro", Plans: []string{"pro"}, Value: boolPtr(true)}}}
	for _, tc := range []struct {
		flag string
		def  Definition
		ok   bool
	}{
		{AIGateway, orgRule, false},      // outboundd does not know the organization
		{AIGateway, planRule, true},      // every consumer knows the plan
		{LiveQueries, planRule, false},   // the realtime path knows only the account
		{ApplicationAuth, orgRule, true}, // apid knows both
	} {
		spec, _ := Lookup(tc.flag)
		if err := ValidateTargeting(spec, tc.def); (err == nil) != tc.ok {
			t.Errorf("%s %+v: err = %v, want ok=%v", tc.flag, tc.def.Rules[0], err, tc.ok)
		}
	}
}

// adr: 20261021080640 — a rule outside its window does not match;
// evaluation falls through and the decision names the inactive rule.
func TestScheduledRulesApplyOnlyInsideTheirWindow(t *testing.T) {
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)
	def, err := ParseDefinition(json.RawMessage(`{"enabled":true,"rules":[
		{"id":"launch","accounts":["` + acctA + `"],"starts_at":"2026-11-02T12:00:00+03:00","ends_at":"2026-11-04T09:00:00Z","value":true},
		{"id":"everyone-later","starts_at":"2026-12-01T00:00:00Z","value":true}]}`))
	if err != nil {
		t.Fatalf("a windowed rule and a window-only rule must be valid: %v", err)
	}
	if got := *def.Rules[0].StartsAt; !got.Equal(start) || got.Location() != time.UTC {
		t.Fatalf("starts_at = %v, want %v in UTC", got, start)
	}
	snap := compileOne(def, nil)
	cases := []struct {
		name    string
		at      time.Time
		subject string
		enabled bool
		reason  string
		state   string
	}{
		{"before the window", start.Add(-time.Minute), acctA, false, ReasonDefault, ScheduleNotStarted},
		{"at the start", start, acctA, true, ReasonRuleMatch, ""},
		{"inside", start.Add(time.Hour), acctA, true, ReasonRuleMatch, ""},
		{"at the end", end, acctA, false, ReasonDefault, ScheduleEnded},
		{"other account inside", start.Add(time.Hour), acctB, false, ReasonDefault, ScheduleNotStarted}, // everyone-later
		{"everyone after general availability", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), acctB, true, ReasonRuleMatch, ""},
	}
	for _, tc := range cases {
		d := snap.Evaluate(AIGateway, Subject{AccountID: tc.subject, At: tc.at})
		if d.Enabled != tc.enabled || d.Reason != tc.reason || d.ScheduleState != tc.state {
			t.Errorf("%s: %+v, want enabled=%v reason=%s state=%q", tc.name, d, tc.enabled, tc.reason, tc.state)
		}
	}
	if _, err := ParseDefinition(json.RawMessage(`{"enabled":true,"rules":[{"id":"x","starts_at":"2026-11-04T00:00:00Z","ends_at":"2026-11-02T00:00:00Z","value":true}]}`)); err == nil {
		t.Fatal("ends_at before starts_at must be refused")
	}
}

func TestCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range Catalog() {
		if !flags.ValidKey(spec.Key) || seen[spec.Key] {
			t.Fatalf("invalid or duplicate flag key %q", spec.Key)
		}
		seen[spec.Key] = true
		if spec.Label == "" || spec.Description == "" || len(spec.Consumers) == 0 || spec.ADR == "" {
			t.Fatalf("flag %q must declare a label, description, ADR and consumers", spec.Key)
		}
		if err := ValidateDefinition(spec.Default, nil); err != nil {
			t.Fatalf("flag %q default: %v", spec.Key, err)
		}
		if key, ok := FlagKeyFromConfigKey(ConfigKey(spec.Key)); !ok || key != spec.Key || !IsConfigKey(ConfigKey(spec.Key)) {
			t.Fatalf("config key round trip failed for %q", spec.Key)
		}
		if _, ok := spec.RetireByTime(); spec.RetireBy != "" && !ok {
			t.Fatalf("flag %q retire_by %q must be YYYY-MM-DD", spec.Key, spec.RetireBy)
		}
		if spec.Capability != "" {
			if key, ok := FlagForCapability(spec.Capability); !ok || key != spec.Key {
				t.Fatalf("capability %q does not resolve to %q", spec.Capability, spec.Key)
			}
		}
	}
	if !IsConfigKey(GroupsConfigKey) || IsConfigKey("hsts_enabled") {
		t.Fatal("IsConfigKey misclassifies keys")
	}
	if got := len(ConfigKeys()); got != len(Catalog())+1 {
		t.Fatalf("ConfigKeys = %d, want one per flag plus groups", got)
	}
}
