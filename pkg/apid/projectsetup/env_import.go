package projectsetup

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// Reasons a Compose environment value is not imported
// (ADR-20261023085230). They are names for clients, never values.
const (
	EnvSkipNotOptedIn     = "not_opted_in"
	EnvSkipSecret         = "secret"
	EnvSkipServiceAddress = "service_address"
	EnvSkipLocalAddress   = "local_address"
	EnvSkipInterpolated   = "interpolated"
	EnvSkipHostValue      = "host_value"
	EnvSkipEmpty          = "empty"
	EnvSkipInvalid        = "invalid"
)

// EnvImport is one service's import decision: the values to write and, for
// each other key, why it is not imported. Rewritten maps a key whose Compose
// value called another project service over HTTP to the Docker host it named
// (ADR-20261026181020); its value in Values is the Gregale address.
type EnvImport struct {
	Values    map[string]string
	Skipped   map[string]string
	Rewritten map[string]string
}

// secretKeyParts are `_`-separated key parts that name a credential.
var secretKeyParts = []string{
	"SECRET", "SECRETS", "TOKEN", "TOKENS", "PASSWORD", "PASSWORDS", "PASSWD", "PASS", "PWD",
	"KEY", "KEYS", "APIKEY", "PRIVATE", "CREDENTIAL", "CREDENTIALS", "AUTH", "DSN", "SALT", "CERT",
}

var localHosts = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1", "host.docker.internal", "gateway.docker.internal"}

// PlanEnvImport decides, for one Compose service, which plain values may be
// imported. Without the project's opt-in every importable value is reported
// as EnvSkipNotOptedIn, so the plan can say how to import it. callable names
// the project services this deploy runs; an HTTP value that calls one of
// them by its Docker host is imported as that service's Gregale address.
func PlanEnvImport(env reposcan.ComposeEnv, service string, callable []string, limits api.Limits) EnvImport {
	out := EnvImport{Values: map[string]string{}, Skipped: map[string]string{}, Rewritten: map[string]string{}}
	services := make([]string, 0, len(env.Services))
	for name := range env.Services {
		services = append(services, strings.ToLower(name))
	}
	for key, value := range env.Services[service] {
		if PlatformProvidedEnvKey(key) {
			continue
		}
		if value != nil {
			if rewritten, host, ok := rewriteServiceAddress(*value, callable); ok {
				value = &rewritten
				out.Rewritten[key] = host
			}
		}
		reason := envSkipReason(key, value, services, limits)
		if reason == "" && !env.Import {
			reason = EnvSkipNotOptedIn
		}
		if reason != "" {
			out.Skipped[key] = reason
			delete(out.Rewritten, key)
			continue
		}
		out.Values[key] = *value
	}
	return out
}

// RewrittenEnvNotes are the plan notes for imported keys whose Compose value
// was rewritten from a Docker host to a Gregale service address, so a
// reviewer sees the change before apply. They name the hosts only: the plan
// never shows a value, whose path or query is the developer's.
func RewrittenEnvNotes(service string, imported []string, plan EnvImport) []string {
	var notes []string
	for _, key := range imported {
		if host, ok := plan.Rewritten[key]; ok {
			if parsed, err := url.Parse(plan.Values[key]); err == nil {
				notes = append(notes, fmt.Sprintf("%s: %s calls %s; imported with %s as its address", service, key, host, parsed.Host))
			}
		}
	}
	slices.Sort(notes)
	return notes
}

// rewriteServiceAddress turns a Compose value that calls a project service
// over HTTP by its Docker host, `http://billing:8080/v1`, into the address
// that service answers on Gregale, `http://billing.svc.gregale:10081/v1`
// (ADR-20261026181020). The HTTP service mesh forwards to the service's
// serving port on every node, so the Docker port is dropped; HTTPS and
// secure WebSocket values use the private TLS alias. Values with
// credentials, other schemes, and hosts that are not project services stay
// as they are.
func rewriteServiceAddress(value string, callable []string) (rewritten, host string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Opaque != "" {
		return "", "", false
	}
	host = parsed.Hostname()
	if !slices.ContainsFunc(callable, func(name string) bool { return strings.EqualFold(name, host) }) {
		return "", "", false
	}
	name := api.ServiceDNSName(host)
	if !api.ValidServiceName(name) {
		return "", "", false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "ws":
		parsed.Host = fmt.Sprintf("%s.svc.gregale:%d", name, api.ServiceBindingPort)
	case "https", "wss":
		parsed.Host = name + ".internal"
	default:
		return "", "", false
	}
	return parsed.String(), host, true
}

// SplitEnvImport divides a workload's unset keys into those the import will
// write and those that stay unset, with the reason a Compose value for them
// is not imported.
func SplitEnvImport(unset []string, plan EnvImport) (stillUnset, imported []string, reasons map[string]string) {
	for _, key := range unset {
		if _, ok := plan.Values[key]; ok {
			imported = append(imported, key)
			continue
		}
		stillUnset = append(stillUnset, key)
		if reason, ok := plan.Skipped[key]; ok {
			if reasons == nil {
				reasons = map[string]string{}
			}
			reasons[key] = reason
		}
	}
	return stillUnset, imported, reasons
}

func envSkipReason(key string, value *string, services []string, limits api.Limits) string {
	switch {
	case api.ValidateEnvKey(key) != nil:
		return EnvSkipInvalid
	case value == nil:
		return EnvSkipHostValue
	case *value == "":
		return EnvSkipEmpty
	case len(*value) > limits.EnvValueMaxBytes:
		return EnvSkipInvalid
	case secretKey(key) || secretValue(key, *value):
		return EnvSkipSecret
	case strings.Contains(*value, "$"):
		return EnvSkipInterpolated
	}
	host := valueHost(*value)
	switch {
	case slices.Contains(localHosts, host):
		return EnvSkipLocalAddress
	case slices.Contains(services, host):
		return EnvSkipServiceAddress
	}
	return ""
}

func secretKey(key string) bool {
	for _, part := range strings.Split(key, "_") {
		if slices.Contains(secretKeyParts, part) {
			return true
		}
	}
	return false
}

func secretValue(key, value string) bool {
	if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return true
		}
	}
	return len(secretscan.ScanEnvPairs([]secretscan.Pair{{Key: key, Value: value}}, "compose")) > 0
}

// valueHost is the lower-case host a value names: a URL's host, a host:port,
// or a bare single-label name such as `db`.
func valueHost(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			return strings.ToLower(parsed.Hostname())
		}
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.ToLower(host)
	}
	if !strings.ContainsAny(value, " /:") {
		return strings.ToLower(value)
	}
	return ""
}
