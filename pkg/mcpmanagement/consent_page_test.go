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

// Every permission on the Connect screen starts ticked; the person unticks
// what they do not want (ADR-20261127160000 §6).
func TestConsentPageTicksEveryPermission(t *testing.T) {
	signIn := consentPage(consentView{Native: true, SameTab: true, OfferWrites: true, RequestID: "r", CSRF: "c", ConsentAction: "/oauth/consent"})
	review := consentPage(consentView{
		Account: "alice@example.com", RequestID: "r", CSRF: "c", ConsentAction: "/oauth/consent",
		Writes: true, Publishing: true, PrivateInvocations: true, Invocations: true,
		Apps:         []consentApp{{Slug: "hello-api"}},
		Environments: []EnvironmentGrant{{Project: "shop", Environment: "staging"}},
	})
	for _, tc := range []struct{ page, name, input string }{
		{signIn, "deploy access", `name="access" value="deploy" form="gregale-connect" checked>`},
		{signIn, "configuration", `name="capability" value="env:write" checked>`},
		{signIn, "secret requests", `name="capability" value="secrets:request" checked>`},
		{signIn, "object storage", `name="capability" value="storage:manage" checked>`},
		{signIn, "managed PostgreSQL", `name="capability" value="postgres:manage" checked>`},
		{review, "writes", `name="allow_writes" value="yes" required checked>`},
		{review, "tool publishing", `name="allow_tool_publishing" value="yes" required checked>`},
		{review, "private tool calls", `name="allow_private_tool_invocation" value="yes" required checked>`},
		{review, "tool calls", `name="allow_tool_invocation" value="yes" required checked>`},
		{review, "all apps", `name="app_access" value="all" required checked>`},
		{review, "app diagnostics", `name="request_read_apps" value="hello-api" checked>`},
		{review, "regression triage", `name="regression_write_apps" value="hello-api" checked>`},
		{review, "default environment", `name="default_write" value="yes" checked>`},
		{review, "named environment read", `name="environments" value="shop/staging" checked>`},
		{review, "named environment write", `name="environment_writes" value="shop/staging" checked>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.page, tc.input) {
				t.Fatalf("%s is not ticked by default; want %s", tc.name, tc.input)
			}
		})
	}
	if strings.Contains(signIn, `value="inspect" form="gregale-connect" checked`) {
		t.Fatal("inspect-only is preselected")
	}
	many := make([]consentApp, maxOAuthEntries+1)
	for i := range many {
		many[i] = consentApp{Slug: "app"}
	}
	if page := consentPage(consentView{Account: "a", Writes: true, Apps: many}); strings.Contains(page, `name="request_read_apps" value="app" checked`) {
		t.Fatal("diagnostics ticked for more apps than one consent accepts")
	}
}
