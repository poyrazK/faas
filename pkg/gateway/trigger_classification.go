package gateway

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// TriggerClass is the bounded classification attached to request-driven wakes.
// Keep this set small: it is used in wake analytics and event payloads.
type TriggerClass string

const (
	TriggerClassUser       TriggerClass = "user"
	TriggerClassMonitor    TriggerClass = "monitor"
	TriggerClassCrawler    TriggerClass = "crawler"
	TriggerClassPreviewBot TriggerClass = "preview_bot"
	TriggerClassUnknown    TriggerClass = "unknown"
)

// crawlerUAFamilies is deliberately an ordered, substring table. More specific
// preview bots must win before broad crawler/monitor matches.
var crawlerUAFamilies = []struct {
	class   TriggerClass
	needles []string
}{
	{TriggerClassPreviewBot, []string{"facebookexternalhit", "facebot", "twitterbot", "slackbot", "linkedinbot", "discordbot", "telegrambot", "whatsapp"}},
	{TriggerClassMonitor, []string{"uptimerobot", "pingdom", "statuscake", "better uptime", "betteruptime", "freshping", "site24x7", "datadog synthetics", "checkly", "uptime kuma", "uptime-kuma"}},
	{TriggerClassCrawler, []string{"googlebot", "bingbot", "duckduckbot", "yandexbot", "baiduspider", "applebot", "petalbot", "semrushbot", "ahrefsbot", "mj12bot", "dotbot", "crawler", "spider", "bot/"}},
}

// ClassifyWakeTrigger maps a User-Agent to the closed trigger_class set.
// Empty and unrecognised agents are unknown; ordinary browser agents are users.
func ClassifyWakeTrigger(r *http.Request) TriggerClass {
	if r == nil {
		return TriggerClassUnknown
	}
	return classifyUserAgent(r.Header.Get("User-Agent"))
}

func classifyUserAgent(raw string) TriggerClass {
	ua := strings.ToLower(strings.TrimSpace(raw))
	if ua == "" {
		return TriggerClassUnknown
	}
	for _, family := range crawlerUAFamilies {
		for _, needle := range family.needles {
			if strings.Contains(ua, needle) {
				return family.class
			}
		}
	}
	for _, needle := range []string{"mozilla/", "chrome/", "chromium/", "firefox/", "safari/", "edg/", "opera/"} {
		if strings.Contains(ua, needle) {
			return TriggerClassUser
		}
	}
	return TriggerClassUnknown
}

func isNonUserTriggerClass(class TriggerClass) bool {
	return class == TriggerClassMonitor || class == TriggerClassCrawler || class == TriggerClassPreviewBot
}

func normalizeCrawlerPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "cached", "block":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "wake"
	}
}

func writeCrawlerPolicyResponse(w http.ResponseWriter, policy string) {
	w.Header().Set("Retry-After", "60")
	code := "crawler_cached_miss"
	detail := "known monitor/crawler traffic is not allowed to wake a parked app"
	if policy == "block" {
		code = "crawler_blocked"
		detail = "known monitor/crawler traffic is blocked by the app policy"
	}
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, code,
		"Crawler wake suppressed", detail))
}
