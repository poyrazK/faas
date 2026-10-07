package api

// adr: 657
import (
	"strings"
	"testing"
)

func TestRegionalAppsDomainAndOrigin(t *testing.T) {
	boundary := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 20)
	for _, tc := range []struct {
		domain string
		valid  bool
	}{
		{"apps.fsn.example.com", true}, {"xn--region-9za.example", true}, {"123.example", true}, {boundary, true},
		{"", false}, {"localhost", false}, {"Apps.example.com", false}, {"apps.example.com.", false},
		{"https://apps.example.com", false}, {"apps.example.com:443", false}, {"user@apps.example.com", false},
		{"apps.example.com/path", false}, {"apps.example.com?x=1", false}, {"apps.example.com#x", false},
		{"*.example.com", false}, {"apps..example.com", false}, {"-apps.example.com", false}, {"apps-.example.com", false},
		{"apps_example.com", false}, {"bölgeler.example", false}, {"127.0.0.1", false}, {"001.2.3.4", false}, {"[::1]", false},
		{strings.Repeat("a", 64) + ".example", false}, {boundary + "d", false},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			if got := ValidRegionalAppsDomain(tc.domain); got != tc.valid {
				t.Fatalf("domain valid=%t, want %t", got, tc.valid)
			}
			origin := RegionalAppOrigin(strings.Repeat("s", GlobalDirectorySlugMaxBytes), tc.domain)
			if tc.valid && (!ValidGlobalDirectoryAPIBase(origin) || origin != "https://"+strings.Repeat("s", GlobalDirectorySlugMaxBytes)+"."+tc.domain) || !tc.valid && origin != "" {
				t.Fatalf("unexpected origin %q", origin)
			}
		})
	}
	for _, slug := range []string{"", "ab", "Shop", "bad/slug", "api", strings.Repeat("s", GlobalDirectorySlugMaxBytes+1)} {
		if RegionalAppOrigin(slug, "apps.example.com") != "" {
			t.Fatalf("invalid slug %q has a traffic origin", slug)
		}
	}
}
