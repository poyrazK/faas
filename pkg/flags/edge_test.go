package flags

import (
	"strings"
	"testing"
)

func edgeFlag(key string, routes ...EdgeRoute) Flag {
	return Flag{Key: key, Type: "variant", Enabled: true, Default: "a", Seed: "s",
		Variants: []FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}}, Edge: &Edge{Routes: routes}}
}

func route(path, variant, rewrite string) EdgeRoute {
	return EdgeRoute{Path: path, Variants: map[string]EdgeAction{variant: {Rewrite: rewrite}}}
}

func TestValidateEdge(t *testing.T) {
	boolean := edgeFlag("bool", route("/x", "a", "/y"))
	boolean.Type, boolean.Default, boolean.Variants = "", false, nil
	many := edgeFlag("many")
	for i := 0; i < 9; i++ {
		many.Edge.Routes = append(many.Edge.Routes, route("/p"+strings.Repeat("x", i), "b", "/q"))
	}
	tests := []struct {
		name  string
		flags []Flag
		want  string
	}{
		{"exact and prefix routes", []Flag{edgeFlag("f", route("/pricing", "b", "/pricing-b"), route("/docs/*", "b", "/docs-b"))}, ""},
		{"root prefix", []Flag{edgeFlag("f", route("/*", "b", "/b"))}, ""},
		{"boolean flag", []Flag{boolean}, "only variant flags"},
		{"no routes", []Flag{edgeFlag("f")}, "needs 1 to"},
		{"too many routes", []Flag{many}, "needs 1 to"},
		{"relative path", []Flag{edgeFlag("f", route("pricing", "b", "/x"))}, "invalid edge path"},
		{"query in path", []Flag{edgeFlag("f", route("/p?x=1", "b", "/x"))}, "invalid edge path"},
		{"inner wildcard", []Flag{edgeFlag("f", route("/a/*/b", "b", "/x"))}, "invalid edge path"},
		{"dot segment", []Flag{edgeFlag("f", route("/a/../b", "b", "/x"))}, "invalid edge path"},
		{"unknown variant", []Flag{edgeFlag("f", route("/p", "c", "/x"))}, "unknown variant"},
		{"no variants", []Flag{edgeFlag("f", EdgeRoute{Path: "/p"})}, "routes no variant"},
		{"bad rewrite", []Flag{edgeFlag("f", route("/p", "b", "/x/*"))}, "edge rewrite"},
		{"rewrite with query", []Flag{edgeFlag("f", route("/p", "b", "/x?y"))}, "edge rewrite"},
		{"path routed twice", []Flag{edgeFlag("f", route("/p", "b", "/x")), edgeFlag("g", route("/p", "b", "/y"))}, "routed by both"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEdge(Config{Flags: tc.flags})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("validateEdge = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestValidateEdgeLimitsFlags(t *testing.T) {
	var fs []Flag
	for i := 0; i < 17; i++ {
		fs = append(fs, edgeFlag("f"+strings.Repeat("x", i), route("/p"+strings.Repeat("x", i), "b", "/q")))
	}
	if err := validateEdge(Config{Flags: fs}); err == nil || !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("validateEdge = %v; want the edge flag limit", err)
	}
}

func TestEdgeMatchAndRewrite(t *testing.T) {
	c := Config{Flags: []Flag{
		{Key: "plain", Type: "variant"},
		edgeFlag("docs", route("/docs/*", "b", "/docs-b"), route("/docs/api/*", "b", "/api-b")),
		edgeFlag("pricing", route("/pricing", "b", "/pricing-b")),
		edgeFlag("site", route("/*", "b", "/b")),
	}}
	tests := []struct {
		path, flag, rewritten string
	}{
		{"/pricing", "pricing", "/pricing-b"},
		{"/docs", "docs", "/docs-b"},
		{"/docs/", "docs", "/docs-b/"},
		{"/docs/intro", "docs", "/docs-b/intro"},
		{"/docs/api/x", "docs", "/api-b/x"}, // the longest prefix wins
		{"/docsx", "site", "/b/docsx"},
		{"/", "site", "/b/"},
		{"/pricing/x", "site", "/b/pricing/x"},
	}
	for _, tc := range tests {
		f, r, ok := EdgeMatch(c, tc.path)
		if !ok || f.Key != tc.flag {
			t.Errorf("EdgeMatch(%q) = %q, %v; want %q", tc.path, f.Key, ok, tc.flag)
			continue
		}
		if got := EdgeRewritePath(r, r.Variants["b"], tc.path); got != tc.rewritten {
			t.Errorf("EdgeRewritePath(%q) = %q; want %q", tc.path, got, tc.rewritten)
		}
	}
	if _, _, ok := EdgeMatch(Config{Flags: []Flag{edgeFlag("p", route("/pricing", "b", "/x"))}}, "/pricing2"); ok {
		t.Error("an exact route matched another path")
	}
}

func TestMergeEvidenceKeepsApplicationDecisions(t *testing.T) {
	bucket := 7000
	edge := []Evidence{
		{Decision: Decision{Flag: "pricing", Value: "b", Type: "variant", ConfigVersion: 3, RuleID: "r", Reason: "rule_match", Source: "configuration", Bucket: &bucket, RolloutBucket: &bucket}, Used: true},
		{Decision: Decision{Flag: "app", Value: "edge", Type: "variant", ConfigVersion: 3, Reason: "default", Source: "configuration"}, Used: true},
	}
	app, err := CanonicalEvidence([]byte(`[{"flag":"app","value":"code","type":"variant","config_version":3,"reason":"default","source":"configuration","used":true}]`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := MergeEvidence(app, edge)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"flag":"pricing"`) || !strings.Contains(got, `"value":"code"`) || strings.Contains(got, `"value":"edge"`) {
		t.Fatalf("MergeEvidence = %s; want the edge decision added and the application's kept", got)
	}
	if only, err := MergeEvidence("", edge[:1]); err != nil || !strings.Contains(only, `"pricing"`) {
		t.Fatalf("MergeEvidence without application evidence = %s, %v", only, err)
	}
}

func TestSameEdge(t *testing.T) {
	a := Config{Flags: []Flag{{Key: "plain"}, edgeFlag("e", route("/p", "b", "/q"))}}
	reordered := Config{Flags: []Flag{edgeFlag("e", route("/p", "b", "/q")), {Key: "plain", Enabled: true}}}
	changed := Config{Flags: []Flag{edgeFlag("e", route("/p", "b", "/other"))}}
	if !SameEdge(a, reordered) || SameEdge(a, changed) || SameEdge(a, Config{}) || !SameEdge(Config{}, Config{Flags: []Flag{{Key: "x"}}}) {
		t.Fatal("SameEdge compares edge routes per flag only")
	}
}

func TestWithoutEdgeAndEdgeOnly(t *testing.T) {
	c := Config{Flags: []Flag{{Key: "plain"}, edgeFlag("e", route("/p", "b", "/q"))}}
	stripped := WithoutEdge(c)
	if HasEdge(stripped) || !HasEdge(c) || len(stripped.Flags) != 2 {
		t.Fatalf("WithoutEdge = %+v; the original must keep its routes", stripped)
	}
	if only := EdgeOnly(c); len(only.Flags) != 1 || only.Flags[0].Key != "e" {
		t.Fatalf("EdgeOnly = %+v", only)
	}
}
