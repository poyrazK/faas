package githubd

import (
	"maps"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261105090226: a preview frontend's public build URLs point at the
// previews of its own pull request; URLs on other hosts are kept.
func TestRewritePreviewBuildURLs(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "")
	hosts := previewBuildURLHosts(
		[]state.App{{Slug: "shop-web"}, {Slug: "shop-api"}},
		[]state.App{{Slug: "pr-7-shop-web"}, {Slug: "pr-7-shop-api"}},
	)
	got := rewritePreviewBuildURLs(map[string]string{
		"VITE_API_URL":     "https://shop-api." + AppsDomain() + "/v1",
		"VITE_SELF_URL":    "https://SHOP-WEB." + AppsDomain(),
		"VITE_PAYMENT_URL": "https://payments.example.com",
		"VITE_OTHER_URL":   "https://billing." + AppsDomain(),
	}, hosts)
	want := map[string]string{
		"VITE_API_URL":     "https://pr-7-shop-api." + AppsDomain() + "/v1",
		"VITE_SELF_URL":    "https://pr-7-shop-web." + AppsDomain(),
		"VITE_PAYMENT_URL": "https://payments.example.com",
		"VITE_OTHER_URL":   "https://billing." + AppsDomain(),
	}
	if !maps.Equal(got, want) {
		t.Fatalf("rewritten = %v, want %v", got, want)
	}
	if got := rewritePreviewBuildURLs(nil, hosts); got != nil {
		t.Fatalf("no URLs = %v, want nil", got)
	}
}
