package projectsetup

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// IngressNotes are the plan notes for a workload's Kubernetes Ingress proxy
// settings (ADR-20261029093621 §6). None is applied; each note says what the
// manifest asked for and what Gregale does on this plan instead. The numbers
// come from the plan's limits, never from the docs.
func IngressNotes(service, slug string, settings []reposcan.IngressSetting, plan api.Plan, limits api.Limits) []string {
	notes := make([]string, 0, len(settings))
	for _, s := range settings {
		asked := fmt.Sprintf("%s: Ingress %q %s %s", service, s.Ingress, path.Base(s.Annotation), s.Value)
		notes = append(notes, asked+": "+ingressOutcome(s, slug, plan, limits))
	}
	return notes
}

func ingressOutcome(s reposcan.IngressSetting, slug string, plan api.Plan, limits api.Limits) string {
	switch s.Setting {
	case reposcan.IngressBodySize:
		return fmt.Sprintf("this plan accepts request bodies up to %s", ingressBytes(requestBodyMax(limits)))
	case reposcan.IngressReadTimeout, reposcan.IngressSendTimeout:
		return requestTimeoutOutcome(s.Value, slug, limits)
	case reposcan.IngressWebSocket:
		switch {
		case plan.WebSocketEnabled():
			return "this plan forwards WebSockets by default"
		case plan.WebSocketResponseAllowed():
			return fmt.Sprintf("enable WebSockets with `gregale app %s --websocket-enabled`", slug)
		default:
			return "this plan does not forward WebSockets"
		}
	case reposcan.IngressSSLRedirect:
		return "TLS ends at the Gregale edge and app URLs use HTTPS, so the setting is not used"
	}
	return "not applied"
}

func requestBodyMax(limits api.Limits) int64 {
	if limits.RequestBodyMaxBytes > 0 {
		return limits.RequestBodyMaxBytes
	}
	return api.MaxRequestBodyBytes
}

// requestTimeoutOutcome compares a proxy timeout with the app request budget
// (ADR-093): its default, and the ceiling `--request-timeout` may raise it
// to on this plan (ADR-1150).
func requestTimeoutOutcome(value, slug string, limits api.Limits) string {
	def, ceiling := limits.RequestBudgetForType("app"), limits.RequestBudgetMaxDuration()
	asked, ok := ingressDuration(value)
	switch {
	case !ok:
		return fmt.Sprintf("requests time out after %s by default, and `gregale app %s --request-timeout` allows up to %s on this plan", ingressSeconds(def), slug, ingressSeconds(ceiling))
	case asked <= def:
		return fmt.Sprintf("requests may take up to %s by default, which covers it", ingressSeconds(def))
	case asked <= ceiling:
		return fmt.Sprintf("requests time out after %s by default; `gregale app %s --request-timeout %d` matches it", ingressSeconds(def), slug, int64((asked+time.Second-1)/time.Second))
	default:
		return fmt.Sprintf("requests time out after %s by default and after at most %s on this plan", ingressSeconds(def), ingressSeconds(ceiling))
	}
}

// ingressDuration reads a proxy timeout: whole seconds (ingress-nginx,
// NGINX) or a duration with a unit (HAProxy: 30s, 2m, 500ms).
func ingressDuration(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, true
	}
	d, err := time.ParseDuration(value)
	return d, err == nil && d > 0
}

func ingressSeconds(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", d/time.Minute)
	}
	return fmt.Sprintf("%d s", (d+time.Second-1)/time.Second)
}

func ingressBytes(n int64) string {
	const mb = 1024 * 1024
	if n%mb == 0 {
		return fmt.Sprintf("%d MB", n/mb)
	}
	return fmt.Sprintf("%d bytes", n)
}
