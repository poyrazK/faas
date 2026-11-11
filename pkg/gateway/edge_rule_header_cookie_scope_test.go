package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// adr: 091 — amendment 2026-10-31 (round 6): the app's own Set-Cookie with
// Domain=<apps domain> is made host-only (H27), but a kind=headers response
// op or a redirect rule's headers were written as they stood, so any tenant
// could set a cookie every other tenant's app receives. Rule header values
// now go through the same scoping.
func TestEdgeRuleHeaderOpsScopeSharedDomainCookies(t *testing.T) {
	restore := hostidentity.Configure(hostidentity.Domains{Platform: "gregale.dev", Apps: "gregale.app", Deploy: "gregale.dev"})
	t.Cleanup(restore)
	for _, tc := range []struct {
		name string
		op   EdgeRuleHeaderOp
		want []string
	}{
		{"set shared domain", EdgeRuleHeaderOp{Name: "Set-Cookie", Value: "sid=evil; Domain=gregale.app; Path=/", Action: "set"}, []string{"sid=evil; Path=/"}},
		{"add parent domain", EdgeRuleHeaderOp{Name: "set-cookie", Value: "sid=evil; domain=.gregale.app", Action: "add"}, []string{"sid=evil"}},
		{"own custom domain kept", EdgeRuleHeaderOp{Name: "Set-Cookie", Value: "sid=1; Domain=shop.example.com", Action: "set"}, []string{"sid=1; Domain=shop.example.com"}},
		{"other header untouched", EdgeRuleHeaderOp{Name: "X-Note", Value: "Domain=gregale.app", Action: "set"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			applyHeaderOp(hdr, tc.op)
			if tc.want == nil {
				if got := hdr.Get(tc.op.Name); got != tc.op.Value {
					t.Fatalf("%s = %q, want %q", tc.op.Name, got, tc.op.Value)
				}
				return
			}
			got := hdr.Values("Set-Cookie")
			if len(got) != len(tc.want) || got[0] != tc.want[0] {
				t.Fatalf("Set-Cookie = %q, want %q", got, tc.want)
			}
		})
	}
}

// A redirect rule's headers are scoped the same way.
func TestEdgeRuleRedirectHeadersScopeSharedDomainCookies(t *testing.T) {
	restore := hostidentity.Configure(hostidentity.Domains{Platform: "gregale.dev", Apps: "gregale.app", Deploy: "gregale.dev"})
	t.Cleanup(restore)
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.edgeRules = stubEdgeRuleMatcher{redirect: &EdgeRuleRedirectResolved{
		ID: "redir", AccountID: "acct-1", AppID: "app-1", StatusCode: http.StatusFound, To: "https://new.example/",
		Headers: map[string]string{"Set-Cookie": "sid=evil; Domain=gregale.app; Path=/"},
	}}
	w := httptest.NewRecorder()
	if !h.matchAndApplyRedirect(w, templateTestRequest("http://old.gregale.app/"), App{ID: "app-1", AccountID: "acct-1"}) {
		t.Fatal("redirect did not apply")
	}
	if got := w.Header().Get("Set-Cookie"); got != "sid=evil; Path=/" {
		t.Fatalf("redirect Set-Cookie = %q, want the Domain attribute dropped", got)
	}
}
