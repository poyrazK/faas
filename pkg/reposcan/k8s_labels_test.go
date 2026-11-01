package reposcan

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const k8sLabelledDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels:
    team: billing
    app.kubernetes.io/name: web
    gregale.dev/owner: someone
    bad key: x
    tier: -web
spec:
  template:
    metadata:
      labels: {pod-only: "yes"}
    spec:
      containers: [{name: web, image: acme/web:1}]
`

// With the resource-labels capability a Deployment's and a CronJob's
// metadata.labels reach their workloads; reserved and invalid labels are
// dropped with a warning, and pod template labels stay behind
// (ADR-20261031100025 §7). Without it no labels are read.
func TestScanWithOptions_KubernetesLabels(t *testing.T) {
	t.Parallel()
	cronJob := strings.Replace(k8sCronJob(""), "metadata: {name: report}", "metadata: {name: report, labels: {team: billing, schedule: nightly}}", 1)
	fsys := k8sPodFS(k8sLabelledDeployment + "---\n" + cronJob)

	result, err := ScanWithOptions(fsys, ScanOptions{ResourceLabels: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]string{}
	for _, w := range result.Workloads {
		byName[w.Name] = w.Labels
	}
	if want := map[string]string{"team": "billing", "app.kubernetes.io/name": "web"}; !maps.Equal(byName["web"], want) {
		t.Errorf("web labels = %v, want %v", byName["web"], want)
	}
	if want := map[string]string{"team": "billing", "schedule": "nightly"}; !maps.Equal(byName["report"], want) {
		t.Errorf("report labels = %v, want %v", byName["report"], want)
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{`"gregale.dev/owner" not imported`, `"bad key"`, `"tier"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "pod-only") {
		t.Errorf("pod template labels were read:\n%s", joined)
	}

	off, err := ScanWithOptions(fsys, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range off.Workloads {
		if w.Labels != nil {
			t.Errorf("%s carries labels without the capability: %v", w.Name, w.Labels)
		}
	}
	if slices.ContainsFunc(off.Warnings, func(w string) bool { return strings.Contains(w, "not imported") }) {
		t.Errorf("label warnings without the capability: %v", off.Warnings)
	}
}

// An object with more labels than an app carries keeps the first ones in key
// order and reports the rest.
func TestK8sLabelsLimit(t *testing.T) {
	t.Parallel()
	var m k8sManifest
	m.Metadata.Name = "many"
	m.Metadata.Labels = map[string]string{}
	for i := range api.LabelsPerResourceMax + 2 {
		m.Metadata.Labels[fmt.Sprintf("k%02d", i)] = "v"
	}
	var warnings []string
	got := k8sLabels(&warnings, "k8s.yaml", "Deployment", m)
	if len(got) != api.LabelsPerResourceMax || got["k00"] != "v" || got[fmt.Sprintf("k%02d", api.LabelsPerResourceMax)] != "" {
		t.Fatalf("kept %d labels: %v", len(got), got)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "at most 32 labels") {
		t.Fatalf("warnings = %v", warnings)
	}
}
