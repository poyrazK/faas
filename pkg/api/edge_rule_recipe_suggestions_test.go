package api

import (
	"slices"
	"strings"
	"testing"
)

// adr: 20261020105000 — suggestions need a volume floor (and a share where it
// applies), skip what the app already has, and carry the parameters the
// recipe is added with.
func TestSuggestEdgeRuleRecipes(t *testing.T) {
	recipeRule := func(id string) EdgeRuleRecipeSuggestionRule {
		return EdgeRuleRecipeSuggestionRule{Kind: "ip", MatchPath: "/**", Description: EdgeRuleRecipeDescription(id), Enabled: true}
	}
	login := EdgeRuleRecipeSuggestionRoute{Method: "POST", Path: "/api/login", Requests: 1200, ErrorRequests: 300}
	cases := []struct {
		name    string
		signals EdgeRuleRecipeSuggestionSignals
		want    []string // recipe:path
	}{
		{name: "quiet app", signals: EdgeRuleRecipeSuggestionSignals{TotalRequests: 400, BotRequests: 200}},
		{name: "bots over floor and share", signals: EdgeRuleRecipeSuggestionSignals{TotalRequests: 10000, BotRequests: 600}, want: []string{"block-unverified-bots:"}},
		{name: "bots under share", signals: EdgeRuleRecipeSuggestionSignals{TotalRequests: 100000, BotRequests: 600}},
		{name: "bot recipe already added", signals: EdgeRuleRecipeSuggestionSignals{TotalRequests: 10000, BotRequests: 600, Rules: []EdgeRuleRecipeSuggestionRule{recipeRule(EdgeRuleRecipeBlockUnverifiedBots)}}},
		{name: "busy sign-in path", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{login}}, want: []string{"rate-limit-path:/api/login"}},
		{name: "failing sign-in path under volume floor", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "POST", Path: "/auth/token", Requests: 200, ErrorRequests: 60}}}, want: []string{"rate-limit-path:/auth/token"}},
		{name: "few errors on a quiet sign-in path", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "POST", Path: "/login", Requests: 250, ErrorRequests: 40}}}},
		{name: "GET sign-in page", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "GET", Path: "/login", Requests: 5000}}}},
		{name: "templated path", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "POST", Path: "/users/{id}/password", Requests: 5000}}}},
		{name: "not a sign-in path", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "POST", Path: "/api/orders", Requests: 5000}}}},
		{name: "sign-in path already throttled", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{login}, Rules: []EdgeRuleRecipeSuggestionRule{{Kind: "throttle", MatchPath: "/api/**", Enabled: true}}}},
		{name: "disabled throttle does not cover", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{login}, Rules: []EdgeRuleRecipeSuggestionRule{{Kind: "throttle", MatchPath: "/api/**"}}}, want: []string{"rate-limit-path:/api/login"}},
		{name: "two busiest sign-in paths", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{
			{Method: "POST", Path: "/signup", Requests: 400},
			{Method: "POST", Path: "/login", Requests: 900},
			{Method: "POST", Path: "/password/reset", Requests: 600},
		}}, want: []string{"rate-limit-path:/login", "rate-limit-path:/password/reset"}},
		{name: "admin traffic", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Path: "/admin", Requests: 8}, {Method: "GET", Path: "/admin/users", Requests: 15}}}, want: []string{"allow-ips-only:/admin/**"}},
		{name: "admin under floor", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Path: "/admin", Requests: 19}}}},
		{name: "administrator is not admin", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Path: "/administrator", Requests: 100}}}},
		{name: "admin already restricted", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Path: "/admin", Requests: 100}}, Rules: []EdgeRuleRecipeSuggestionRule{{Kind: "ip", MatchPath: "/admin/**", Enabled: true}}}},
		{name: "bot recipe does not cover admin", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Path: "/admin", Requests: 100}}, Rules: []EdgeRuleRecipeSuggestionRule{recipeRule(EdgeRuleRecipeBlockUnverifiedBots)}}, want: []string{"allow-ips-only:/admin/**"}},
		{name: "scanner probes", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "GET", Path: "/.env", Requests: 12, ErrorRequests: 12}, {Method: "GET", Path: "/wp-login.php", Requests: 9, ErrorRequests: 9}}}, want: []string{"block-scanner-paths:"}},
		{name: "scanner probes under floor", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "GET", Path: "/.git/config", Requests: 19, ErrorRequests: 19}}}},
		{name: "scanner recipe already added", signals: EdgeRuleRecipeSuggestionSignals{Routes: []EdgeRuleRecipeSuggestionRoute{{Method: "GET", Path: "/.env", Requests: 50}}, Rules: []EdgeRuleRecipeSuggestionRule{recipeRule(EdgeRuleRecipeBlockScannerPaths)}}},
		{name: "everything at once, two sign-in paths at most", signals: EdgeRuleRecipeSuggestionSignals{TotalRequests: 10000, BotRequests: 2000, Routes: []EdgeRuleRecipeSuggestionRoute{
			login, {Method: "POST", Path: "/signin", Requests: 800}, {Method: "POST", Path: "/register", Requests: 700}, {Path: "/admin", Requests: 50},
		}}, want: []string{"block-unverified-bots:", "rate-limit-path:/api/login", "rate-limit-path:/signin", "allow-ips-only:/admin/**"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SuggestEdgeRuleRecipes(tc.signals)
			var keys []string
			for _, sg := range got {
				keys = append(keys, sg.Recipe+":"+sg.Params["path"])
				recipe, ok := EdgeRuleRecipeByID(sg.Recipe)
				if !ok || sg.Title != recipe.Title || sg.Reason == "" {
					t.Fatalf("suggestion %+v: unknown recipe, wrong title or no reason", sg)
				}
			}
			if strings.Join(keys, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("suggestions = %v, want %v", keys, tc.want)
			}
		})
	}
}

// adr: 20261020105000 — a suggestion's parameters build a valid recipe request once
// the required inputs it cannot fill are given.
func TestEdgeRuleRecipeSuggestionsBuild(t *testing.T) {
	got := SuggestEdgeRuleRecipes(EdgeRuleRecipeSuggestionSignals{TotalRequests: 1000, BotRequests: 600, Routes: []EdgeRuleRecipeSuggestionRoute{
		{Method: "POST", Path: "/login", Requests: 900}, {Path: "/admin/", Requests: 30},
	}})
	if len(got) != 3 {
		t.Fatalf("suggestions = %+v, want bots, login and admin", got)
	}
	for _, sg := range got {
		values := map[string]string{}
		for k, v := range sg.Params {
			values[k] = v
		}
		if sg.Recipe == EdgeRuleRecipeAllowIPsOnly {
			values["cidrs"] = "203.0.113.7"
		}
		req, prob := BuildEdgeRuleRecipe(sg.Recipe, EdgeRuleAppHosts, values, false)
		if prob != nil {
			t.Fatalf("%s: %v", sg.Recipe, prob.Detail)
		}
		if sg.Recipe == EdgeRuleRecipeRateLimitPath && (req.MatchPath != "/login" || len(req.MatchMethods) != 1 || req.MatchMethods[0] != "POST") {
			t.Fatalf("rate-limit request = %+v", req)
		}
		if sg.Recipe == EdgeRuleRecipeAllowIPsOnly {
			if ok, err := MatchEdgeRulePath(req.MatchPath, "/admin"); err != nil || !ok {
				t.Fatalf("allow-ips-only path %q does not cover /admin: %v", req.MatchPath, err)
			}
		}
	}
}

func TestSplitEdgeRuleRecipeSuggestionRoute(t *testing.T) {
	for _, tc := range []struct{ label, method, path string }{
		{"POST /login", "POST", "/login"},
		{"/login", "", "/login"},
		{"GET /users/{id}", "GET", "/users/{id}"},
		{"weird label", "", "weird label"},
	} {
		if m, p := SplitEdgeRuleRecipeSuggestionRoute(tc.label); m != tc.method || p != tc.path {
			t.Errorf("split(%q) = %q %q, want %q %q", tc.label, m, p, tc.method, tc.path)
		}
	}
}

// adr: 20261020105000 — a scanner prefix the app really serves (a WordPress site's
// /wp-admin) is left out of the suggested list; secret paths never are,
// even when an SPA answers them with its index page.
func TestScannerSuggestionKeepsServedSoftware(t *testing.T) {
	got := SuggestEdgeRuleRecipes(EdgeRuleRecipeSuggestionSignals{Days: 7, Routes: []EdgeRuleRecipeSuggestionRoute{
		{Method: "GET", Path: "/wp-admin/", Requests: 400, ErrorRequests: 10},
		{Method: "GET", Path: "/.env", Requests: 30},
	}})
	if len(got) != 1 || got[0].Recipe != EdgeRuleRecipeBlockScannerPaths {
		t.Fatalf("suggestions = %+v", got)
	}
	paths := got[0].Params["paths"]
	if strings.Contains(paths, "/wp-") || !strings.Contains(paths, "/.env") || !strings.Contains(paths, "/.git/") {
		t.Fatalf("paths = %q, want the defaults without /wp-", paths)
	}
	if !strings.Contains(got[0].Reason, "430 requests in the last 7 days") || !strings.Contains(got[0].Reason, "left out") {
		t.Fatalf("reason = %q", got[0].Reason)
	}
	req, prob := BuildEdgeRuleRecipe(EdgeRuleRecipeBlockScannerPaths, EdgeRuleAppHosts, got[0].Params, false)
	if prob != nil || req.Match == nil || slices.Contains(req.Match.Values, "/wp-") {
		t.Fatalf("build: %+v %v", req.Match, prob)
	}
}

// adr: 831 — amendment 7: sign-in and /admin suggestions offer challenging
// browsers as a softer alternative, unless a challenge rule already covers
// the path.
func TestEdgeRuleRecipeSuggestionAlternatives(t *testing.T) {
	routes := []EdgeRuleRecipeSuggestionRoute{{Method: "POST", Path: "/login", Requests: 900}, {Path: "/admin", Requests: 40}}
	got := SuggestEdgeRuleRecipes(EdgeRuleRecipeSuggestionSignals{Days: 7, Routes: routes})
	alts := map[string]string{}
	for _, sg := range got {
		if sg.Alternative == nil {
			t.Fatalf("%s has no alternative", sg.Recipe)
		}
		if sg.Alternative.Recipe != EdgeRuleRecipeChallengePath || sg.Alternative.Title == "" {
			t.Fatalf("alternative = %+v", sg.Alternative)
		}
		alts[sg.Recipe] = sg.Alternative.Params["path"]
		if _, prob := BuildEdgeRuleRecipe(sg.Alternative.Recipe, EdgeRuleAppHosts, sg.Alternative.Params, false); prob != nil {
			t.Fatalf("alternative does not build: %v", prob.Detail)
		}
	}
	if alts[EdgeRuleRecipeRateLimitPath] != "/login" || alts[EdgeRuleRecipeAllowIPsOnly] != "/admin/**" {
		t.Fatalf("alternative paths = %v", alts)
	}
	covered := SuggestEdgeRuleRecipes(EdgeRuleRecipeSuggestionSignals{Days: 7, Routes: routes, Rules: []EdgeRuleRecipeSuggestionRule{
		{Kind: "challenge", MatchPath: "/**", Description: EdgeRuleRecipeDescription(EdgeRuleRecipeChallengePath), Enabled: true},
	}})
	for _, sg := range covered {
		if sg.Alternative != nil {
			t.Fatalf("%s offers a challenge the app already has: %+v", sg.Recipe, sg.Alternative)
		}
	}
}
