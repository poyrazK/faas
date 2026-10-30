package reposcan

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

const k8sTestDeployment = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec: {template: {spec: {containers: [{name: api, image: ghcr.io/acme/api:1}]}}}
`

// TestDetectK8s_NonKubernetesYAMLIsSkipped pins ADR-20261029093621 §1: YAML
// under a manifest directory that is not a Kubernetes object is skipped with
// one warning per file instead of failing the whole scan. An Ansible playbook
// (a top-level list) failed every project scan of a repository with deploy/.
func TestDetectK8s_NonKubernetesYAMLIsSkipped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		wantSeeds   int
		wantWarning string
	}{
		{
			name:        "ansible playbook",
			body:        "- hosts: all\n  tasks:\n    - name: ping\n      ping: {}\n",
			wantWarning: "deploy/site.yml: skipped 1 YAML document(s) that are not Kubernetes objects",
		},
		{
			name:        "values file",
			body:        "image: {repository: acme/api, tag: \"1\"}\nreplicas: 2\n",
			wantWarning: "deploy/site.yml: skipped 1 YAML document(s)",
		},
		{
			name:        "kind without apiVersion",
			body:        "kind: Deployment\nmetadata: {name: api}\n",
			wantWarning: "deploy/site.yml: skipped 1 YAML document(s)",
		},
		{
			name:        "mixed stream keeps the object",
			body:        "- not: an object\n---\n" + k8sTestDeployment,
			wantSeeds:   1,
			wantWarning: "deploy/site.yml: skipped 1 YAML document(s)",
		},
		{
			name:      "comment-only document is not counted",
			body:      "# nothing here\n---\n" + k8sTestDeployment,
			wantSeeds: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			seeds, _, warnings, err := detectK8s(fstest.MapFS{
				"deploy":          &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
				"deploy/site.yml": &fstest.MapFile{Data: []byte(tt.body)},
			})
			if err != nil {
				t.Fatalf("detectK8s: %v", err)
			}
			if len(seeds) != tt.wantSeeds {
				t.Errorf("seeds = %v, want %d", names(seeds), tt.wantSeeds)
			}
			if tt.wantWarning == "" {
				if len(warnings) != 0 {
					t.Errorf("warnings = %q, want none", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tt.wantWarning) {
				t.Errorf("warnings = %q, want one containing %q", warnings, tt.wantWarning)
			}
		})
	}
}

// TestDetectK8s_RenderedSourcesAreSkipped — Helm templates and Kustomize
// patches are not the objects that run. Their directories are skipped with
// the render command, and nothing inside is read: a template that does not
// parse, or a patch without an image, must not fail the scan.
func TestDetectK8s_RenderedSourcesAreSkipped(t *testing.T) {
	t.Parallel()
	partialPatch := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: api}\nspec: {replicas: 3}\n"
	tests := []struct {
		name        string
		files       map[string]string
		wantWarning string
	}{
		{
			name: "helm chart",
			files: map[string]string{
				"deploy/chart/Chart.yaml":                "apiVersion: v2\nname: api\n",
				"deploy/chart/templates/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ .Release.Name }\n",
			},
			wantWarning: "reposcan: deploy/chart: skipped Helm chart; render it with `helm template <release> deploy/chart`",
		},
		{
			name: "kustomize overlay",
			files: map[string]string{
				"deploy/overlays/prod/kustomization.yaml": "resources: [../../base]\npatches: [{path: replicas.yaml}]\n",
				"deploy/overlays/prod/replicas.yaml":      partialPatch,
			},
			wantWarning: "reposcan: deploy/overlays/prod: skipped Kustomize directory; render it with `kubectl kustomize deploy/overlays/prod`",
		},
		{
			name: "chart at the manifest root",
			files: map[string]string{
				"k8s/Chart.yaml":       "apiVersion: v2\nname: api\n",
				"k8s/templates/x.yaml": partialPatch,
			},
			wantWarning: "reposcan: k8s: skipped Helm chart",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{"deploy/plain/api.yaml": &fstest.MapFile{Data: []byte(k8sTestDeployment)}}
			for name, body := range tt.files {
				fsys[name] = &fstest.MapFile{Data: []byte(body)}
			}
			seeds, _, warnings, err := detectK8s(fsys)
			if err != nil {
				t.Fatalf("detectK8s: %v", err)
			}
			if got := names(seeds); !equalSet(got, []string{"api"}) {
				t.Errorf("seeds = %v, want only the plain deploy/plain Deployment", got)
			}
			if len(warnings) != 1 || !strings.HasPrefix(warnings[0], tt.wantWarning) {
				t.Errorf("warnings = %q, want one starting %q", warnings, tt.wantWarning)
			}
		})
	}
}

// TestDetectK8s_KindsAreExplained pins ADR-20261029093621 §2: an object that
// is not a workload is reported with what to use instead, a few kinds stay
// quiet, and no Secret or ConfigMap value reaches a warning.
func TestDetectK8s_KindsAreExplained(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string // "" means no warning
	}{
		{"cluster service is quiet", "apiVersion: v1\nkind: Service\nmetadata: {name: api}\nspec: {ports: [{port: 80}]}\n", ""},
		{"load balancer service", "apiVersion: v1\nkind: Service\nmetadata: {name: api}\nspec: {type: LoadBalancer}\n",
			`kind Service "api": type LoadBalancer ignored: every app gets an HTTPS URL`},
		{"namespace is quiet", "apiVersion: v1\nkind: Namespace\nmetadata: {name: shop}\n", ""},
		{"pdb is quiet", "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata: {name: api}\n", ""},
		{"secret without data", "apiVersion: v1\nkind: Secret\nmetadata: {name: db}\n",
			`kind Secret "db" ignored: values are never imported; set them with ` + "`gregale env set`"},
		{"committed secret", "apiVersion: v1\nkind: Secret\nmetadata: {name: db}\nstringData: {PASSWORD: hunter2}\ndata: {TOKEN: c2VjcmV0}\n",
			"should be rotated"},
		{"configmap", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cfg}\ndata: {LOG_LEVEL: debug}\n",
			`kind ConfigMap "cfg" ignored: its values are not imported`},
		{"ingress hosts", "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: web}\nspec: {rules: [{host: b.example.com}, {host: a.example.com}, {host: b.example.com}]}\n",
			"add a.example.com, b.example.com with `gregale domain add`"},
		{"httproute hostnames", "apiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute\nmetadata: {name: web}\nspec: {hostnames: [api.example.com], rules: [{matches: [{path: {value: /}}]}]}\n",
			"add api.example.com with `gregale domain add`"},
		{"ingress of unexpected shape degrades", "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: web}\nspec: {rules: oops}\n",
			`kind Ingress "web" ignored: every app gets an HTTPS URL`},
		{"daemonset refused", "apiVersion: apps/v1\nkind: DaemonSet\nmetadata: {name: agent}\n",
			"refusing DaemonSet agent (there are no per-node workloads"},
		{"job", "apiVersion: batch/v1\nkind: Job\nmetadata: {name: seed}\n", "`gregale job create`"},
		{"pvc", "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata: {name: data}\n", "persistent volumes and bind mounts are not supported"},
		{"network policy", "apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata: {name: deny}\n", "`gregale app egress-allowlist`"},
		{"hpa", "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata: {name: api}\n", "`gregale app scale`"},
		{"irsa service account", "apiVersion: v1\nkind: ServiceAccount\nmetadata: {name: api, annotations: {eks.amazonaws.com/role-arn: 'arn:aws:iam::1:role/api'}}\n",
			"for the role in eks.amazonaws.com/role-arn, trust the subject app:<app_id>"},
		{"unquoted annotation value does not fail", "apiVersion: v1\nkind: ServiceAccount\nmetadata: {name: api, annotations: {example.com/flag: true}}\n",
			`kind ServiceAccount "api" ignored: people and CI use scoped keys`},
		{"custom resource", "apiVersion: cert-manager.io/v1\nkind: Certificate\nmetadata: {name: tls}\nspec: {template: not-a-pod}\n",
			`kind Certificate "tls" ignored: the scan does not read this kind`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			seeds, _, warnings, err := detectK8s(fstest.MapFS{
				"k8s/object.yaml": &fstest.MapFile{Data: []byte(tt.body)},
			})
			if err != nil {
				t.Fatalf("detectK8s: %v", err)
			}
			if len(seeds) != 0 {
				t.Errorf("seeds = %v, want none", names(seeds))
			}
			if tt.want == "" {
				if len(warnings) != 0 {
					t.Errorf("warnings = %q, want none", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tt.want) {
				t.Fatalf("warnings = %q, want one containing %q", warnings, tt.want)
			}
			for _, secret := range []string{"hunter2", "c2VjcmV0", "debug"} {
				if strings.Contains(warnings[0], secret) {
					t.Errorf("warning %q carries a value from the object", warnings[0])
				}
			}
		})
	}
}

// TestDetectK8s_KindWarningsNameNoWorkload — --explain must not attribute an
// explained object or a skipped file to a workload named "kind" or
// "skipped"; a refused DaemonSet is attributed to its own name, and a
// "<name>: <warning>" warning to that name without its colon.
func TestDetectK8s_KindWarningsNameNoWorkload(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		`reposcan: k8s/a.yaml: kind Secret "db" ignored: values are never imported`:             "",
		"reposcan: deploy/site.yml: skipped 1 YAML document(s) that are not Kubernetes objects": "",
		"reposcan: deploy/chart: skipped Helm chart; render it":                                 "",
		"reposcan: k8s/a.yaml: refusing DaemonSet agent (there are no per-node workloads)":      "agent",
		"reposcan: k8s/a.yaml: refusing StatefulSet db (stateless contract, ADR-046)":           "db",
		"reposcan: k8s/a.yaml: api: spec.replicas 0 ignored: instances start on demand":         "api",
		"reposcan: compose.yaml: web: deploy.mode global ignored":                               "web",
	}
	for raw, want := range tests {
		if got := detectionWarningFromString(detK8s, raw).Workload; got != want {
			t.Errorf("Workload(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestScan_AnsibleUnderDeployDoesNotFailProject — the whole-scan regression:
// a repository whose deploy/ holds an Ansible playbook next to its
// Kubernetes manifests plans its workloads, and --explain shows the skip.
func TestScan_AnsibleUnderDeployDoesNotFailProject(t *testing.T) {
	t.Parallel()
	result, err := Scan(fstest.MapFS{
		"deploy/site.yml": &fstest.MapFile{Data: []byte("- hosts: all\n  roles: [web]\n")},
		"deploy/api.yaml": &fstest.MapFile{Data: []byte(k8sTestDeployment)},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var got []string
	for _, workload := range result.Workloads {
		got = append(got, workload.Name)
	}
	if !equalSet(got, []string{"api"}) {
		t.Errorf("workloads = %v, want {api}", got)
	}
	found := false
	for _, warning := range result.DetectionWarnings {
		if warning.Marker == "deploy/site.yml" && warning.Workload == "" && strings.HasPrefix(warning.Reason, "skipped 1 YAML document(s)") {
			found = true
		}
	}
	if !found {
		t.Errorf("detection warnings = %+v, want the skipped playbook", result.DetectionWarnings)
	}
}
