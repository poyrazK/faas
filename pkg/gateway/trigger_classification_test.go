package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestClassifyUserAgent(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want TriggerClass
	}{
		{"empty", "", TriggerClassUnknown},
		{"google", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", TriggerClassCrawler},
		{"monitor", "UptimeRobot/2.0", TriggerClassMonitor},
		{"preview", "facebookexternalhit/1.1", TriggerClassPreviewBot},
		{"browser", "Mozilla/5.0 Chrome/126.0 Safari/537.36", TriggerClassUser},
		{"unknown", "my-custom-client/1.0", TriggerClassUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://example.test/", nil)
			r.Header.Set("User-Agent", tt.ua)
			if got := ClassifyWakeTrigger(r); got != tt.want {
				t.Fatalf("classify = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeCrawlerPolicy(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"", "wake"}, {"wake", "wake"}, {"cached", "cached"}, {"BLOCK", "block"}, {"invalid", "wake"},
	} {
		if got := normalizeCrawlerPolicy(tt.in); got != tt.want {
			t.Fatalf("normalizeCrawlerPolicy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
