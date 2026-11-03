package mailoutbox

import "testing"

// The customer dashboard has no per-deployment path, so a failed-deployment
// email links to the app's Deployments tab. An intent an earlier release
// queued with the old path still verifies and is sent with the working link.
func TestDeploymentFailureLinkOpensTheDeploymentsTab(t *testing.T) {
	const id = "0193d1a0-0000-7000-8000-000000000001"
	want := "https://gregale.dev/dashboard/apps/shop-api?deployment=" + id + "&tab=Deployments"
	if got := deploymentFailureURL("https://gregale.dev", "shop-api", id); got != want {
		t.Fatalf("link = %q, want %q", got, want)
	}
	for name, tc := range map[string]struct {
		link string
		ok   bool
	}{
		"current":         {want, true},
		"legacy path":     {"https://gregale.dev/dashboard/apps/shop-api/deployments/" + id, true},
		"other app":       {"https://gregale.dev/dashboard/apps/other?deployment=" + id + "&tab=Deployments", false},
		"other deploy":    {"https://gregale.dev/dashboard/apps/shop-api?deployment=x&tab=Deployments", false},
		"extra parameter": {want + "&next=https://evil.example", false},
		"plain http":      {"http://gregale.dev/dashboard/apps/shop-api?deployment=" + id + "&tab=Deployments", false},
		"fragment":        {want + "#x", false},
	} {
		got, ok := verifiedDeploymentFailureURL(tc.link, "shop-api", id)
		if ok != tc.ok || (ok && got != want) {
			t.Errorf("%s: verified = (%q, %t), want (%q, %t)", name, got, ok, want, tc.ok)
		}
	}
}
