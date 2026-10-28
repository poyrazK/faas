// adr: 20261026195952
package mcpmanagement

import (
	"strings"
	"testing"
)

func TestConsentPageEscapesEveryValue(t *testing.T) {
	hostile := `"><script>alert(1)</script>`
	page := consentPage(consentView{
		Client: hostile, Redirect: hostile, APIBase: hostile, Account: hostile, ConsentAction: hostile,
		Capabilities: []string{hostile}, RequestID: hostile, CSRF: hostile, Writes: true,
		Apps: []consentApp{{Slug: hostile}}, Environments: []EnvironmentGrant{{Project: hostile, Environment: hostile}},
	})
	if strings.Contains(page, "<script>") || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatalf("unescaped value in review page:\n%s", page)
	}
	native := consentPage(consentView{Native: true, LoginURL: `https://console.example/cli-auth?code=a&b="x"`, RequestID: "r", CSRF: "c", ConsentAction: "/mcp/oauth/consent"})
	if !strings.Contains(native, `href="https://console.example/cli-auth?code=a&amp;b=&#34;x&#34;"`) || !strings.Contains(native, `value="check-login"`) {
		t.Fatalf("login page:\n%s", native)
	}
	start := consentPage(consentView{Native: true, Writes: true, RequestID: "r", CSRF: "c", ConsentAction: "/oauth/consent"})
	if !strings.Contains(start, `name="capability" value="secrets:request"`) || !strings.Contains(start, `value="start-login"`) {
		t.Fatalf("sign-in page:\n%s", start)
	}
	if key := consentPage(consentView{RequestID: "r", CSRF: "c", ConsentAction: "/oauth/consent"}); !strings.Contains(key, `name="api_key"`) {
		t.Fatalf("API key page:\n%s", key)
	}
}
