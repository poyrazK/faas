package reposcan

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func k8sReplicaDeployment(name, replicas string) string {
	body := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: " + name + "}\nspec:\n"
	if replicas != "" {
		body += "  replicas: " + replicas + "\n"
	}
	return body + "  template:\n    spec:\n      containers: [{name: " + name + ", image: acme/" + name + ":1}]\n"
}

func k8sSeedNamed(t *testing.T, seeds []workloadSeed, name string) workloadSeed {
	t.Helper()
	for _, s := range seeds {
		if s.name == name {
			return s
		}
	}
	t.Fatalf("no seed %q in %+v", name, seeds)
	return workloadSeed{}
}

func k8sSeedReplicas(s workloadSeed) int {
	if s.resources == nil || s.resources.Replicas == nil {
		return 0
	}
	return *s.resources.Replicas
}

func TestDetectK8s_DeploymentReplicas(t *testing.T) {
	for _, tc := range []struct {
		name, replicas string
		opts           ScanOptions
		want           int
		wantWarning    string
		wantErr        string
	}{
		{name: "capability off keeps today's mapping", replicas: "3", opts: ScanOptions{}, want: 0},
		{name: "replicas cap instances", replicas: "3", opts: k8sOn(), want: 3},
		{name: "no replicas keeps the plan default", opts: k8sOn(), want: 0},
		{name: "zero is reported, not applied", replicas: "0", opts: k8sOn(), wantWarning: "api: spec.replicas 0 ignored: instances start on demand"},
		{name: "not a count", replicas: "lots", opts: k8sOn(), wantErr: "spec.replicas: lots must be a whole number"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seeds, _, warnings, err := detectK8sWith(k8sPodFS(k8sReplicaDeployment("api", tc.replicas)), tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := k8sSeedReplicas(k8sSeedNamed(t, seeds, "api")); got != tc.want {
				t.Errorf("replicas = %d, want %d", got, tc.want)
			}
			if tc.wantWarning != "" && !strings.Contains(strings.Join(warnings, "\n"), tc.wantWarning) {
				t.Errorf("warnings = %q, want %q", warnings, tc.wantWarning)
			}
		})
	}
}

// An HPA's maxReplicas replaces its Deployment's replicas as the instance
// cap wherever the HPA sits in the repository; minReplicas, metrics and
// behavior are reported against the Deployment's workload.
func TestDetectK8s_AutoscalerSetsInstanceCap(t *testing.T) {
	fsys := fstest.MapFS{
		"k8s/a-hpa.yaml": &fstest.MapFile{Data: []byte(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: api-hpa}
spec:
  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: api}
  minReplicas: 2
  maxReplicas: 8
  metrics:
    - type: Resource
      resource: {name: cpu, target: {type: Utilization, averageUtilization: 70}}
    - type: Resource
      resource: {name: memory, target: {type: Utilization, averageUtilization: 80}}
    - type: External
      external: {metric: {name: queue_messages}, target: {type: AverageValue, averageValue: "30"}}
  behavior:
    scaleDown: {stabilizationWindowSeconds: 300}
`)},
		"k8s/b-deploy.yaml": &fstest.MapFile{Data: []byte(k8sReplicaDeployment("api", "2"))},
	}
	seeds, _, warnings, err := detectK8sWith(fsys, k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	if got := k8sSeedReplicas(k8sSeedNamed(t, seeds, "api")); got != 8 {
		t.Errorf("replicas = %d, want maxReplicas 8", got)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		`reposcan: k8s/a-hpa.yaml: api: HorizontalPodAutoscaler "api-hpa": minReplicas 2 not imported: instances start on demand and park when idle; keep instances warm with scaling.min_instances`,
		`HorizontalPodAutoscaler "api-hpa": CPU utilization target 70% ignored: the equivalent is scaling.targets metric cpu value 70`,
		`HorizontalPodAutoscaler "api-hpa": memory target ignored`,
		`HorizontalPodAutoscaler "api-hpa": metric type External ignored`,
		`HorizontalPodAutoscaler "api-hpa": behavior ignored`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}
	for _, w := range warnings {
		if strings.Contains(w, "api-hpa") {
			if got := detectionWarningFromString(detK8s, w).Workload; got != "api" {
				t.Errorf("warning %q names workload %q, want api", w, got)
			}
		}
	}
}

func TestDetectK8s_AutoscalerEdges(t *testing.T) {
	hpa := func(name, kind, target, extra string) string {
		return "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata: {name: " + name + "}\nspec:\n" +
			"  scaleTargetRef: {kind: " + kind + ", name: " + target + "}\n" + extra
	}
	for _, tc := range []struct {
		name         string
		docs         []string
		opts         ScanOptions
		wantReplicas int
		wantWarnings []string
		wantErr      string
	}{
		{
			name:         "capability off reports the kind",
			docs:         []string{k8sReplicaDeployment("api", "2"), hpa("api", "Deployment", "api", "  maxReplicas: 8\n")},
			opts:         ScanOptions{},
			wantWarnings: []string{`kind HorizontalPodAutoscaler "api" ignored: set the instance range`},
		},
		{
			name:         "v1 cpu target",
			docs:         []string{k8sReplicaDeployment("api", ""), hpa("api", "Deployment", "api", "  maxReplicas: 4\n  targetCPUUtilizationPercentage: 60\n")},
			opts:         k8sOn(),
			wantReplicas: 4,
			wantWarnings: []string{`CPU utilization target 60% ignored`},
		},
		{
			name:         "missing maxReplicas keeps the Deployment's replicas",
			docs:         []string{k8sReplicaDeployment("api", "3"), hpa("api", "Deployment", "api", "")},
			opts:         k8sOn(),
			wantReplicas: 3,
			wantWarnings: []string{`api: HorizontalPodAutoscaler "api": maxReplicas missing`},
		},
		{
			name:         "a StatefulSet target is not an app",
			docs:         []string{hpa("db", "StatefulSet", "db", "  maxReplicas: 3\n")},
			opts:         k8sOn(),
			wantWarnings: []string{`kind HorizontalPodAutoscaler "db" ignored: it scales StatefulSet "db"`},
		},
		{
			name:         "an unknown Deployment",
			docs:         []string{hpa("web", "Deployment", "web", "  maxReplicas: 3\n")},
			opts:         k8sOn(),
			wantWarnings: []string{`kind HorizontalPodAutoscaler "web" ignored: Deployment "web" is not an app in the scan`},
		},
		{
			name: "a second autoscaler for one Deployment",
			docs: []string{
				k8sReplicaDeployment("api", ""),
				hpa("first", "Deployment", "api", "  maxReplicas: 5\n"),
				hpa("second", "Deployment", "api", "  maxReplicas: 9\n"),
			},
			opts:         k8sOn(),
			wantReplicas: 5,
			wantWarnings: []string{`kind HorizontalPodAutoscaler "second" ignored: HorizontalPodAutoscaler "first" already scales Deployment "api"`},
		},
		{
			name:    "maxReplicas not a count",
			docs:    []string{hpa("api", "Deployment", "api", "  maxReplicas: many\n")},
			opts:    k8sOn(),
			wantErr: `HorizontalPodAutoscaler "api": spec.maxReplicas: many must be a whole number`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seeds, _, warnings, err := detectK8sWith(k8sPodFS(strings.Join(tc.docs, "---\n")), tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantReplicas != 0 {
				if got := k8sSeedReplicas(k8sSeedNamed(t, seeds, "api")); got != tc.wantReplicas {
					t.Errorf("replicas = %d, want %d", got, tc.wantReplicas)
				}
			}
			joined := strings.Join(warnings, "\n")
			for _, want := range tc.wantWarnings {
				if !strings.Contains(joined, want) {
					t.Errorf("warnings missing %q:\n%s", want, joined)
				}
			}
		})
	}
}

// The bounds apply to every scan, with or without the capability, and name
// the limit and the value the repository reached.
func TestDetectK8s_ScanBounds(t *testing.T) {
	manyFiles := fstest.MapFS{}
	for i := 0; i <= api.KubernetesScanMaxFiles; i++ {
		manyFiles[fmt.Sprintf("deploy/f%04d.yaml", i)] = &fstest.MapFile{Data: []byte("a: 1\n")}
	}
	manyDocs := strings.Repeat("a: 1\n---\n", api.KubernetesScanMaxDocuments) + "a: 1\n"
	tooBig := strings.Repeat("#", api.KubernetesScanMaxBytes+1)
	for _, tc := range []struct {
		name    string
		fsys    fstest.MapFS
		wantErr string
	}{
		{"files", manyFiles, fmt.Sprintf("is Kubernetes manifest file %d, above the limit of %d", api.KubernetesScanMaxFiles+1, api.KubernetesScanMaxFiles)},
		{"documents", k8sPodFS(manyDocs), fmt.Sprintf("k8s/app.yaml has %d YAML documents, above the limit of %d", api.KubernetesScanMaxDocuments+1, api.KubernetesScanMaxDocuments)},
		{"bytes", k8sPodFS(tooBig), fmt.Sprintf("k8s/app.yaml brings Kubernetes manifests to %d bytes, above the limit of %d", api.KubernetesScanMaxBytes+1, api.KubernetesScanMaxBytes)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, opts := range []ScanOptions{{}, k8sOn()} {
				_, _, _, err := detectK8sWith(tc.fsys, opts)
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("opts %+v: err = %v, want %q", opts, err, tc.wantErr)
				}
			}
		})
	}
}

// The cap reaches the planned workload through the whole scan.
func TestScanWithOptions_KubernetesReplicasReachWorkload(t *testing.T) {
	fsys := k8sPodFS(k8sReplicaDeployment("api", "3") + "---\n" + `apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: api}
spec:
  scaleTargetRef: {kind: Deployment, name: api}
  maxReplicas: 6
`)
	result, err := ScanWithOptions(fsys, k8sOn())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Workloads) != 1 {
		t.Fatalf("workloads = %+v, want 1", result.Workloads)
	}
	w := result.Workloads[0]
	if w.Resources == nil || w.Resources.Replicas == nil || *w.Resources.Replicas != 6 {
		t.Fatalf("resources = %+v, want replicas 6", w.Resources)
	}
	if got := w.ReplicasKey(); got != "replicas" {
		t.Errorf("ReplicasKey = %q, want replicas", got)
	}
}
