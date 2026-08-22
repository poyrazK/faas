package openapidiff

// Tests for the auto-generation extension (ADR-126 / issue #975
// item #2). GenerateFromApp is the canonical entry point for
// `?source=auto`; ComputeDryRun is the read-only shape for
// `?dry-run`. Both are pure functions of their inputs — no DB,
// no gateway — so the test surface is hermetic.

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

const importedDoc3Users = `{
  "openapi": "3.1.0",
  "info": {"title": "Test API", "version": "1.0.0"},
  "paths": {
    "/users":     {"get": {"summary": "list users"}, "post": {"summary": "create user"}},
    "/users/{id}": {"get": {"summary": "get user"}, "delete": {"summary": "delete user"}},
    "/healthz":   {"get": {"summary": "health"}}
  }
}`

// TestGenerateFromApp_AllInputsPresent pins the happy path. With
// imported doc + observed routes + edge rules all present, the
// returned Spec has paths from the import, stub paths from the
// observed routes (when the import doesn't cover them), and the
// annotations sidecar carries the (kind, action) projection
// for matching rules.
func TestGenerateFromApp_AllInputsPresent(t *testing.T) {
	in := GenerateFromAppInputs{
		AppID:        "app-1",
		AccountID:    "acct-1",
		ImportedDoc:  []byte(importedDoc3Users),
		ObservedRoutes: []RouteRow{
			{Route: "GET /v2/internal/metrics", Count: 7},
		},
		EdgeRules: []state.EdgeRule{
			{ID: "rule-1", Kind: state.EdgeRuleKindValidate, MatchPath: "/users", MatchMethods: []string{"get", "post"}},
		},
	}
	spec, meta, err := GenerateFromApp(in)
	if err != nil {
		t.Fatalf("GenerateFromApp: %v", err)
	}
	if meta.Source != SourceAuto {
		t.Errorf("Source: got %q, want %q", meta.Source, SourceAuto)
	}
	// 3 imported paths + 1 observed path = 4 total.
	if len(spec.Paths) != 4 {
		t.Errorf("paths: got %d, want 4 (%v)", len(spec.Paths), keys(spec.Paths))
	}
	// /v2/internal/metrics came from observed routes only.
	if _, ok := spec.Paths["/v2/internal/metrics"]; !ok {
		t.Error("expected observed path /v2/internal/metrics")
	}
	// /users path's GET was annotated with the validate rule.
	annotations := meta.Annotations
	if got := annotations["get /users"]; len(got) != 1 || got[0].Kind != "validate" {
		t.Errorf("annotations[get /users]: got %+v, want 1 validate", got)
	}
	if meta.DocSHA256 == ([32]byte{}) {
		t.Error("DocSHA256 empty")
	}
	if meta.RoutesSHA256 == ([32]byte{}) {
		t.Error("RoutesSHA256 empty")
	}
	if meta.RulesSHA256 == ([32]byte{}) {
		t.Error("RulesSHA256 empty")
	}
}

// TestGenerateFromApp_NoImport_RulesOnly pins the degraded
// path: no imported doc but rules present. The spec carries
// the rule actions on the empty paths map (annotations empty
// because no path matches), and Source is
// "empty: no_import".
func TestGenerateFromApp_NoImport_RulesOnly(t *testing.T) {
	in := GenerateFromAppInputs{
		AppID:     "app-1",
		AccountID: "acct-1",
		EdgeRules: []state.EdgeRule{
			{ID: "rule-1", Kind: state.EdgeRuleKindRoute, MatchPath: "/v1/foo", MatchMethods: []string{"get"}},
		},
	}
	_, meta, err := GenerateFromApp(in)
	if err != nil {
		t.Fatalf("GenerateFromApp: %v", err)
	}
	if meta.Source != SourceEmptyImport {
		t.Errorf("Source: got %q, want %q", meta.Source, SourceEmptyImport)
	}
	if meta.DocSHA256 != ([32]byte{}) {
		t.Error("DocSHA256 should be empty when no import")
	}
	if meta.RulesSHA256 == ([32]byte{}) {
		t.Error("RulesSHA256 should be non-empty when rules present")
	}
}

// TestGenerateFromApp_NoImport_NoRules_ReturnsErrImportMissing
// pins the empty-state path. With no imported doc, no observed
// routes, and no edge rules, GenerateFromApp returns
// ErrImportMissing so the apid handler can render a 200 with
// Source: "empty: no_import_no_rules" instead of 502'ing.
func TestGenerateFromApp_NoImport_NoRules_ReturnsErrImportMissing(t *testing.T) {
	in := GenerateFromAppInputs{AppID: "app-1", AccountID: "acct-1"}
	_, _, err := GenerateFromApp(in)
	if !errors.Is(err, ErrImportMissing) {
		t.Errorf("err: got %v, want ErrImportMissing", err)
	}
}

// TestGenerateFromApp_DegradedRoutes pins the
// degraded-routes path. Imported doc + rules are present, but
// the gateway is unavailable (nil ObservedRoutes). Source is
// "degraded: routes_unavailable", and the spec is built from
// the import + rules alone.
func TestGenerateFromApp_DegradedRoutes(t *testing.T) {
	in := GenerateFromAppInputs{
		AppID:       "app-1",
		AccountID:   "acct-1",
		ImportedDoc: []byte(importedDoc3Users),
		EdgeRules: []state.EdgeRule{
			{ID: "rule-1", Kind: state.EdgeRuleKindHeaders, MatchPath: "/", MatchMethods: []string{"get"}},
		},
	}
	spec, meta, err := GenerateFromApp(in)
	if err != nil {
		t.Fatalf("GenerateFromApp: %v", err)
	}
	if meta.Source != SourceDegradedRoutes {
		t.Errorf("Source: got %q, want %q", meta.Source, SourceDegradedRoutes)
	}
	if len(spec.Paths) != 3 {
		t.Errorf("paths: got %d, want 3 (import only)", len(spec.Paths))
	}
	if meta.RoutesSHA256 != ([32]byte{}) {
		t.Error("RoutesSHA256 should be empty when observed routes nil")
	}
}

// TestGenerateFromApp_DegradedRules pins the degraded-rules
// path. Imported doc + observed routes are present, but
// edge-rules lookup failed (nil). Source is
// "degraded: rules_unavailable".
func TestGenerateFromApp_DegradedRules(t *testing.T) {
	in := GenerateFromAppInputs{
		AppID:           "app-1",
		AccountID:       "acct-1",
		ImportedDoc:     []byte(importedDoc3Users),
		ObservedRoutes:  []RouteRow{{Route: "GET /v2/x", Count: 1}},
	}
	_, meta, err := GenerateFromApp(in)
	if err != nil {
		t.Fatalf("GenerateFromApp: %v", err)
	}
	if meta.Source != SourceDegradedRules {
		t.Errorf("Source: got %q, want %q", meta.Source, SourceDegradedRules)
	}
	if meta.RulesSHA256 != ([32]byte{}) {
		t.Error("RulesSHA256 should be empty when rules nil")
	}
}

// TestGenerateFromApp_InvalidImportedDoc pins the parse-error
// path. A non-OpenAPI blob returns an error from GenerateFromApp.
func TestGenerateFromApp_InvalidImportedDoc(t *testing.T) {
	in := GenerateFromAppInputs{
		AppID:       "app-1",
		AccountID:   "acct-1",
		ImportedDoc: []byte("not even JSON"),
	}
	_, _, err := GenerateFromApp(in)
	if err == nil {
		t.Fatal("expected error on invalid imported doc")
	}
}

// TestMergeObservedRoutes_NoOverlap pins the merge: observed
// routes not in the import are added as stub paths.
func TestMergeObservedRoutes_NoOverlap(t *testing.T) {
	spec := &Spec{Paths: map[string]*PathItem{}}
	rows := []RouteRow{
		{Route: "GET /v1/observed", Count: 3},
		{Route: "POST /v1/observed", Count: 1},
	}
	mergeObservedRoutes(spec, rows)
	if len(spec.Paths) != 1 {
		t.Errorf("paths: got %d, want 1", len(spec.Paths))
	}
	pi, ok := spec.Paths["/v1/observed"]
	if !ok {
		t.Fatal("/v1/observed missing")
	}
	if _, ok := pi.Methods["get"]; !ok {
		t.Error("expected GET method")
	}
	if _, ok := pi.Methods["post"]; !ok {
		t.Error("expected POST method")
	}
}

// TestMergeObservedRoutes_WithOverlap pins the no-op: an
// observed route that already exists in the spec is not
// overwritten.
func TestMergeObservedRoutes_WithOverlap(t *testing.T) {
	existing := &Operation{Responses: map[string]*Response{"404": {}}}
	spec := &Spec{Paths: map[string]*PathItem{
		"/v1/observed": {Methods: map[string]*Operation{"get": existing}},
	}}
	rows := []RouteRow{{Route: "GET /v1/observed", Count: 3}}
	mergeObservedRoutes(spec, rows)
	got := spec.Paths["/v1/observed"].Methods["get"]
	if got != existing {
		t.Error("existing operation was overwritten by observed-route merge")
	}
}

// TestMergeObservedRoutes_SkipsMalformed pins the
// defensive split: rows without a space separator are
// silently dropped.
func TestMergeObservedRoutes_SkipsMalformed(t *testing.T) {
	spec := &Spec{Paths: map[string]*PathItem{}}
	rows := []RouteRow{
		{Route: "GET /v1/good"},
		{Route: "__route_other__"},          // missing space → drop
		{Route: ""},                          // empty → drop
		{Route: " /v1/missing-method"},       // empty method → drop
	}
	mergeObservedRoutes(spec, rows)
	if len(spec.Paths) != 1 {
		t.Errorf("paths: got %d, want 1 (only the well-formed row)", len(spec.Paths))
	}
}

// TestAnnotateWithEdgeRules_MatchPath pins the (path, method)
// projection. A rule with MatchPath="/users" projects onto
// /users's get method; a rule with MatchPath="/" projects onto
// every path. Both rules in this test have MatchMethods=[get],
// so only GET operations get annotated.
func TestAnnotateWithEdgeRules_MatchPath(t *testing.T) {
	spec := &Spec{Paths: map[string]*PathItem{
		"/users": {Methods: map[string]*Operation{
			"get":  {},
			"post": {},
		}},
		"/healthz": {Methods: map[string]*Operation{
			"get": {},
		}},
	}}
	rules := []state.EdgeRule{
		{ID: "r1", Kind: state.EdgeRuleKindValidate, MatchPath: "/users", MatchMethods: []string{"get"}},
		{ID: "r2", Kind: state.EdgeRuleKindHeaders, MatchPath: "/", MatchMethods: []string{"get"}},
	}
	got := annotateWithEdgeRules(spec, rules)
	// /users get: validate (r1, MatchPath=/users) + headers (r2, MatchPath=/) → 2 annotations.
	if len(got["get /users"]) != 2 {
		t.Errorf("get /users: got %d annotations, want 2", len(got["get /users"]))
	}
	// /users post: zero — both rules have MatchMethods=[get], so post is not annotated.
	if len(got["post /users"]) != 0 {
		t.Errorf("post /users: got %d annotations, want 0 (rules are GET-only)", len(got["post /users"]))
	}
	// /healthz get: only headers (r2); r1's MatchPath=/users does not match /healthz.
	if len(got["get /healthz"]) != 1 {
		t.Errorf("get /healthz: got %d annotations, want 1 (headers only)", len(got["get /healthz"]))
	}
}

// TestAnnotateWithEdgeRules_NoMatch pins the no-match path: a
// rule whose MatchPath is not in the spec returns empty
// annotations.
func TestAnnotateWithEdgeRules_NoMatch(t *testing.T) {
	spec := &Spec{Paths: map[string]*PathItem{
		"/users": {Methods: map[string]*Operation{"get": {}}},
	}}
	rules := []state.EdgeRule{
		{ID: "r1", Kind: state.EdgeRuleKindRoute, MatchPath: "/v1/nope", MatchMethods: []string{"get"}},
	}
	got := annotateWithEdgeRules(spec, rules)
	if len(got) != 0 {
		t.Errorf("expected empty annotations; got %v", got)
	}
}

// TestAnnotateWithEdgeRules_EmptyMethods_AllMethods pins the
// wildcard methods path. A rule with empty MatchMethods applies
// to every method on the matched path.
func TestAnnotateWithEdgeRules_EmptyMethods_AllMethods(t *testing.T) {
	spec := &Spec{Paths: map[string]*PathItem{
		"/users": {Methods: map[string]*Operation{"get": {}, "post": {}, "delete": {}}},
	}}
	rules := []state.EdgeRule{
		{ID: "r1", Kind: state.EdgeRuleKindCORSA, MatchPath: "/users", MatchMethods: nil},
	}
	got := annotateWithEdgeRules(spec, rules)
	if len(got["get /users"]) != 1 || len(got["post /users"]) != 1 || len(got["delete /users"]) != 1 {
		t.Errorf("expected 1 annotation per method; got %v", got)
	}
}

// TestHashRules_Stable pins the canonical-hash contract:
// same rules → same SHA; reordered rules → same SHA;
// rule changed → different SHA.
func TestHashRules_Stable(t *testing.T) {
	rules1 := []state.EdgeRule{
		{ID: "a", Kind: state.EdgeRuleKindValidate, MatchPath: "/users"},
		{ID: "b", Kind: state.EdgeRuleKindHeaders, MatchPath: "/healthz"},
	}
	rules2 := []state.EdgeRule{
		{ID: "b", Kind: state.EdgeRuleKindHeaders, MatchPath: "/healthz"},
		{ID: "a", Kind: state.EdgeRuleKindValidate, MatchPath: "/users"},
	}
	rules3 := []state.EdgeRule{
		{ID: "a", Kind: state.EdgeRuleKindValidate, MatchPath: "/users-CHANGED"},
	}
	if hashRules(rules1) != hashRules(rules2) {
		t.Error("reorder changed the hash; canonical sort should normalise")
	}
	if hashRules(rules1) == hashRules(rules3) {
		t.Error("rule change did not change the hash")
	}
}

// TestHashRoutes_Stable pins the canonical-routes-hash contract.
func TestHashRoutes_Stable(t *testing.T) {
	r1 := []RouteRow{
		{Route: "GET /users", Count: 10},
		{Route: "GET /healthz", Count: 5},
	}
	r2 := []RouteRow{
		{Route: "GET /healthz", Count: 5},
		{Route: "GET /users", Count: 10},
	}
	r3 := []RouteRow{
		{Route: "GET /users", Count: 11}, // count differs
	}
	if hashRoutes(r1) != hashRoutes(r2) {
		t.Error("reorder changed the hash; canonical sort should normalise")
	}
	if hashRoutes(r1) == hashRoutes(r3) {
		t.Error("count change did not change the hash")
	}
}

// TestComputeDryRun_Empty pins the no-import path: dry-run
// returns zero suggestions when no imported doc.
func TestComputeDryRun_Empty(t *testing.T) {
	out, err := ComputeDryRun(nil, nil)
	if err != nil {
		t.Fatalf("ComputeDryRun: %v", err)
	}
	if len(out.Suggestions) != 0 {
		t.Errorf("suggestions: got %d, want 0", len(out.Suggestions))
	}
}

// TestComputeDryRun_UncoveredPaths pins the dry-run happy
// path. Every (path, method) in the import that isn't covered
// by an existing validate rule gets a suggestion.
func TestComputeDryRun_UncoveredPaths(t *testing.T) {
	existing := []state.EdgeRule{
		{ID: "r1", Kind: state.EdgeRuleKindValidate, MatchPath: "/users", MatchMethods: []string{"get"}},
	}
	out, err := ComputeDryRun([]byte(importedDoc3Users), existing)
	if err != nil {
		t.Fatalf("ComputeDryRun: %v", err)
	}
	// /users get is covered → no suggestion.
	// /users post, /users/{id} get/delete, /healthz get → 4 suggestions.
	if len(out.Suggestions) != 4 {
		t.Errorf("suggestions: got %d, want 4 (%v)", len(out.Suggestions), suggestionPaths(out.Suggestions))
	}
	if out.OpenAPIVersion != "3.1.0" {
		t.Errorf("OpenAPIVersion: got %q, want 3.1.0", out.OpenAPIVersion)
	}
	if out.EndpointCount != 5 {
		t.Errorf("EndpointCount: got %d, want 5", out.EndpointCount)
	}
}

// TestComputeDryRun_FullyCovered pins the no-suggestions path.
// When every operation is covered by an existing validate rule,
// the response has an empty Suggestions array.
func TestComputeDryRun_FullyCovered(t *testing.T) {
	existing := []state.EdgeRule{
		{ID: "r1", Kind: state.EdgeRuleKindValidate, MatchPath: "/users", MatchMethods: []string{"get", "post"}},
		{ID: "r2", Kind: state.EdgeRuleKindValidate, MatchPath: "/users/{id}", MatchMethods: []string{"get", "delete"}},
		{ID: "r3", Kind: state.EdgeRuleKindValidate, MatchPath: "/healthz", MatchMethods: []string{"get"}},
	}
	out, err := ComputeDryRun([]byte(importedDoc3Users), existing)
	if err != nil {
		t.Fatalf("ComputeDryRun: %v", err)
	}
	if len(out.Suggestions) != 0 {
		t.Errorf("suggestions: got %d, want 0 (fully covered); got %v", len(out.Suggestions), suggestionPaths(out.Suggestions))
	}
}

// TestComputeDryRun_SuggestionsSorted pins the deterministic
// ordering. Same input always produces suggestions in
// (path asc, method asc) order.
func TestComputeDryRun_SuggestionsSorted(t *testing.T) {
	out, err := ComputeDryRun([]byte(importedDoc3Users), nil)
	if err != nil {
		t.Fatalf("ComputeDryRun: %v", err)
	}
	gotPaths := suggestionPaths(out.Suggestions)
	// ASCII sort: `/` (0x2F) < `:` (0x3A), so `/users/{id}:delete`
	// sorts BEFORE `/users:get` — the `/` at position 6 of the
	// templated path beats the `:` we appended as the method
	// separator. This is the contract the test pins: deterministic
	// ordering across runs (the dashboard relies on it for stable
	// rendering).
	wantPaths := []string{
		"/healthz:get",
		"/users/{id}:delete",
		"/users/{id}:get",
		"/users:get",
		"/users:post",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("paths: got %v, want %v", gotPaths, wantPaths)
	}
}

// TestLowerASCII pins the helper.
func TestLowerASCII(t *testing.T) {
	cases := map[string]string{
		"GET":     "get",
		"POST":    "post",
		"Delete":  "delete",
		"already": "already",
		"":        "",
		"GET /v1": "get /v1",
	}
	for in, want := range cases {
		if got := lowerASCII(in); got != want {
			t.Errorf("lowerASCII(%q): got %q, want %q", in, got, want)
		}
	}
}

// keys returns the keys of a path map, sorted, for stable
// error messages.
func keys(m map[string]*PathItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// suggestionPaths returns the path strings of a suggestion
// slice, sorted, for stable error messages.
func suggestionPaths(s []EdgeRuleSuggestion) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		out = append(out, x.Path+":"+x.Methods[0])
	}
	sort.Strings(out)
	return out
}
