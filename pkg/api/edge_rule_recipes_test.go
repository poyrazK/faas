package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
)

// adr: 986 — every recipe expands to a valid create request, starts in log
// mode unless enforced, and carries its name and provenance.
func TestEdgeRuleRecipesBuildValidRequests(t *testing.T) {
	required := map[string]map[string]string{
		EdgeRuleRecipeAllowIPsOnly:   {"cidrs": "203.0.113.7, 198.51.100.0/24 2001:db8::1"},
		EdgeRuleRecipeBlockCountries: {"countries": "kp ir"},
	}
	for _, recipe := range EdgeRuleRecipes {
		t.Run(recipe.ID, func(t *testing.T) {
			req, prob := BuildEdgeRuleRecipe(recipe.ID, "api.example.com", required[recipe.ID], false)
			if prob != nil {
				t.Fatalf("build: %v", prob.Detail)
			}
			if req.Mode != EdgeRuleModeLog || req.Enabled == nil || !*req.Enabled {
				t.Fatalf("mode=%q enabled=%v, want enabled log mode", req.Mode, req.Enabled)
			}
			if req.MatchHost != "api.example.com" || req.Name != recipe.Title || !strings.Contains(req.Description, recipe.ID) || req.Kind != recipe.Kind {
				t.Fatalf("request = %+v", req)
			}
			if _, err := CompileEdgeRuleMatch(req.Match); err != nil {
				t.Fatalf("match does not compile: %v", err)
			}
			switch req.Kind {
			case "ip":
				var a EdgeRuleIPAction
				if err := json.Unmarshal(req.Action, &a); err != nil || a.Validate() != nil || len(a.Deny) != 2 || req.Match == nil {
					t.Fatalf("ip action %s: %v", req.Action, err)
				}
			case "throttle":
				var a EdgeRuleThrottleAction
				if err := json.Unmarshal(req.Action, &a); err != nil || a.KeyBy != "ip" || a.RequestsPerSecond <= 0 || a.Burst < 1 {
					t.Fatalf("throttle action %s: %v", req.Action, err)
				}
			case "challenge":
				var a EdgeRuleChallengeAction
				if err := json.Unmarshal(req.Action, &a); err != nil || a.Validate() != nil || req.Match == nil {
					t.Fatalf("challenge action %s: %v", req.Action, err)
				}
			default:
				t.Fatalf("unexpected kind %q", req.Kind)
			}
			enforced, _ := BuildEdgeRuleRecipe(recipe.ID, "api.example.com", required[recipe.ID], true)
			if enforced.Mode != EdgeRuleModeEnforce {
				t.Fatalf("enforce mode = %q", enforced.Mode)
			}
		})
	}
}

// adr: 986 — blocking recipes' conditions match exactly the requests the
// recipe should block, so a log-mode count is a would-block count.
func TestEdgeRuleRecipeConditions(t *testing.T) {
	compile := func(id string, values map[string]string) *EdgeRuleMatchProgram {
		t.Helper()
		req, prob := BuildEdgeRuleRecipe(id, "h.example", values, false)
		if prob != nil {
			t.Fatal(prob.Detail)
		}
		p, err := CompileEdgeRuleMatch(req.Match)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ua := func(s string) http.Header { return http.Header{"User-Agent": {s}} }
	verified := func(name string) func() string { return func() string { return name } }
	const googlebot = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"

	bots := compile(EdgeRuleRecipeBlockUnverifiedBots, nil)
	allow := compile(EdgeRuleRecipeAllowIPsOnly, map[string]string{"cidrs": "203.0.113.7 198.51.100.0/24"})
	countries := compile(EdgeRuleRecipeBlockCountries, map[string]string{"countries": "KP,IR"})
	scanners := compile(EdgeRuleRecipeBlockScannerPaths, nil)
	challenge := compile(EdgeRuleRecipeChallengePath, map[string]string{"path": "/login"})
	custom := compile(EdgeRuleRecipeBlockScannerPaths, map[string]string{"paths": "/.env /old-admin/"})
	training := compile(EdgeRuleRecipeControlAICrawlers, nil)
	allAI := compile(EdgeRuleRecipeControlAICrawlers, map[string]string{"purposes": "all"})
	const gptbot = "Mozilla/5.0 (compatible; GPTBot/1.1; +https://openai.com/gptbot)"
	const chatgptUser = "Mozilla/5.0 (compatible; ChatGPT-User/1.0; +https://openai.com/bot)"
	for _, tc := range []struct {
		name  string
		p     *EdgeRuleMatchProgram
		in    EdgeRuleMatchInput
		block bool
	}{
		{"spoofed googlebot", bots, EdgeRuleMatchInput{Headers: ua(googlebot), VerifiedBot: verified("")}, true},
		{"verified googlebot", bots, EdgeRuleMatchInput{Headers: ua(googlebot), VerifiedBot: verified("googlebot")}, false},
		{"browser", bots, EdgeRuleMatchInput{Headers: ua("Mozilla/5.0 (Windows NT 10.0) Chrome/129.0")}, false},
		{"listed IP", allow, EdgeRuleMatchInput{ClientIP: net.ParseIP("203.0.113.7")}, false},
		{"listed range", allow, EdgeRuleMatchInput{ClientIP: net.ParseIP("198.51.100.42")}, false},
		{"other IP", allow, EdgeRuleMatchInput{ClientIP: net.ParseIP("192.0.2.1")}, true},
		{"unknown IP", allow, EdgeRuleMatchInput{}, true},
		{"blocked country", countries, EdgeRuleMatchInput{Country: "KP"}, true},
		{"other country", countries, EdgeRuleMatchInput{Country: "DE"}, false},
		{"unknown country", countries, EdgeRuleMatchInput{}, false},
		// adr: 986 amendment — scanner paths by prefix, nothing else.
		{"env file", scanners, EdgeRuleMatchInput{Path: "/.env.production"}, true},
		{"git metadata", scanners, EdgeRuleMatchInput{Path: "/.git/config"}, true},
		{"wordpress login", scanners, EdgeRuleMatchInput{Path: "/wp-login.php"}, true},
		{"app route", scanners, EdgeRuleMatchInput{Path: "/api/users"}, false},
		{"github path", scanners, EdgeRuleMatchInput{Path: "/.github/workflows"}, false},
		{"custom prefix", custom, EdgeRuleMatchInput{Path: "/old-admin/login"}, true},
		{"default dropped", custom, EdgeRuleMatchInput{Path: "/wp-login.php"}, false},
		// adr: 831 — amendment 7: challenge-path spares verified crawlers.
		{"challenge browser", challenge, EdgeRuleMatchInput{Headers: ua("Mozilla/5.0 (Windows NT 10.0) Chrome/129.0")}, true},
		{"challenge spoofed crawler", challenge, EdgeRuleMatchInput{Headers: ua(googlebot), VerifiedBot: verified("")}, true},
		{"challenge verified crawler", challenge, EdgeRuleMatchInput{Headers: ua(googlebot), VerifiedBot: verified("googlebot")}, false},
		// adr: 20261022083601 — AI crawlers by purpose; training by default.
		{"training crawler", training, EdgeRuleMatchInput{Headers: ua(gptbot)}, true},
		{"assistant fetch kept", training, EdgeRuleMatchInput{Headers: ua(chatgptUser)}, false},
		{"search engine kept", training, EdgeRuleMatchInput{Headers: ua(googlebot), VerifiedBot: verified("googlebot")}, false},
		{"browser kept", training, EdgeRuleMatchInput{Headers: ua("Mozilla/5.0 (Windows NT 10.0) Chrome/129.0")}, false},
		{"all purposes", allAI, EdgeRuleMatchInput{Headers: ua(chatgptUser)}, true},
	} {
		if got := tc.p.Matches(tc.in); got != tc.block {
			t.Errorf("%s: matches=%v, want %v", tc.name, got, tc.block)
		}
	}
}

// adr: 986 — bad recipe input fails before any request is sent.
func TestEdgeRuleRecipeRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		id     string
		host   string
		values map[string]string
	}{
		"unknown recipe":      {"nope", "h", nil},
		"no host":             {EdgeRuleRecipeBlockUnverifiedBots, " ", nil},
		"unknown parameter":   {EdgeRuleRecipeBlockUnverifiedBots, "h", map[string]string{"cidrs": "1.2.3.4"}},
		"missing cidrs":       {EdgeRuleRecipeAllowIPsOnly, "h", nil},
		"bad cidr":            {EdgeRuleRecipeAllowIPsOnly, "h", map[string]string{"cidrs": "10.0.0.0/33"}},
		"missing countries":   {EdgeRuleRecipeBlockCountries, "h", map[string]string{"countries": " , "}},
		"reserved country":    {EdgeRuleRecipeBlockCountries, "h", map[string]string{"countries": "ZZ"}},
		"zero rate":           {EdgeRuleRecipeRateLimitPath, "h", map[string]string{"per_minute": "0"}},
		"rate not a number":   {EdgeRuleRecipeRateLimitPath, "h", map[string]string{"per_minute": "ten"}},
		"burst out of bounds": {EdgeRuleRecipeRateLimitPath, "h", map[string]string{"burst": "100000"}},
		"scanner not a path":  {EdgeRuleRecipeBlockScannerPaths, "h", map[string]string{"paths": "/.env wp-admin"}},
		"scanner root prefix": {EdgeRuleRecipeBlockScannerPaths, "h", map[string]string{"paths": "/"}},
		"unknown ai purpose":  {EdgeRuleRecipeControlAICrawlers, "h", map[string]string{"purposes": "scraping"}},
		"empty ai purposes":   {EdgeRuleRecipeControlAICrawlers, "h", map[string]string{"purposes": " , "}},
		"unknown ai action":   {EdgeRuleRecipeControlAICrawlers, "h", map[string]string{"action": "tarpit"}},
	} {
		if _, prob := BuildEdgeRuleRecipe(tc.id, tc.host, tc.values, false); prob == nil {
			t.Errorf("%s: built, want error", name)
		}
	}
	req, prob := BuildEdgeRuleRecipe(EdgeRuleRecipeRateLimitPath, "h", map[string]string{"per_minute": "30", "methods": "post, put"}, false)
	if prob != nil {
		t.Fatal(prob.Detail)
	}
	var a EdgeRuleThrottleAction
	_ = json.Unmarshal(req.Action, &a)
	if a.RequestsPerSecond != 0.5 || a.Burst != 30 || strings.Join(req.MatchMethods, ",") != "POST,PUT" || req.MatchPath != "/login" {
		t.Fatalf("rate-limit request = %+v action = %+v", req, a)
	}
}

// adr: 20261022083601 — the AI crawler recipe blocks with a deny-all ip
// rule or challenges with a challenge rule, de-duplicating purposes.
func TestEdgeRuleRecipeControlAICrawlersActions(t *testing.T) {
	block, prob := BuildEdgeRuleRecipe(EdgeRuleRecipeControlAICrawlers, "h", map[string]string{"purposes": "training, search, training"}, false)
	if prob != nil {
		t.Fatal(prob.Detail)
	}
	if block.Kind != "ip" || block.Mode != EdgeRuleModeLog || block.Match == nil || block.Match.Field != "ai_crawler_purpose" ||
		strings.Join(block.Match.Values, ",") != "training,search" {
		t.Fatalf("block request = %+v", block)
	}
	challenge, prob := BuildEdgeRuleRecipe(EdgeRuleRecipeControlAICrawlers, "h", map[string]string{"action": "Challenge"}, true)
	if prob != nil {
		t.Fatal(prob.Detail)
	}
	var action EdgeRuleChallengeAction
	if challenge.Kind != "challenge" || challenge.Mode != EdgeRuleModeEnforce || json.Unmarshal(challenge.Action, &action) != nil {
		t.Fatalf("challenge request = %+v", challenge)
	}
}
