package api

import "testing"

// adr: 20261107190000 — a gate's match condition holds for every
// path form its match_path covers (dot segments, duplicate slashes, ";"
// parameters, case) and for GET on a HEAD request, so "everything except
// /public" and "only /admin" conditions cannot be stepped around.
func TestMatchesProtectiveCoversPathFormsAndHead(t *testing.T) {
	compile := func(expr EdgeRuleMatchExpr) *EdgeRuleMatchProgram {
		t.Helper()
		p, err := CompileEdgeRuleMatch(&expr)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	exceptPublic := compile(EdgeRuleMatchExpr{Not: &EdgeRuleMatchExpr{Field: "path", Op: "prefix", Value: "/public"}})
	onlyAdmin := compile(EdgeRuleMatchExpr{Field: "path", Op: "prefix", Value: "/admin"})
	onlyGet := compile(EdgeRuleMatchExpr{Field: "method", Op: "eq", Value: "GET"})
	for _, tc := range []struct {
		name    string
		program *EdgeRuleMatchProgram
		method  string
		path    string
		want    bool
	}{
		{"except /public: dot segments into /admin", exceptPublic, "GET", "/public/../admin/users", true},
		{"except /public: a real public path", exceptPublic, "GET", "/public/logo.png", false},
		{"only /admin: duplicate slash", onlyAdmin, "GET", "//admin/users", true},
		{"only /admin: upper case", onlyAdmin, "GET", "/ADMIN/users", true},
		{"only /admin: backslash", onlyAdmin, "GET", "/x\\..\\admin", true},
		{"only /admin: segment parameters", onlyAdmin, "GET", "/admin;jsessionid=1/users", true},
		{"only /admin: elsewhere", onlyAdmin, "GET", "/shop", false},
		{"only GET: HEAD runs the GET handler", onlyGet, "HEAD", "/", true},
		{"only GET: POST", onlyGet, "POST", "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := EdgeRuleMatchInput{Method: tc.method, Path: tc.path}
			if got := tc.program.MatchesProtective(in); got != tc.want {
				t.Fatalf("MatchesProtective(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
	// Exact matching (non-gate kinds) is unchanged.
	if onlyAdmin.Matches(EdgeRuleMatchInput{Method: "GET", Path: "/ADMIN/users"}) {
		t.Fatal("Matches widened to case-folded paths")
	}
}
