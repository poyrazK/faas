package api

import (
	"net/http"
	"testing"
)

// adr: 968 — the User-Agent classifier sorts common clients into families.
func TestClassifyUserAgent(t *testing.T) {
	for ua, want := range map[string]string{
		"":    "",
		"   ": "",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36":       UAFamilyBrowser,
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15": UAFamilyBrowser,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148":                          UAFamilyMobile,
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/129.0 Mobile Safari/537.36":                      UAFamilyMobile,
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)":                                            UAFamilyBot,
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)":       UAFamilyBot,
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 HeadlessChrome/129.0 Safari/537.36":                              UAFamilyBot,
		"facebookexternalhit/1.1": UAFamilyBot,
		"curl/8.7.1":              UAFamilyTool,
		"python-requests/2.32.3":  UAFamilyTool,
		"Go-http-client/2.0":      UAFamilyTool,
		"Wget/1.21.4":             UAFamilyTool,
		"okhttp/4.12.0":           UAFamilyTool,
		"MyCustomApp/1.0":         UAFamilyOther,
	} {
		if got := ClassifyUserAgent(ua); got != want {
			t.Errorf("ClassifyUserAgent(%q) = %q, want %q", ua, got, want)
		}
	}
}

// adr: 968 — a User-Agent claims a known crawler by token; the claim alone
// is not verification.
func TestClaimedEdgeRuleBot(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)":            "googlebot",
		"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)":             "bingbot",
		"Mozilla/5.0 (Macintosh) Applebot/0.1":                                                "applebot",
		"Mozilla/5.0 (compatible; Yahoo! Slurp; http://help.yahoo.com/help/us/ysearch/slurp)": "yahoo",
		"curl/8.7.1": "",
		"":           "",
	} {
		b, ok := ClaimedEdgeRuleBot(ua)
		if ok != (want != "") || b.Name != want {
			t.Errorf("ClaimedEdgeRuleBot(%q) = %q,%v, want %q", ua, b.Name, ok, want)
		}
	}
	for _, b := range EdgeRuleKnownBots {
		for _, s := range b.DNSSuffixes {
			if s == "" || s[0] != '.' {
				t.Errorf("%s: DNS suffix %q must start with a dot", b.Name, s)
			}
		}
	}
}

// adr: 968 — ua_family and verified_bot match over closed value sets;
// verified_bot is only resolved when evaluated, and unknown values fail
// to compile.
func TestEdgeRuleMatchBotFields(t *testing.T) {
	headers := func(ua string) http.Header { return http.Header{"User-Agent": {ua}} }
	calls := 0
	verified := func(name string) func() string {
		return func() string { calls++; return name }
	}
	unverifiedBots := EdgeRuleMatchExpr{All: []EdgeRuleMatchExpr{{Field: "ua_family", Op: "eq", Value: "bot"}, {Field: "verified_bot", Op: "missing"}}}
	cases := []struct {
		expr EdgeRuleMatchExpr
		in   EdgeRuleMatchInput
		want bool
	}{
		{EdgeRuleMatchExpr{Field: "ua_family", Op: "eq", Value: "tool"}, EdgeRuleMatchInput{Headers: headers("curl/8.7.1")}, true},
		{EdgeRuleMatchExpr{Field: "ua_family", Op: "in", Values: []string{"BOT", "tool"}}, EdgeRuleMatchInput{Headers: headers("Googlebot/2.1")}, true},
		{EdgeRuleMatchExpr{Field: "ua_family", Op: "ne", Value: "browser"}, EdgeRuleMatchInput{Headers: headers("Mozilla/5.0 (X11) Firefox/131.0")}, false},
		{EdgeRuleMatchExpr{Field: "ua_family", Op: "missing"}, EdgeRuleMatchInput{Headers: http.Header{}}, true},
		{EdgeRuleMatchExpr{Field: "verified_bot", Op: "eq", Value: "googlebot"}, EdgeRuleMatchInput{VerifiedBot: verified("googlebot")}, true},
		{EdgeRuleMatchExpr{Field: "verified_bot", Op: "exists"}, EdgeRuleMatchInput{VerifiedBot: verified("")}, false},
		{EdgeRuleMatchExpr{Field: "verified_bot", Op: "missing"}, EdgeRuleMatchInput{}, true},
		// The documented "block unverified bots" pair.
		{unverifiedBots, EdgeRuleMatchInput{Headers: headers("Googlebot/2.1"), VerifiedBot: verified("")}, true},
		{unverifiedBots, EdgeRuleMatchInput{Headers: headers("Googlebot/2.1"), VerifiedBot: verified("googlebot")}, false},
	}
	for i, tc := range cases {
		p, err := CompileEdgeRuleMatch(&tc.expr)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if got := p.Matches(tc.in); got != tc.want {
			t.Errorf("case %d (%+v): got %v, want %v", i, tc.expr, got, tc.want)
		}
	}
	calls = 0
	p, err := CompileEdgeRuleMatch(&EdgeRuleMatchExpr{Field: "ua_family", Op: "eq", Value: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	p.Matches(EdgeRuleMatchInput{Headers: headers("curl/8"), VerifiedBot: verified("googlebot")})
	if calls != 0 {
		t.Fatalf("verified_bot resolved %d times for a rule that does not read it", calls)
	}
	lists := EdgeRuleLists{"names": mustEdgeRuleList(t, EdgeRuleListKindString, "x")}
	for _, bad := range []EdgeRuleMatchExpr{
		{Field: "ua_family", Op: "eq", Value: "robot"},
		{Field: "ua_family", Op: "contains", Value: "bot"},
		{Field: "verified_bot", Op: "eq", Value: "duckduckbot"},
		{Field: "verified_bot", Op: "regex", Value: ".*"},
		{Field: "ua_family", Op: "in_list", List: "names"},
	} {
		if _, err := CompileEdgeRuleMatchWithLists(&bad, lists); err == nil {
			t.Errorf("%+v compiled, want error", bad)
		}
	}
}
