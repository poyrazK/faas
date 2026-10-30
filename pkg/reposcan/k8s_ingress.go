package reposcan

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Edge behaviour declared on an Ingress (ADR-20261029093621 §6). Common
// proxy annotations are not applied: the scan records each one on the
// workload its Ingress routes to, and the plan compares it with what
// Gregale does on the account's plan.

// IngressSetting kinds.
const (
	IngressBodySize    = "body_size"
	IngressReadTimeout = "read_timeout"
	IngressSendTimeout = "send_timeout"
	IngressWebSocket   = "websocket"
	IngressSSLRedirect = "ssl_redirect"
)

// IngressSetting is one proxy annotation of a Kubernetes Ingress that routes
// to the workload. Gregale does not apply it; the plan says what happens
// instead on the account's plan.
type IngressSetting struct {
	Setting    string // one of the Ingress* kinds
	Annotation string // the annotation key as written
	Value      string
	Ingress    string // the Ingress name
}

// k8sIngressAnnotations are the proxy annotations of ingress-nginx, NGINX
// Inc's controller, HAProxy (both controllers) and Traefik that the plan
// explains.
var k8sIngressAnnotations = map[string]string{
	"nginx.ingress.kubernetes.io/proxy-body-size":        IngressBodySize,
	"nginx.org/client-max-body-size":                     IngressBodySize,
	"haproxy-ingress.github.io/proxy-body-size":          IngressBodySize,
	"nginx.ingress.kubernetes.io/proxy-read-timeout":     IngressReadTimeout,
	"nginx.org/proxy-read-timeout":                       IngressReadTimeout,
	"haproxy.org/timeout-server":                         IngressReadTimeout,
	"haproxy-ingress.github.io/timeout-server":           IngressReadTimeout,
	"nginx.ingress.kubernetes.io/proxy-send-timeout":     IngressSendTimeout,
	"nginx.org/proxy-send-timeout":                       IngressSendTimeout,
	"nginx.ingress.kubernetes.io/websocket-services":     IngressWebSocket,
	"nginx.org/websocket-services":                       IngressWebSocket,
	"nginx.ingress.kubernetes.io/ssl-redirect":           IngressSSLRedirect,
	"nginx.ingress.kubernetes.io/force-ssl-redirect":     IngressSSLRedirect,
	"nginx.org/redirect-to-https":                        IngressSSLRedirect,
	"haproxy.org/ssl-redirect":                           IngressSSLRedirect,
	"haproxy-ingress.github.io/ssl-redirect":             IngressSSLRedirect,
	"ingress.kubernetes.io/ssl-redirect":                 IngressSSLRedirect,
	"traefik.ingress.kubernetes.io/redirect-entry-point": IngressSSLRedirect,
}

// k8sIngress is an Ingress the scan read: its proxy settings, the Services
// its rules and default backend route to, and which path goes where.
type k8sIngress struct {
	src, name, namespace string
	settings             []IngressSetting
	backends             []string
	routes               []string // "<path> → <service>", "(default) → <service>"
}

// k8sReadIngress reads an Ingress's known proxy annotations and backend
// Services, networking.k8s.io/v1 and the older extensions/v1beta1 shape.
// It reports false for an Ingress with neither.
func k8sReadIngress(src string, node *yaml.Node, h k8sHeader) (k8sIngress, bool, error) {
	var obj map[string]any
	if err := node.Decode(&obj); err != nil {
		return k8sIngress{}, false, err
	}
	in := k8sIngress{src: src, name: h.Metadata.Name}
	in.namespace, _ = k8sMap(obj, "metadata")["namespace"].(string)
	annotations := k8sMap(obj, "metadata", "annotations")
	for _, key := range slices.Sorted(maps.Keys(annotations)) {
		if setting, ok := k8sIngressAnnotations[key]; ok {
			in.settings = append(in.settings, IngressSetting{Setting: setting, Annotation: key, Value: strings.TrimSpace(fmt.Sprint(annotations[key])), Ingress: in.name})
		}
	}
	spec := k8sMap(obj, "spec")
	add := func(path string, backend map[string]any) {
		name, _ := k8sMap(backend, "service")["name"].(string)
		if name == "" {
			name, _ = backend["serviceName"].(string)
		}
		if name = strings.TrimSpace(name); name == "" {
			return
		}
		if !slices.Contains(in.backends, name) {
			in.backends = append(in.backends, name)
		}
		if route := path + " → " + name; !slices.Contains(in.routes, route) {
			in.routes = append(in.routes, route)
		}
	}
	add("(default)", k8sMap(spec, "defaultBackend"))
	add("(default)", k8sMap(spec, "backend"))
	rules, _ := spec["rules"].([]any)
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		paths, _ := k8sMap(rule, "http")["paths"].([]any)
		for _, rawPath := range paths {
			path, _ := rawPath.(map[string]any)
			p, _ := path["path"].(string)
			if p = strings.TrimSpace(p); p == "" {
				p = "/"
			}
			add(p, k8sMap(path, "backend"))
		}
	}
	slices.Sort(in.backends)
	return in, len(in.settings) > 0 || len(in.backends) > 0, nil
}

// applyK8sIngresses records each Ingress's proxy settings on the workload
// each of its backend Services selects. A websocket-services annotation
// names the Services it covers; the other settings cover every backend. An
// Ingress that sends paths to more than one Service is reported: each
// Service's Deployment becomes its own app with its own URL.
func applyK8sIngresses(seeds []workloadSeed, ingresses []k8sIngress, services []k8sService, deployments []k8sEnvWorkload) []string {
	if len(ingresses) == 0 {
		return nil
	}
	targets := k8sServiceTargets(services, deployments)
	index := map[string]int{}
	for i, s := range seeds {
		index[s.name] = i
	}
	var warnings []string
	for _, in := range ingresses {
		if len(in.backends) > 1 {
			warnings = append(warnings, fmt.Sprintf("reposcan: %s: Ingress %q routes paths to several Services (%s): each Service's Deployment becomes its own app with its own URL; to serve them under one host, add `route` edge rules with `gregale edge rule create --kind route`", in.src, in.name, strings.Join(in.routes, ", ")))
		}
		if len(in.settings) == 0 {
			continue
		}
		for _, backend := range in.backends {
			name, ok := targets[strings.ToLower(backend)+"."+k8sNamespace(in.namespace)]
			i, seeded := index[name]
			if !ok || !seeded {
				warnings = append(warnings, fmt.Sprintf("reposcan: %s: Ingress %q: backend Service %q does not select exactly one scanned Deployment, so its proxy settings are not compared with Gregale's", in.src, in.name, backend))
				continue
			}
			for _, setting := range in.settings {
				if setting.Setting == IngressWebSocket && !k8sListsService(setting.Value, backend) {
					continue
				}
				seeds[i].ingressSettings = append(seeds[i].ingressSettings, setting)
			}
		}
	}
	return warnings
}

func k8sListsService(list, service string) bool {
	for _, name := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(name), service) {
			return true
		}
	}
	return false
}
