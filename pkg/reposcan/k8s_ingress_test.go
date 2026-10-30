package reposcan

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

const k8sIngressManifests = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: shop}
spec:
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: api, image: acme/api:1, ports: [{containerPort: 8080}]}]
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: chat, namespace: shop}
spec:
  template:
    metadata: {labels: {app: chat}}
    spec:
      containers: [{name: chat, image: acme/chat:1}]
---
apiVersion: v1
kind: Service
metadata: {name: api-svc, namespace: shop}
spec: {selector: {app: api}, ports: [{port: 80}]}
---
apiVersion: v1
kind: Service
metadata: {name: chat-svc, namespace: shop}
spec: {selector: {app: chat}, ports: [{port: 80}]}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: shop
  annotations:
    nginx.ingress.kubernetes.io/proxy-body-size: 50m
    nginx.ingress.kubernetes.io/proxy-read-timeout: "120"
    nginx.ingress.kubernetes.io/websocket-services: chat-svc
    nginx.ingress.kubernetes.io/rewrite-target: /
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - {path: /api, pathType: Prefix, backend: {service: {name: api-svc, port: {number: 80}}}}
          - {path: /chat, pathType: Prefix, backend: {service: {name: chat-svc, port: {number: 80}}}}
          - {path: /old, pathType: Prefix, backend: {service: {name: gone, port: {number: 80}}}}
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: legacy
  namespace: shop
  annotations: {haproxy.org/ssl-redirect: "true"}
spec:
  backend: {serviceName: api-svc, servicePort: 80}
`

func TestScanWithOptions_KubernetesIngressSettings(t *testing.T) {
	t.Parallel()
	result, err := ScanWithOptions(k8sPodFS(k8sIngressManifests), k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]IngressSetting{}
	for _, w := range result.Workloads {
		got[w.Name] = w.IngressSettings
	}
	body := IngressSetting{IngressBodySize, "nginx.ingress.kubernetes.io/proxy-body-size", "50m", "web"}
	read := IngressSetting{IngressReadTimeout, "nginx.ingress.kubernetes.io/proxy-read-timeout", "120", "web"}
	ws := IngressSetting{IngressWebSocket, "nginx.ingress.kubernetes.io/websocket-services", "chat-svc", "web"}
	ssl := IngressSetting{IngressSSLRedirect, "haproxy.org/ssl-redirect", "true", "legacy"}
	// Settings land on each backend's Deployment; websocket-services only on
	// the Services it names.
	if want := []IngressSetting{body, read, ssl}; !reflect.DeepEqual(got["api"], want) {
		t.Errorf("api settings = %+v, want %+v", got["api"], want)
	}
	if want := []IngressSetting{body, read, ws}; !reflect.DeepEqual(got["chat"], want) {
		t.Errorf("chat settings = %+v, want %+v", got["chat"], want)
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{
		`Ingress "web": backend Service "gone" does not select exactly one scanned Deployment`,
		`Ingress "web" routes paths to several Services (/api → api-svc, /chat → chat-svc, /old → gone): each Service's Deployment becomes its own app`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `Ingress "legacy" routes paths`) {
		t.Errorf("an Ingress with one backend was reported as fanning out:\n%s", joined)
	}

	off, err := ScanWithOptions(k8sPodFS(k8sIngressManifests), ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range off.Workloads {
		if len(w.IngressSettings) != 0 {
			t.Errorf("without the capability %s settings = %+v, want none", w.Name, w.IngressSettings)
		}
	}
	if offJoined := strings.Join(off.Warnings, "\n"); strings.Contains(offJoined, "proxy settings") || strings.Contains(offJoined, "routes paths") {
		t.Errorf("without the capability the scan reported Ingress settings or routes:\n%s", offJoined)
	}
}

// An Ingress without proxy annotations is still read for its routes, so a
// fan-out across Services is reported.
func TestScanWithOptions_KubernetesIngressFanOutWithoutSettings(t *testing.T) {
	t.Parallel()
	manifests := strings.Replace(k8sIngressManifests, `    nginx.ingress.kubernetes.io/proxy-body-size: 50m
    nginx.ingress.kubernetes.io/proxy-read-timeout: "120"
    nginx.ingress.kubernetes.io/websocket-services: chat-svc
`, "", 1)
	result, err := ScanWithOptions(k8sPodFS(manifests), k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Warnings, "\n")
	if want := `Ingress "web" routes paths to several Services (/api → api-svc, /chat → chat-svc, /old → gone)`; !strings.Contains(joined, want) {
		t.Errorf("warnings missing %q:\n%s", want, joined)
	}
	if strings.Contains(joined, `Ingress "web": backend Service "gone"`) {
		t.Errorf("an Ingress without proxy settings warned about comparing them:\n%s", joined)
	}
}

// An Ingress without a known proxy annotation adds nothing, and its kind
// warning still names its hosts.
func TestScanWithOptions_KubernetesIngressWithoutSettings(t *testing.T) {
	t.Parallel()
	manifests := strings.Replace(k8sIngressManifests, "metadata:\n  name: web", "metadata:\n  name: plain", 1)
	manifests = manifests[:strings.Index(manifests, "  annotations:\n    nginx")] + "spec:\n  rules: [{host: shop.example.com}]\n"
	result, err := ScanWithOptions(k8sPodFS(manifests), k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range result.Workloads {
		if len(w.IngressSettings) != 0 {
			t.Errorf("%s settings = %+v, want none", w.Name, w.IngressSettings)
		}
	}
	if !slices.ContainsFunc(result.Warnings, func(w string) bool {
		return strings.Contains(w, `Ingress "plain" ignored`) && strings.Contains(w, "shop.example.com")
	}) {
		t.Errorf("warnings = %q, want the Ingress kind warning with its host", result.Warnings)
	}
}
