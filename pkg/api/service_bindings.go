package api

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ServiceBindingPolicy controls which same-account internal services an app
// may call. The empty persisted value is the backwards-compatible account
// policy.
type ServiceBindingPolicy string

const (
	// ServiceBindingPolicyAccount preserves the original service-mesh
	// contract: any app may call another app owned by the same account.
	ServiceBindingPolicyAccount ServiceBindingPolicy = "account"
	// ServiceBindingPolicyDeclared limits the caller to targets present in its
	// repository-declared service binding inventory.
	ServiceBindingPolicyDeclared ServiceBindingPolicy = "declared"
)

// Effective returns the policy used at runtime. Unknown non-empty values fail
// closed to declared so an older gateway cannot accidentally widen a policy
// written by a newer control plane.
func (p ServiceBindingPolicy) Effective() ServiceBindingPolicy {
	switch p {
	case "", ServiceBindingPolicyAccount:
		return ServiceBindingPolicyAccount
	case ServiceBindingPolicyDeclared:
		return ServiceBindingPolicyDeclared
	default:
		return ServiceBindingPolicyDeclared
	}
}

// ServiceBindingTransport selects the canonical URL injected into
// GREGALE_SERVICE_<NAME>_URL. The HTTPS URL remains available as an explicit
// alias in either mode so workloads can migrate without changing the binding
// name.
type ServiceBindingTransport string

const (
	ServiceBindingTransportHTTP  ServiceBindingTransport = "http"
	ServiceBindingTransportHTTPS ServiceBindingTransport = "https"
)

// Effective returns the transport used for URL injection. Empty is the
// backwards-compatible HTTP contract; unknown non-empty values fail closed
// to HTTPS rather than silently downgrading a newer control-plane setting.
func (t ServiceBindingTransport) Effective() ServiceBindingTransport {
	switch t {
	case "", ServiceBindingTransportHTTP:
		return ServiceBindingTransportHTTP
	case ServiceBindingTransportHTTPS:
		return ServiceBindingTransportHTTPS
	default:
		return ServiceBindingTransportHTTPS
	}
}

// NormalizeServiceBindingTransport validates and canonicalizes a requested
// transport. An omitted setting is represented by the empty value so updates
// can preserve the current transport.
func NormalizeServiceBindingTransport(raw ServiceBindingTransport) (ServiceBindingTransport, error) {
	switch normalized := ServiceBindingTransport(strings.ToLower(strings.TrimSpace(string(raw)))); normalized {
	case "":
		return "", nil
	case ServiceBindingTransportHTTP, ServiceBindingTransportHTTPS:
		return normalized, nil
	default:
		return "", fmt.Errorf("service_binding_transport must be http or https")
	}
}

// AppServiceBinding is one declared dependency from the returned app to
// another app in the same account. Binding is the platform-owned
// environment key injected into the caller; Service is the target app's
// stable internal name.
//
// The account policy uses this as discovery metadata only. The opt-in declared
// policy also uses it as the caller's outbound service authorization list.
type AppServiceBinding struct {
	Binding string `json:"binding"`
	Service string `json:"service"`
}

// NormalizeAllowedServiceCallers validates and canonicalizes a target policy.
// It returns a non-nil empty slice for an explicit deny-all list. Callers are
// logical app names rather than generated preview slugs.
func NormalizeAllowedServiceCallers(raw []string) ([]string, error) {
	return normalizeServiceNames(raw, "allowed_service_callers", AllowedServiceCallersMax)
}

// NormalizeServiceBindingTargets validates and canonicalizes standalone
// outbound target names. An empty slice deliberately means no bindings.
func NormalizeServiceBindingTargets(raw []string) ([]string, error) {
	return normalizeServiceNames(raw, "service_binding_targets", ServiceBindingTargetsMax)
}

func normalizeServiceNames(raw []string, field string, max int) ([]string, error) {
	if len(raw) > max {
		return nil, fmt.Errorf("%s exceeds %d names", field, max)
	}
	seen := make(map[string]struct{}, len(raw))
	names := make([]string, 0, len(raw))
	for _, value := range raw {
		name := strings.ToLower(strings.TrimSpace(value))
		if !validServiceCallerName(name) {
			return nil, fmt.Errorf("%s contains invalid app name %q", field, value)
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

const (
	ServiceBindingEnvPrefix      = "GREGALE_SERVICE_"
	ServiceBindingEnvSuffix      = "_URL"
	ServiceBindingHTTPSEnvSuffix = "_HTTPS_URL"
	ServiceBindingPort           = 10080

	// ServiceBindingProbePath is reserved by the gateway for the HTTPS canary
	// sent by `gregale bindings verify`. The gateway only intercepts it when a
	// caller sends the probe marker over a bound .internal alias.
	ServiceBindingProbePath = "/.well-known/gregale/service-binding-probe"
	// ServiceBindingProbeRequestHeader marks a HEAD request as a platform
	// probe rather than an application request. Ordinary requests to the same
	// path are still forwarded to the target application.
	ServiceBindingProbeRequestHeader = "X-Gregale-Service-Binding-Probe"
	// ServiceBindingProbeResponseHeader echoes the request marker only when
	// the gateway recognizes and handles a platform probe.
	ServiceBindingProbeResponseHeader = "X-Gregale-Service-Binding-Probe"
	// ServiceBindingProbeStageHeader reports the last gateway stage reached.
	ServiceBindingProbeStageHeader = "X-Gregale-Service-Binding-Probe-Stage"
	// ServiceBindingProbeVersion versions the probe marker and response.
	ServiceBindingProbeVersion = "v1"
	// ServiceBindingSmokePathMaxBytes bounds the origin-form request URI
	// accepted by the caller-side handler smoke test.
	ServiceBindingSmokePathMaxBytes = 2048
)

// NormalizeServiceBindingSmokePath accepts only an origin-form request URI
// for a private service call. The result never carries a host or scheme, so a
// smoke task cannot be redirected to an arbitrary URL by its path argument.
func NormalizeServiceBindingSmokePath(raw string) (string, error) {
	if raw == "" || len(raw) > ServiceBindingSmokePathMaxBytes ||
		!strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		strings.ContainsAny(raw, "\\\r\n#") {
		return "", fmt.Errorf("path must be an absolute service path without a host, fragment, or backslash")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Opaque != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.Path, "/") {
		return "", fmt.Errorf("path must be a valid origin-form service request URI")
	}
	return parsed.RequestURI(), nil
}

// ServiceBindingProbeCheck is one independently observable stage in an
// HTTPS service-binding canary.
type ServiceBindingProbeCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ServiceBindingProbeReport contains only bounded, non-secret diagnostics
// from a canary executed in the caller's deployment-attached task guest.
type ServiceBindingProbeReport struct {
	App           string                   `json:"app,omitempty"`
	Service       string                   `json:"service"`
	URL           string                   `json:"url"`
	TaskID        string                   `json:"task_id,omitempty"`
	DeploymentID  string                   `json:"deployment_id,omitempty"`
	DNS           ServiceBindingProbeCheck `json:"dns"`
	TLS           ServiceBindingProbeCheck `json:"tls"`
	Authorization ServiceBindingProbeCheck `json:"authorization"`
	Routing       ServiceBindingProbeCheck `json:"routing"`
	HTTPStatus    int                      `json:"http_status,omitempty"`
	Error         string                   `json:"error,omitempty"`
}

// Passed reports whether every canary stage succeeded.
func (r ServiceBindingProbeReport) Passed() bool {
	return r.DNS.Status == "passed" && r.TLS.Status == "passed" &&
		r.Authorization.Status == "passed" && r.Routing.Status == "passed"
}

// ServiceBindingSmokeReport contains bounded, non-secret diagnostics from an
// explicitly requested GET to one pinned service deployment. It intentionally
// excludes the request query and response body.
type ServiceBindingSmokeReport struct {
	App                string `json:"app,omitempty"`
	Service            string `json:"service"`
	TargetDeploymentID string `json:"target_deployment_id"`
	URL                string `json:"url"`
	Path               string `json:"path,omitempty"`
	TaskID             string `json:"task_id,omitempty"`
	CallerDeploymentID string `json:"caller_deployment_id,omitempty"`
	HTTPStatus         int    `json:"http_status,omitempty"`
	ExpectedStatus     string `json:"expected_status"`
	ElapsedMillis      int64  `json:"elapsed_ms,omitempty"`
	Passed             bool   `json:"passed"`
	Error              string `json:"error,omitempty"`
}

// ServiceBindingsForTargets derives platform-owned binding keys from
// normalized target names. The caller should validate names first.
func ServiceBindingsForTargets(targets []string) []AppServiceBinding {
	if len(targets) == 0 {
		return nil
	}
	bindings := make([]AppServiceBinding, 0, len(targets))
	for _, target := range targets {
		bindings = append(bindings, AppServiceBinding{Binding: ServiceBindingEnvKey(target), Service: target})
	}
	return bindings
}

// ServiceBindingEnvKey derives the platform-owned variable name for a target.
func ServiceBindingEnvKey(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	b.Grow(len(ServiceBindingEnvPrefix) + len(name) + len(ServiceBindingEnvSuffix))
	b.WriteString(ServiceBindingEnvPrefix)
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString(ServiceBindingEnvSuffix)
	return b.String()
}

// ServiceBindingHTTPSEnvKey derives the HTTPS companion variable for a
// target. In HTTPS transport mode it intentionally matches the canonical
// ServiceBindingEnvKey URL.
func ServiceBindingHTTPSEnvKey(name string) string {
	return strings.TrimSuffix(ServiceBindingEnvKey(name), ServiceBindingEnvSuffix) + ServiceBindingHTTPSEnvSuffix
}

// ServiceBindingEnv replaces platform-owned URLs while preserving other app
// environment values. It retains the legacy HTTP canonical URL contract.
func ServiceBindingEnv(base map[string]string, bindings []AppServiceBinding) map[string]string {
	return ServiceBindingEnvForTransport(base, bindings, ServiceBindingTransportHTTP)
}

// ServiceBindingEnvForTransport replaces platform-owned URLs while preserving
// other app environment values. The HTTPS alias is injected in either mode;
// the selected transport controls the canonical _URL binding.
func ServiceBindingEnvForTransport(base map[string]string, bindings []AppServiceBinding, transport ServiceBindingTransport) map[string]string {
	env := make(map[string]string, len(base)+len(bindings))
	for key, value := range base {
		if strings.HasPrefix(key, ServiceBindingEnvPrefix) && strings.HasSuffix(key, ServiceBindingEnvSuffix) {
			continue
		}
		env[key] = value
	}
	transport = transport.Effective()
	for _, binding := range bindings {
		httpsURL := fmt.Sprintf("https://%s.internal", binding.Service)
		canonicalURL := fmt.Sprintf("http://%s.svc.gregale:%d", binding.Service, ServiceBindingPort)
		if transport == ServiceBindingTransportHTTPS {
			canonicalURL = httpsURL
		}
		env[binding.Binding] = canonicalURL
		env[ServiceBindingHTTPSEnvKey(binding.Service)] = httpsURL
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func validServiceCallerName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
