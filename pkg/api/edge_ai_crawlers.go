package api

// AI crawlers (ADR-20261022083601): the crawlers AI companies run, grouped
// by what their fetches are for. The ai_crawler and ai_crawler_purpose match
// fields read the User-Agent claim, which anyone can send, so they suit
// blocking. verified_ai_crawler is set only when the client IP is in the
// operator's published IP ranges, so it suits letting a crawler through.
// The catalog is code: adding a crawler is a reviewed change (as ADR-973
// decided for search crawlers). Only the published ranges are refreshed
// at run time.

import (
	"strings"
	"time"
)

// AI crawler purposes (the ai_crawler_purpose match field).
const (
	// AICrawlerPurposeTraining collects pages to train models.
	AICrawlerPurposeTraining = "training"
	// AICrawlerPurposeSearch indexes pages for an AI search product.
	AICrawlerPurposeSearch = "search"
	// AICrawlerPurposeAssistant fetches a page because a user asked an
	// assistant to.
	AICrawlerPurposeAssistant = "assistant"
)

// AICrawlerPurposes lists every ai_crawler_purpose value.
var AICrawlerPurposes = []string{AICrawlerPurposeTraining, AICrawlerPurposeSearch, AICrawlerPurposeAssistant}

// EdgeAICrawler is one AI crawler. A User-Agent containing one of Tokens
// claims to be it. RangesURL is the operator's published IP ranges; a
// crawler without one can be matched by its claim but never verified.
type EdgeAICrawler struct {
	Name      string
	Operator  string
	Purpose   string
	Tokens    []string // lowercase User-Agent substrings
	RangesURL string
}

// EdgeAICrawlers is the catalog, from each operator's crawler
// documentation. No token is a substring of another crawler's User-Agent.
var EdgeAICrawlers = []EdgeAICrawler{
	{Name: "gptbot", Operator: "OpenAI", Purpose: AICrawlerPurposeTraining, Tokens: []string{"gptbot"}, RangesURL: "https://openai.com/gptbot.json"},
	{Name: "oai-searchbot", Operator: "OpenAI", Purpose: AICrawlerPurposeSearch, Tokens: []string{"oai-searchbot"}, RangesURL: "https://openai.com/searchbot.json"},
	{Name: "chatgpt-user", Operator: "OpenAI", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"chatgpt-user"}, RangesURL: "https://openai.com/chatgpt-user.json"},
	{Name: "claudebot", Operator: "Anthropic", Purpose: AICrawlerPurposeTraining, Tokens: []string{"claudebot", "anthropic-ai"}},
	{Name: "claude-searchbot", Operator: "Anthropic", Purpose: AICrawlerPurposeSearch, Tokens: []string{"claude-searchbot"}},
	{Name: "claude-user", Operator: "Anthropic", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"claude-user", "claude-web"}},
	{Name: "perplexitybot", Operator: "Perplexity", Purpose: AICrawlerPurposeSearch, Tokens: []string{"perplexitybot"}, RangesURL: "https://www.perplexity.ai/perplexitybot.json"},
	{Name: "perplexity-user", Operator: "Perplexity", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"perplexity-user"}, RangesURL: "https://www.perplexity.ai/perplexity-user.json"},
	{Name: "mistralai-user", Operator: "Mistral AI", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"mistralai-user"}, RangesURL: "https://mistral.ai/mistralai-user-ips.json"},
	{Name: "duckassistbot", Operator: "DuckDuckGo", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"duckassistbot"}, RangesURL: "https://duckduckgo.com/duckassistbot.json"},
	{Name: "meta-externalagent", Operator: "Meta", Purpose: AICrawlerPurposeTraining, Tokens: []string{"meta-externalagent"}},
	{Name: "meta-externalfetcher", Operator: "Meta", Purpose: AICrawlerPurposeAssistant, Tokens: []string{"meta-externalfetcher"}},
	{Name: "ccbot", Operator: "Common Crawl", Purpose: AICrawlerPurposeTraining, Tokens: []string{"ccbot"}},
	{Name: "bytespider", Operator: "ByteDance", Purpose: AICrawlerPurposeTraining, Tokens: []string{"bytespider"}},
	{Name: "amazonbot", Operator: "Amazon", Purpose: AICrawlerPurposeTraining, Tokens: []string{"amazonbot"}},
	{Name: "ai2bot", Operator: "Allen Institute for AI", Purpose: AICrawlerPurposeTraining, Tokens: []string{"ai2bot"}},
	{Name: "cohere-training-data-crawler", Operator: "Cohere", Purpose: AICrawlerPurposeTraining, Tokens: []string{"cohere-training-data-crawler"}},
	{Name: "diffbot", Operator: "Diffbot", Purpose: AICrawlerPurposeTraining, Tokens: []string{"diffbot"}},
	{Name: "imagesiftbot", Operator: "ImageSift", Purpose: AICrawlerPurposeTraining, Tokens: []string{"imagesiftbot"}},
	{Name: "omgilibot", Operator: "Webz.io", Purpose: AICrawlerPurposeTraining, Tokens: []string{"omgilibot", "omgili/"}},
	{Name: "pangubot", Operator: "Huawei", Purpose: AICrawlerPurposeTraining, Tokens: []string{"pangubot"}},
	{Name: "timpibot", Operator: "Timpi", Purpose: AICrawlerPurposeTraining, Tokens: []string{"timpibot"}},
	{Name: "youbot", Operator: "You.com", Purpose: AICrawlerPurposeSearch, Tokens: []string{"youbot"}},
}

// ClaimedAICrawler returns the AI crawler a User-Agent claims to be. The
// claim is unverified: anyone can send any User-Agent.
func ClaimedAICrawler(userAgent string) (EdgeAICrawler, bool) {
	if userAgent == "" {
		return EdgeAICrawler{}, false
	}
	return ClaimedAICrawlerLower(strings.ToLower(clipUA(userAgent)))
}

// ClaimedAICrawlerLower is ClaimedAICrawler for a User-Agent the caller has
// already lowercased, so a hot path that lowercases once does not do it
// twice.
func ClaimedAICrawlerLower(ua string) (EdgeAICrawler, bool) {
	ua = clipUA(ua)
	for _, c := range EdgeAICrawlers {
		for _, t := range c.Tokens {
			if strings.Contains(ua, t) {
				return c, true
			}
		}
	}
	return EdgeAICrawler{}, false
}

// EdgeAICrawlerByName returns the catalog entry named name.
func EdgeAICrawlerByName(name string) (EdgeAICrawler, bool) {
	for _, c := range EdgeAICrawlers {
		if c.Name == name {
			return c, true
		}
	}
	return EdgeAICrawler{}, false
}

// EdgeAICrawlerNames lists the catalog names, optionally only those that
// publish IP ranges and so can be verified.
func EdgeAICrawlerNames(verifiableOnly bool) []string {
	names := make([]string, 0, len(EdgeAICrawlers))
	for _, c := range EdgeAICrawlers {
		if !verifiableOnly || c.RangesURL != "" {
			names = append(names, c.Name)
		}
	}
	return names
}

// EdgeCrawlerRangeFeed is one published IP-range feed the control plane
// refreshes: an AI crawler's, or a search crawler's (ADR-973), whose ranges
// verify it without a DNS lookup.
type EdgeCrawlerRangeFeed struct {
	Crawler string
	URL     string
}

// EdgeCrawlerRangeFeeds lists every feed, search crawlers first.
func EdgeCrawlerRangeFeeds() []EdgeCrawlerRangeFeed {
	var feeds []EdgeCrawlerRangeFeed
	for _, b := range EdgeRuleKnownBots {
		if b.RangesURL != "" {
			feeds = append(feeds, EdgeCrawlerRangeFeed{Crawler: b.Name, URL: b.RangesURL})
		}
	}
	for _, c := range EdgeAICrawlers {
		if c.RangesURL != "" {
			feeds = append(feeds, EdgeCrawlerRangeFeed{Crawler: c.Name, URL: c.RangesURL})
		}
	}
	return feeds
}

// Published-range bounds (ADR-20261022083601). A feed outside them is
// refused whole, and the previous ranges stay.
const (
	// EdgeCrawlerRangesRefreshInterval is how often apid fetches the feeds.
	EdgeCrawlerRangesRefreshInterval = 12 * time.Hour
	// EdgeCrawlerRangesStaleAfter is how old stored ranges may be before
	// gateways stop verifying with them, so a feed that stopped refreshing
	// cannot vouch for addresses its operator has since given up.
	EdgeCrawlerRangesStaleAfter = 7 * 24 * time.Hour
	// EdgeCrawlerRangesReload is how often a gateway rereads them.
	EdgeCrawlerRangesReload = 10 * time.Minute
	// EdgeCrawlerRangesMaxPrefixes and EdgeCrawlerRangesMaxBodyBytes bound
	// one feed.
	EdgeCrawlerRangesMaxPrefixes  = 5000
	EdgeCrawlerRangesMaxBodyBytes = 1 << 20
	// EdgeCrawlerRangesMinIPv4Bits and EdgeCrawlerRangesMinIPv6Bits refuse
	// implausibly broad prefixes (a crawler feed listing 0.0.0.0/0 would
	// verify every client).
	EdgeCrawlerRangesMinIPv4Bits = 16
	EdgeCrawlerRangesMinIPv6Bits = 32
)

// EdgeRuleMatchUsesAICrawlerFields reports whether expr tests ai_crawler,
// ai_crawler_purpose or verified_ai_crawler anywhere, the fields the
// edge_ai_crawlers platform flag releases (ADR-20261022083601 amendment 1).
func EdgeRuleMatchUsesAICrawlerFields(expr *EdgeRuleMatchExpr) bool {
	if expr == nil {
		return false
	}
	switch expr.Field {
	case "ai_crawler", "ai_crawler_purpose", "verified_ai_crawler":
		return true
	}
	for i := range expr.All {
		if EdgeRuleMatchUsesAICrawlerFields(&expr.All[i]) {
			return true
		}
	}
	for i := range expr.Any {
		if EdgeRuleMatchUsesAICrawlerFields(&expr.Any[i]) {
			return true
		}
	}
	return EdgeRuleMatchUsesAICrawlerFields(expr.Not)
}
