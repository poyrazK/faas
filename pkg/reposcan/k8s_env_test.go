package reposcan

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// The Secret values below must never leave the manifest.
const (
	k8sSecretData       = "c3VwZXJzZWNyZXQ=" // base64 "supersecret"
	k8sSecretStringData = "hunter2-plaintext"
)

const k8sEnvManifests = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
  annotations: {gregale.dev/env-import: "true"}
spec:
  template:
    metadata: {labels: {app: api}}
    spec:
      containers:
        - name: api
          image: acme/api:1
          envFrom:
            - configMapRef: {name: api-config}
              prefix: CFG_
            - secretRef: {name: api-secrets}
          env:
            - {name: LOG_LEVEL, value: debug}
            - {name: WORKERS, value: 4}
            - name: REGION
              valueFrom: {configMapKeyRef: {name: api-config, key: region}}
            - name: DB_PASSWORD
              valueFrom: {secretKeyRef: {name: db, key: password}}
            - name: POD_NAME
              valueFrom: {fieldRef: {fieldPath: metadata.name}}
            - {name: BILLING_URL, value: "http://billing-svc.shop.svc.cluster.local:8080/v1"}
            - {name: BILLING_SHORT, value: "http://billing-svc/v1"}
            - {name: BILLING_GRPC, value: "billing-svc:9000"}
            - {name: OTHER_NS, value: "http://billing-svc.other.svc:8080"}
            - {name: CFG_MODE, value: override}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: billing, namespace: shop}
spec:
  template:
    metadata: {labels: {app: billing, tier: backend}}
    spec:
      containers: [{name: billing, image: acme/billing:1, env: [{name: CURRENCY, value: EUR}]}]
---
apiVersion: v1
kind: Service
metadata: {name: billing-svc, namespace: shop}
spec:
  selector: {app: billing}
  ports: [{port: 8080}]
---
apiVersion: v1
kind: ConfigMap
metadata: {name: api-config, namespace: shop}
data:
  region: eu-central
  MODE: fast
---
apiVersion: v1
kind: Secret
metadata: {name: api-secrets, namespace: shop}
data: {API_TOKEN: ` + k8sSecretData + `}
stringData: {SIGNING_KEY: ` + k8sSecretStringData + `}
`

func k8sEnvValue(t *testing.T, env ComposeEnv, service, key string) string {
	t.Helper()
	value, ok := env.Services[service][key]
	if !ok || value == nil {
		t.Fatalf("%s %s: no value in %v", service, key, env.Services[service])
	}
	return *value
}

func TestReadProjectEnv_KubernetesValues(t *testing.T) {
	env, err := ReadProjectEnv(k8sPodFS(k8sEnvManifests), k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"LOG_LEVEL":     "debug",
		"WORKERS":       "4",
		"REGION":        "eu-central",
		"CFG_region":    "eu-central",
		"CFG_MODE":      "override", // env wins over envFrom
		"BILLING_URL":   "http://billing:8080/v1",
		"BILLING_SHORT": "http://billing/v1",
		"BILLING_GRPC":  "billing:9000",
		// Another namespace's Service is not the one the scan read.
		"OTHER_NS": "http://billing-svc.other.svc:8080",
	} {
		if got := k8sEnvValue(t, env, "api", key); got != want {
			t.Errorf("api %s = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"POD_NAME", "DB_PASSWORD", "API_TOKEN", "SIGNING_KEY"} {
		if _, ok := env.Services["api"][key]; ok {
			t.Errorf("api %s has an entry, want none", key)
		}
	}
	if got, want := env.SecretRefs["api"], []string{"API_TOKEN", "DB_PASSWORD", "SIGNING_KEY"}; !slices.Equal(got, want) {
		t.Errorf("SecretRefs = %v, want %v", got, want)
	}
	if !env.Kubernetes["api"] || !env.Kubernetes["billing"] {
		t.Errorf("Kubernetes = %v, want api and billing", env.Kubernetes)
	}
	if !env.OptedIn("api") || env.OptedIn("billing") {
		t.Errorf("OptedIn api=%t billing=%t, want true and false", env.OptedIn("api"), env.OptedIn("billing"))
	}
	if !env.ImportsAny() {
		t.Error("ImportsAny = false, want true")
	}
	assertNoK8sSecretValue(t, fmt.Sprintf("%#v", env))
	for _, value := range env.Services["api"] {
		if value != nil {
			assertNoK8sSecretValue(t, *value)
		}
	}
}

func assertNoK8sSecretValue(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{k8sSecretData, k8sSecretStringData, "supersecret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("Secret value %q leaked into %s", secret, text)
		}
	}
}

func TestReadProjectEnv_KubernetesNeedsTheCapability(t *testing.T) {
	env, err := ReadProjectEnv(k8sPodFS(k8sEnvManifests), ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Services) != 0 || env.ImportsAny() {
		t.Fatalf("env = %+v, want nothing without the capability", env)
	}
}

// A Compose service keeps its own entries when a Kubernetes workload has its
// name, and Compose's file-wide opt-in does not opt Kubernetes workloads in.
func TestReadProjectEnv_ComposeAndKubernetes(t *testing.T) {
	fsys := k8sPodFS(k8sEnvManifests)
	fsys["compose.yaml"] = &fstest.MapFile{Data: []byte("x-gregale-env-import: true\nservices:\n  api:\n    image: acme/api:1\n    environment: {LOG_LEVEL: info}\n")}
	env, err := ReadProjectEnv(fsys, k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	if got := k8sEnvValue(t, env, "api", "LOG_LEVEL"); got != "info" {
		t.Errorf("api LOG_LEVEL = %q, want Compose's info", got)
	}
	if env.Kubernetes["api"] || !env.OptedIn("api") {
		t.Errorf("api: Kubernetes=%t OptedIn=%t, want the Compose service", env.Kubernetes["api"], env.OptedIn("api"))
	}
	if !env.Kubernetes["billing"] || env.OptedIn("billing") {
		t.Errorf("billing: Kubernetes=%t OptedIn=%t, want a Kubernetes workload that did not opt in", env.Kubernetes["billing"], env.OptedIn("billing"))
	}
}

// With the capability, the scan lists envFrom keys (Secret keys included,
// values never), drops pod-field names, and reports a source it did not read.
func TestScanWithOptions_KubernetesEnvKeys(t *testing.T) {
	fsys := k8sPodFS(k8sEnvManifests + `---
apiVersion: batch/v1
kind: CronJob
metadata: {name: report}
spec:
  schedule: "0 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: report
              image: acme/report:1
              envFrom: [{configMapRef: {name: missing}}, {configMapRef: {name: maybe, optional: true}}]
`)
	result, err := ScanWithOptions(fsys, k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	var api Workload
	for _, w := range result.Workloads {
		if w.Name == "api" {
			api = w
		}
	}
	for _, want := range []string{"API_TOKEN", "CFG_MODE", "CFG_region", "DB_PASSWORD", "LOG_LEVEL", "SIGNING_KEY"} {
		if !slices.Contains(api.EnvKeys, want) {
			t.Errorf("api EnvKeys %v missing %s", api.EnvKeys, want)
		}
	}
	if slices.Contains(api.EnvKeys, "POD_NAME") {
		t.Errorf("api EnvKeys %v has POD_NAME, a pod field", api.EnvKeys)
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{
		"api: env POD_NAME valueFrom.fieldRef ignored",
		`report: envFrom ConfigMap "missing" is not in the scan`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"maybe"`) {
		t.Errorf("an optional source was reported:\n%s", joined)
	}
	if strings.Contains(joined, "envFrom ignored") {
		t.Errorf("envFrom, which the scan reads, was reported as ignored:\n%s", joined)
	}
	assertNoK8sSecretValue(t, fmt.Sprintf("%#v", result))

	off, err := ScanWithOptions(fsys, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range off.Workloads {
		if w.Name == "api" && slices.Contains(w.EnvKeys, "API_TOKEN") {
			t.Errorf("without the capability EnvKeys = %v, want today's env names only", w.EnvKeys)
		}
	}
}

func TestK8sServiceTarget(t *testing.T) {
	targets := map[string]string{"billing-svc.shop": "billing", "cache.default": "redis-proxy"}
	for _, tc := range []struct {
		value, namespace, want string
	}{
		{"http://billing-svc:8080/x", "shop", "http://billing:8080/x"},
		{"http://billing-svc.shop/x", "", "http://billing/x"},
		{"https://billing-svc.shop.svc.cluster.local./x", "", "https://billing/x"},
		{"billing-svc.shop.svc:9000", "", "billing:9000"},
		{"cache", "", "redis-proxy"},
		{"http://billing-svc:8080", "default", "http://billing-svc:8080"},
		{"http://example.com/billing-svc", "shop", "http://example.com/billing-svc"},
		{"plain text", "shop", "plain text"},
	} {
		if got := k8sRewriteServiceHost(tc.value, tc.namespace, targets); got != tc.want {
			t.Errorf("k8sRewriteServiceHost(%q, %q) = %q, want %q", tc.value, tc.namespace, got, tc.want)
		}
	}
}

// A ConfigMap is reported as not imported unless every workload that reads
// it is annotated for the import, and the warning names the ones that are not.
func TestScanWithOptions_KubernetesConfigMapImport(t *testing.T) {
	const configMap = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cfg}\ndata: {LOG_LEVEL: debug}\n"
	deployment := func(name, annotations, env string) string {
		return fmt.Sprintf("---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: %s%s}\nspec:\n  template:\n    spec:\n      containers:\n        - name: %s\n          image: acme/%s:1\n%s", name, annotations, name, name, env)
	}
	const (
		annotated = ", annotations: {gregale.dev/env-import: \"true\"}"
		envFrom   = "          envFrom: [{configMapRef: {name: cfg}}]\n"
		keyRef    = "          env: [{name: LEVEL, valueFrom: {configMapKeyRef: {name: cfg, key: LOG_LEVEL}}}]\n"
	)
	tests := []struct {
		name    string
		body    string
		want    string
		wantNot string
	}{
		{"unread", configMap + deployment("api", annotated, ""), `kind ConfigMap "cfg" ignored: its values are not imported`, "values not imported into"},
		{"annotated reader", configMap + deployment("api", annotated, envFrom), "", `ConfigMap "cfg"`},
		{"unannotated reader", configMap + deployment("api", annotated, envFrom) + deployment("worker", "", keyRef),
			"kind ConfigMap \"cfg\": values not imported into worker; annotate the workload with `gregale.dev/env-import: \"true\"`", "ignored: its values"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ScanWithOptions(k8sPodFS(tc.body), k8sOn())
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(result.Warnings, "\n")
			if tc.want != "" && !strings.Contains(joined, tc.want) {
				t.Errorf("warnings missing %q:\n%s", tc.want, joined)
			}
			if strings.Contains(joined, tc.wantNot) {
				t.Errorf("warnings have %q:\n%s", tc.wantNot, joined)
			}
			if strings.Contains(joined, "debug") {
				t.Errorf("a ConfigMap value reached a warning:\n%s", joined)
			}
		})
	}
}
