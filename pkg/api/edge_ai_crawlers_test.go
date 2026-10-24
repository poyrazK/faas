package api

import (
	"net/http"
	"strings"
	"testing"
)

// adr: 20261022083601 — operators' documented User-Agents map to the right
// crawler and purpose, browsers and search crawlers to none.
func TestClaimedAICrawler(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.1; +https://openai.com/gptbot)":                      "gptbot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; OAI-SearchBot/1.0; +https://openai.com/searchbot":            "oai-searchbot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot":                   "chatgpt-user",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)":                     "claudebot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-SearchBot/1.0; +Claude-SearchBot@anthropic.com)":       "claude-searchbot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-User/1.0; +Claude-User@anthropic.com)":                 "claude-user",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; PerplexityBot/1.0; +https://perplexity.ai/perplexitybot)":     "perplexitybot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Perplexity-User/1.0; +https://perplexity.ai/perplexity-user)": "perplexity-user",
		"CCBot/2.0 (https://commoncrawl.org/faq/)": "ccbot",
		"Mozilla/5.0 (Linux; Android 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; Bytespider; spider-feedback@bytedance.com)": "bytespider",
		"meta-externalagent/1.1 (+https://developers.facebook.com/docs/sharing/webmasters/crawler)":                                                            "meta-externalagent",
		"DuckAssistBot/1.2; (+http://duckduckgo.com/duckassistbot.html)":                                                                                       "duckassistbot",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)":                                                                             "",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36":                                          "",
		"": "",
	} {
		c, ok := ClaimedAICrawler(ua)
		if got := c.Name; got != want || ok != (want != "") {
			t.Errorf("%q: got %q (%v), want %q", ua, got, ok, want)
		}
	}
}

// adr: 20261022083601 — the catalog is internally consistent: unique names,
// a known purpose, lowercase tokens no other crawler's name contains, and
// only HTTPS range feeds.
func TestEdgeAICrawlerCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range EdgeRuleKnownBots {
		seen[b.Name] = true
	}
	for _, c := range EdgeAICrawlers {
		if seen[c.Name] || c.Name != strings.ToLower(c.Name) {
			t.Errorf("%s: duplicate or not lowercase", c.Name)
		}
		seen[c.Name] = true
		if !containsMatchValue(AICrawlerPurposes, c.Purpose) || c.Operator == "" || len(c.Tokens) == 0 {
			t.Errorf("%s: purpose %q, operator %q, %d tokens", c.Name, c.Purpose, c.Operator, len(c.Tokens))
		}
		if c.RangesURL != "" && !strings.HasPrefix(c.RangesURL, "https://") {
			t.Errorf("%s: range feed %q is not https", c.Name, c.RangesURL)
		}
		for _, tok := range c.Tokens {
			if tok != strings.ToLower(tok) {
				t.Errorf("%s: token %q not lowercase", c.Name, tok)
			}
			if got, _ := ClaimedAICrawler(tok); got.Name != c.Name {
				t.Errorf("token %q of %s claims %q first", tok, c.Name, got.Name)
			}
		}
	}
	feeds := EdgeCrawlerRangeFeeds()
	if len(feeds) == 0 || feeds[0].Crawler != "googlebot" {
		t.Fatalf("feeds = %+v, want search crawlers first", feeds)
	}
	for _, f := range feeds {
		if !strings.HasPrefix(f.URL, "https://") {
			t.Errorf("feed %s: %q is not https", f.Crawler, f.URL)
		}
	}
}

// adr: 20261022083601 — ai_crawler and ai_crawler_purpose read the
// User-Agent claim; verified_ai_crawler reads the verifier only when a rule
// evaluates it; closed value sets reject typos and crawlers that can never
// verify.
func TestEdgeRuleMatchAICrawlerFields(t *testing.T) {
	headers := func(ua string) http.Header { return http.Header{"User-Agent": {ua}} }
	gpt, browser := headers("Mozilla/5.0 (compatible; GPTBot/1.1)"), headers("Mozilla/5.0 Chrome/129.0")
	calls := 0
	verified := func(name string) func() string { return func() string { calls++; return name } }
	cases := []struct {
		expr EdgeRuleMatchExpr
		in   EdgeRuleMatchInput
		want bool
	}{
		{EdgeRuleMatchExpr{Field: "ai_crawler", Op: "eq", Value: "GPTBot"}, EdgeRuleMatchInput{Headers: gpt}, true},
		{EdgeRuleMatchExpr{Field: "ai_crawler", Op: "exists"}, EdgeRuleMatchInput{Headers: browser}, false},
		{EdgeRuleMatchExpr{Field: "ai_crawler_purpose", Op: "eq", Value: "training"}, EdgeRuleMatchInput{Headers: gpt}, true},
		{EdgeRuleMatchExpr{Field: "ai_crawler_purpose", Op: "in", Values: []string{"search", "assistant"}}, EdgeRuleMatchInput{Headers: gpt}, false},
		{EdgeRuleMatchExpr{Field: "ai_crawler_purpose", Op: "missing"}, EdgeRuleMatchInput{Headers: browser}, true},
		{EdgeRuleMatchExpr{Field: "verified_ai_crawler", Op: "eq", Value: "gptbot"}, EdgeRuleMatchInput{Headers: gpt, VerifiedAICrawler: verified("gptbot")}, true},
		{EdgeRuleMatchExpr{Field: "verified_ai_crawler", Op: "missing"}, EdgeRuleMatchInput{Headers: gpt, VerifiedAICrawler: verified("")}, true},
		{EdgeRuleMatchExpr{Field: "verified_ai_crawler", Op: "exists"}, EdgeRuleMatchInput{Headers: gpt}, false},
		// Block impersonators: claims GPTBot, not from OpenAI's ranges.
		{EdgeRuleMatchExpr{All: []EdgeRuleMatchExpr{{Field: "ai_crawler", Op: "eq", Value: "gptbot"}, {Field: "verified_ai_crawler", Op: "missing"}}},
			EdgeRuleMatchInput{Headers: gpt, VerifiedAICrawler: verified("")}, true},
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
	p, err := CompileEdgeRuleMatch(&EdgeRuleMatchExpr{Field: "ai_crawler", Op: "exists"})
	if err != nil {
		t.Fatal(err)
	}
	p.Matches(EdgeRuleMatchInput{Headers: gpt, VerifiedAICrawler: verified("gptbot")})
	if calls != 0 {
		t.Fatalf("verified_ai_crawler resolved %d times for a rule that does not read it", calls)
	}
	for _, bad := range []EdgeRuleMatchExpr{
		{Field: "ai_crawler", Op: "eq", Value: "gpt-bot"},
		{Field: "ai_crawler", Op: "contains", Value: "gpt"},
		{Field: "ai_crawler_purpose", Op: "eq", Value: "scraping"},
		{Field: "verified_ai_crawler", Op: "eq", Value: "nobody"},
	} {
		if _, err := CompileEdgeRuleMatch(&bad); err == nil {
			t.Errorf("%+v compiled, want error", bad)
		}
	}
	_, err = CompileEdgeRuleMatch(&EdgeRuleMatchExpr{Field: "verified_ai_crawler", Op: "eq", Value: "claudebot"})
	if err == nil || !strings.Contains(err.Error(), "publishes no IP ranges") {
		t.Fatalf("verified_ai_crawler claudebot: %v, want the no-ranges explanation", err)
	}
}
