package projectsetup

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestIngressNotes(t *testing.T) {
	setting := func(kind, annotation, value string) []reposcan.IngressSetting {
		return []reposcan.IngressSetting{{Setting: kind, Annotation: annotation, Value: value, Ingress: "web"}}
	}
	limits := func(body int64, budget, ceiling int) api.Limits {
		return api.Limits{RequestBodyMaxBytes: body, RequestBudgetMs: budget, RequestBudgetMaxMs: ceiling}
	}
	for _, tc := range []struct {
		name     string
		settings []reposcan.IngressSetting
		plan     api.Plan
		limits   api.Limits
		want     string
	}{
		{"body size", setting(reposcan.IngressBodySize, "nginx.ingress.kubernetes.io/proxy-body-size", "50m"), api.PlanHobby, limits(25<<20, 30_000, 60_000),
			`api: Ingress "web" proxy-body-size 50m: this plan accepts request bodies up to 25 MB`},
		{"timeout within the default", setting(reposcan.IngressReadTimeout, "nginx.ingress.kubernetes.io/proxy-read-timeout", "20"), api.PlanHobby, limits(0, 30_000, 60_000),
			`api: Ingress "web" proxy-read-timeout 20: requests may take up to 30 s by default, which covers it`},
		{"timeout the app can raise to", setting(reposcan.IngressReadTimeout, "haproxy.org/timeout-server", "45s"), api.PlanHobby, limits(0, 30_000, 60_000),
			"api: Ingress \"web\" timeout-server 45s: requests time out after 30 s by default; `gregale app update --request-timeout 45 shop-api` matches it"},
		{"timeout over the plan", setting(reposcan.IngressSendTimeout, "nginx.org/proxy-send-timeout", "600"), api.PlanPro, limits(0, 30_000, 300_000),
			`api: Ingress "web" proxy-send-timeout 600: requests time out after 30 s by default and after at most 5 min on this plan`},
		{"unreadable timeout", setting(reposcan.IngressReadTimeout, "nginx.org/proxy-read-timeout", "soon"), api.PlanHobby, limits(0, 30_000, 60_000),
			"api: Ingress \"web\" proxy-read-timeout soon: requests time out after 30 s by default, and `gregale app update --request-timeout SECONDS shop-api` allows up to 1 min on this plan"},
		{"websocket on free", setting(reposcan.IngressWebSocket, "nginx.org/websocket-services", "chat"), api.PlanFree, api.Limits{},
			`api: Ingress "web" websocket-services chat: this plan does not forward WebSockets`},
		{"ssl redirect", setting(reposcan.IngressSSLRedirect, "nginx.ingress.kubernetes.io/ssl-redirect", "true"), api.PlanHobby, api.Limits{},
			`api: Ingress "web" ssl-redirect true: TLS ends at the Gregale edge and app URLs use HTTPS, so the setting is not used`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := IngressNotes("api", "shop-api", tc.settings, tc.plan, tc.limits)
			if !slices.Equal(got, []string{tc.want}) {
				t.Errorf("notes = %q\nwant    %q", got, tc.want)
			}
		})
	}
	if got := IngressNotes("api", "shop-api", nil, api.PlanPro, api.Limits{}); len(got) != 0 {
		t.Errorf("no settings: notes = %q, want none", got)
	}
}

// The WebSocket note follows the plan: on by default, opt in, or refused.
func TestIngressNotes_WebSocketFollowsThePlan(t *testing.T) {
	settings := []reposcan.IngressSetting{{Setting: reposcan.IngressWebSocket, Annotation: "nginx.org/websocket-services", Value: "chat", Ingress: "web"}}
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		got := IngressNotes("chat", "chat", settings, plan, api.MustLimitsFor(plan))
		want := "this plan does not forward WebSockets"
		switch {
		case plan.WebSocketEnabled():
			want = "this plan forwards WebSockets by default"
		case plan.WebSocketResponseAllowed():
			want = "enable WebSockets with `gregale app update --websocket-enabled chat`"
		}
		if len(got) != 1 || got[0] != `chat: Ingress "web" websocket-services chat: `+want {
			t.Errorf("%s: notes = %q, want %q", plan, got, want)
		}
	}
}
