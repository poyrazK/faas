package api

// ADR-968: bot signals for edge-rule conditions. ua_family classifies the
// User-Agent header; verified_bot names a known crawler whose claim was
// confirmed against the client IP by forward-confirmed reverse DNS.

import (
	"strings"
	"time"
)

// User-agent families (the ua_family match field).
const (
	UAFamilyBrowser = "browser"
	UAFamilyMobile  = "mobile"
	UAFamilyBot     = "bot"
	UAFamilyTool    = "tool"
	UAFamilyOther   = "other"
)

// UAFamilies lists every ua_family value, for validation and docs.
var UAFamilies = []string{UAFamilyBrowser, UAFamilyMobile, UAFamilyBot, UAFamilyTool, UAFamilyOther}

// Bounds on verified-bot lookups (ADR-968 §3). A lookup that misses its
// deadline, or finds the in-flight budget spent, leaves the bot unverified.
const (
	EdgeRuleBotVerifyTimeout     = 300 * time.Millisecond
	EdgeRuleBotVerifyMaxInFlight = 64
	EdgeRuleBotVerifyTTL         = time.Hour
	EdgeRuleBotVerifyNegativeTTL = 10 * time.Minute
	EdgeRuleBotVerifyCacheSize   = 16384
	edgeRuleUAMaxClassifyBytes   = 512
)

// EdgeRuleKnownBot is a crawler the gateway can verify: a request whose
// User-Agent carries one of Tokens is that bot only when the client IP's
// reverse DNS name ends in one of DNSSuffixes and resolves back to the IP.
type EdgeRuleKnownBot struct {
	Name        string
	Tokens      []string // lowercase User-Agent substrings
	DNSSuffixes []string // lowercase, each starting with "."
}

// EdgeRuleKnownBots are the verifiable crawlers, from each operator's
// published verification guidance.
var EdgeRuleKnownBots = []EdgeRuleKnownBot{
	{Name: "googlebot", Tokens: []string{"googlebot", "google-inspectiontool", "adsbot-google", "mediapartners-google", "storebot-google", "googleother"}, DNSSuffixes: []string{".googlebot.com", ".google.com"}},
	{Name: "bingbot", Tokens: []string{"bingbot", "adidxbot", "bingpreview", "microsoftpreview"}, DNSSuffixes: []string{".search.msn.com"}},
	{Name: "applebot", Tokens: []string{"applebot"}, DNSSuffixes: []string{".applebot.apple.com"}},
	{Name: "yandexbot", Tokens: []string{"yandexbot", "yandeximages", "yandexmobilebot"}, DNSSuffixes: []string{".yandex.ru", ".yandex.net", ".yandex.com"}},
	{Name: "baiduspider", Tokens: []string{"baiduspider"}, DNSSuffixes: []string{".baidu.com", ".baidu.jp"}},
	{Name: "yahoo", Tokens: []string{"yahoo! slurp"}, DNSSuffixes: []string{".crawl.yahoo.net"}},
	{Name: "petalbot", Tokens: []string{"petalbot"}, DNSSuffixes: []string{".petalsearch.com"}},
}

// ClaimedEdgeRuleBot returns the known crawler a User-Agent claims to be.
// The claim is unverified: anyone can send any User-Agent.
func ClaimedEdgeRuleBot(userAgent string) (EdgeRuleKnownBot, bool) {
	if userAgent == "" {
		return EdgeRuleKnownBot{}, false
	}
	ua := strings.ToLower(clipUA(userAgent))
	for _, b := range EdgeRuleKnownBots {
		for _, t := range b.Tokens {
			if strings.Contains(ua, t) {
				return b, true
			}
		}
	}
	return EdgeRuleKnownBot{}, false
}

func knownEdgeRuleBot(name string) bool {
	for _, b := range EdgeRuleKnownBots {
		if b.Name == name {
			return true
		}
	}
	return false
}

var (
	uaBotMarkers = []string{
		"bot", "crawl", "spider", "slurp", "facebookexternalhit", "mediapartners-google",
		"bingpreview", "headlesschrome", "phantomjs", "lighthouse", "pingdom", "uptimerobot",
		"+http", "archiver", "scrapy",
	}
	uaToolPrefixes = []string{
		"curl/", "wget/", "httpie/", "python-requests/", "python-urllib/", "python-httpx/",
		"aiohttp/", "go-http-client/", "okhttp/", "java/", "apache-httpclient/", "libwww-perl/",
		"node-fetch", "axios/", "undici", "node", "postmanruntime/", "insomnia/", "ruby",
		"php/", "guzzlehttp/", "dart:io", "reqwest/", "hackney/", "k6/", "apachebench/",
		"hey/", "wrk", "powershell", "winhttp", "rest-client/", "faraday", "deno/", "bun/",
	}
	uaMobileMarkers = []string{"mobile", "android", "iphone", "ipad", "ipod"}
)

// ClassifyUserAgent returns the ua_family of a User-Agent header value, or
// "" when it is empty. Bot markers win over everything else, so a crawler
// that also names a browser engine is still a bot.
func ClassifyUserAgent(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(clipUA(userAgent)))
	if ua == "" {
		return ""
	}
	for _, m := range uaBotMarkers {
		if strings.Contains(ua, m) {
			return UAFamilyBot
		}
	}
	for _, p := range uaToolPrefixes {
		if strings.HasPrefix(ua, p) {
			return UAFamilyTool
		}
	}
	if strings.HasPrefix(ua, "mozilla/") || strings.HasPrefix(ua, "opera/") {
		for _, m := range uaMobileMarkers {
			if strings.Contains(ua, m) {
				return UAFamilyMobile
			}
		}
		return UAFamilyBrowser
	}
	return UAFamilyOther
}

// clipUA bounds classification work on oversized headers.
func clipUA(s string) string {
	if len(s) > edgeRuleUAMaxClassifyBytes {
		return s[:edgeRuleUAMaxClassifyBytes]
	}
	return s
}
