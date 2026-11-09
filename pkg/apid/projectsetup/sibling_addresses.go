package projectsetup

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// SiblingAddresses are the values of a Compose service's environment that
// call another service of the project, rewritten to that service's Gregale
// address: the subset of PlanEnvImport a GitHub push imports, because an
// address is never a credential and the Docker address cannot resolve here
// (ADR-20261105104500). Other plain values still need a reviewed deploy.
func SiblingAddresses(env reposcan.ComposeEnv, service string, callable []string, limits api.Limits, privateHTTPS bool) map[string]string {
	plan := PlanEnvImport(env, service, callable, limits, privateHTTPS)
	out := map[string]string{}
	for key := range plan.Rewritten {
		if value, ok := plan.Values[key]; ok {
			out[key] = value
		}
	}
	return out
}

// ValidSiblingAddress reports whether value is a Gregale service address
// SiblingAddresses produces: an http, https, ws or wss URL without
// credentials whose host is <service>.svc.gregale on the service mesh port or
// <service>.internal. apid checks what githubd sends before writing it.
func ValidSiblingAddress(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss":
	default:
		return false
	}
	host := parsed.Hostname()
	if name, ok := strings.CutSuffix(host, ".svc.gregale"); ok {
		return api.ValidServiceName(name) && parsed.Port() == strconv.Itoa(api.ServiceBindingPort)
	}
	if name, ok := strings.CutSuffix(host, ".internal"); ok {
		return api.ValidServiceName(name) && parsed.Port() == ""
	}
	return false
}
