package appstandards

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

var testLimits = Limits{DefinitionBytes: 65536, SetEntries: 64, Layers: 64}

const destinationA = "00000000-0000-4000-8000-000000000001"
const destinationB = "00000000-0000-4000-8000-000000000002"

func raw(value any) json.RawMessage { encoded, _ := json.Marshal(value); return encoded }
func layer(id, scope string, field Field, value any, mode Mode, override Override) Layer {
	return Layer{StandardID: id, Version: 1, Scope: scope, Definition: Definition{field: {Value: raw(value), Mode: mode, Override: override}}}
}

func TestParseCanonicalAndStrict(t *testing.T) {
	a, hashA, err := Parse([]byte(`{"egress_cidrs":{"mode":"restricted","value":["203.0.113.17/24","203.0.113.0/24"]}}`), testLimits)
	if err != nil {
		t.Fatal(err)
	}
	_, hashB, err := Parse([]byte(`{"egress_cidrs":{"override":"narrow","value":["203.0.113.0/24"],"mode":"restricted"}}`), testLimits)
	if err != nil || hashA != hashB {
		t.Fatalf("canonical hashes differ: %s %s %v", hashA, hashB, err)
	}
	if string(a[EgressCIDRs].Value) != `["203.0.113.0/24"]` {
		t.Fatal(a)
	}
	for _, input := range []string{
		`{}`, `null`, `[]`, `{"secrets":{"mode":"mandatory","value":"private"}}`,
		`{"require_signed":{"mode":"mandatory","value":true,"extra":1}}`,
		`{"require_signed":{"mode":"mandatory","value":true},"require_signed":{"mode":"default","value":false}}`,
		`{"require_signed":{"mode":"mandatory","mode":"default","value":true}}`,
		`{"require_signed":{"mode":"mandatory","value":true}} {}`,
		`{"require_signed":{"mode":"mandatory","value":null}}`,
		`{"require_signed":{"mode":"mandatory","override":"extend","value":true}}`,
		`{"egress_cidrs":{"mode":"restricted","value":[]}}`,
		`{"log_destinations":{"mode":"mandatory","value":["not-a-resource"]}}`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, _, err := Parse([]byte(input), testLimits); err == nil {
				t.Fatal("accepted invalid definition")
			}
		})
	}
}

func TestResolveRequirements(t *testing.T) {
	cases := []struct {
		name      string
		layers    []Layer
		local     Settings
		field     Field
		want      string
		violation bool
	}{
		{"default override", []Layer{layer("a", "organization", RequireSigned, true, Default, "")}, Settings{RequireSigned: raw(false)}, RequireSigned, "false", false},
		{"mandatory cannot weaken", []Layer{layer("a", "organization", RequireSigned, true, Mandatory, "")}, Settings{RequireSigned: raw(false)}, RequireSigned, "false", true},
		{"child default preserves mandate", []Layer{layer("a", "organization", RequireSigned, true, Mandatory, ""), layer("b", "application", RequireSigned, false, Default, "")}, nil, RequireSigned, "true", false},
		{"conflicting child mandate", []Layer{layer("a", "organization", RequireSigned, true, Mandatory, ""), layer("b", "application", RequireSigned, false, Mandatory, "")}, nil, RequireSigned, "true", true},
		{"narrow CIDRs", []Layer{layer("a", "organization", EgressCIDRs, []string{"203.0.113.0/24"}, Restricted, "")}, Settings{EgressCIDRs: raw([]string{"203.0.113.128/25"})}, EgressCIDRs, `["203.0.113.128/25"]`, false},
		{"clearing CIDRs widens", []Layer{layer("a", "organization", EgressCIDRs, []string{"203.0.113.0/24"}, Restricted, "")}, Settings{EgressCIDRs: raw([]string{})}, EgressCIDRs, `[]`, true},
		{"ipv6 does not allow ipv4", []Layer{layer("a", "organization", EgressCIDRs, []string{"2001:db8::/32"}, Restricted, "")}, Settings{EgressCIDRs: raw([]string{"203.0.113.0/24"})}, EgressCIDRs, `["203.0.113.0/24"]`, true},
		{"nested CIDR bounds", []Layer{layer("a", "organization", EgressCIDRs, []string{"203.0.113.0/24"}, Restricted, ""), layer("b", "project", EgressCIDRs, []string{"203.0.113.128/25"}, Restricted, "")}, nil, EgressCIDRs, `["203.0.113.128/25"]`, false},
		{"ports can narrow", []Layer{layer("a", "organization", EgressExtraPorts, []int{5432, 6379}, Restricted, "")}, Settings{EgressExtraPorts: raw([]int{5432})}, EgressExtraPorts, `[5432]`, false},
		{"ports cannot widen", []Layer{layer("a", "organization", EgressExtraPorts, []int{5432}, Restricted, "")}, Settings{EgressExtraPorts: raw([]int{5432, 6379})}, EgressExtraPorts, `[5432,6379]`, true},
		{"security can strengthen", []Layer{layer("a", "organization", SecurityPolicy, "warn", Mandatory, Narrow)}, Settings{SecurityPolicy: raw("enforce")}, SecurityPolicy, `"enforce"`, false},
		{"logging permits extras", []Layer{layer("a", "organization", LogDestinations, []string{destinationA}, Mandatory, Extend)}, Settings{LogDestinations: raw([]string{destinationB, destinationA})}, LogDestinations, `["` + destinationA + `","` + destinationB + `"]`, false},
		{"logging cannot remove required", []Layer{layer("a", "organization", LogDestinations, []string{destinationA}, Mandatory, Extend)}, Settings{LogDestinations: raw([]string{destinationB})}, LogDestinations, `["` + destinationB + `"]`, true},
		{"child logging extends", []Layer{layer("a", "organization", LogDestinations, []string{destinationA}, Mandatory, Extend), layer("b", "application", LogDestinations, []string{destinationB}, Mandatory, Extend)}, nil, LogDestinations, `["` + destinationA + `","` + destinationB + `"]`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			result, err := Resolve(nil, test.layers, test.local, nil, time.Now(), testLimits)
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Values[test.field]) != test.want {
				t.Fatalf("value: %s, want %s", result.Values[test.field], test.want)
			}
			if (len(result.Violations) > 0) != test.violation {
				t.Fatalf("violations: %+v", result.Violations)
			}
			if len(result.Sources[test.field]) != len(test.layers) {
				t.Fatalf("provenance: %+v", result.Sources)
			}
		})
	}
}

func TestExceptionIsVersionAndExpiryBound(t *testing.T) {
	now := time.Now()
	parent := layer("parent", "organization", RequireSigned, true, Mandatory, "")
	exception := Exception{ID: "exception", StandardID: "parent", Version: 1, Field: RequireSigned, Value: raw(false), Reason: "migration", ExpiresAt: now.Add(time.Hour)}
	for _, test := range []struct {
		name     string
		at       time.Time
		version  int64
		violated bool
	}{
		{"active", now, 1, false}, {"expires at boundary", exception.ExpiresAt, 1, true}, {"expired", now.Add(2 * time.Hour), 1, true}, {"other version", now, 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent.Version = test.version
			result, err := Resolve(nil, []Layer{parent}, Settings{RequireSigned: raw(false)}, []Exception{exception}, test.at, testLimits)
			if err != nil {
				t.Fatal(err)
			}
			if (len(result.Violations) > 0) != test.violated {
				t.Fatalf("violations %+v", result.Violations)
			}
			if !test.violated && result.Sources[RequireSigned][0].ExceptionID != exception.ID {
				t.Fatal("missing exception provenance")
			}
		})
	}
	child := layer("child", "application", RequireSigned, true, Mandatory, "")
	parent.Version = 1
	result, err := Resolve(nil, []Layer{parent, child}, Settings{RequireSigned: raw(false)}, []Exception{exception}, now, testLimits)
	if err != nil || len(result.Violations) == 0 {
		t.Fatalf("exception bypassed independent standard: %+v %v", result, err)
	}
}

func TestLayerOrderDoesNotChangeResolution(t *testing.T) {
	layers := []Layer{layer("c", "application", SecurityPolicy, "enforce", Restricted, ""), layer("a", "organization", SecurityPolicy, "warn", Restricted, ""), layer("b", "project", SecurityPolicy, "off", Default, "")}
	a, err := Resolve(nil, layers, nil, nil, time.Now(), testLimits)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(layers)
	b, err := Resolve(nil, layers, nil, nil, time.Now(), testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw(a)) != string(raw(b)) {
		t.Fatalf("nondeterministic result %s %s", raw(a), raw(b))
	}
}

func FuzzResolverNeverAcceptsWiderCIDR(f *testing.F) {
	f.Add("203.0.113.0/24", "203.0.113.128/25")
	f.Add("2001:db8::/32", "2001:db8:1::/48")
	f.Fuzz(func(t *testing.T, bound, candidate string) {
		definition, _, err := Normalize(Definition{EgressCIDRs: {Mode: Restricted, Value: raw([]string{bound})}}, testLimits)
		if err != nil {
			return
		}
		result, err := Resolve(nil, []Layer{{StandardID: "a", Version: 1, Scope: "organization", Definition: definition}}, Settings{EgressCIDRs: raw([]string{candidate})}, nil, time.Now(), testLimits)
		if err != nil {
			return
		}
		if len(result.Violations) == 0 && !prefixContained(stringsValue(result.Values[EgressCIDRs])[0], stringsValue(definition[EgressCIDRs].Value)) {
			t.Fatal("accepted broader access")
		}
	})
}

func TestDisjointCIDRsCannotBecomeUnrestricted(t *testing.T) {
	_, err := Resolve(nil, []Layer{layer("a", "organization", EgressCIDRs, []string{"203.0.113.0/24"}, Restricted, ""), layer("b", "application", EgressCIDRs, []string{"198.51.100.0/24"}, Restricted, "")}, nil, nil, time.Now(), testLimits)
	if err == nil || !strings.Contains(err.Error(), "no representable overlap") {
		t.Fatalf("expected conflict, got %v", err)
	}
}
