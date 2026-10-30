package reposcan

import (
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// k8sAutoscaler is a HorizontalPodAutoscaler waiting to be matched to the
// Deployment it scales (ADR-20261029093621 §4). An HPA can sit in any file,
// so the scan matches them after it has read every Deployment.
type k8sAutoscaler struct {
	src, name              string
	targetKind, targetName string
	maxReplicas            *int
	warnings               []string
}

// k8sScanBudget bounds one scan's Kubernetes manifests (ADR-20261029093621
// §7). It applies whether or not the kubernetes-manifests capability is on:
// it bounds the work every scan does.
type k8sScanBudget struct {
	files int
	bytes int64
}

// readFile counts p against the budget and reads it.
func (b *k8sScanBudget) readFile(fsys fs.FS, p string, entry fs.DirEntry) ([]byte, error) {
	b.files++
	if b.files > api.KubernetesScanMaxFiles {
		return nil, fmt.Errorf("reposcan: %s is Kubernetes manifest file %d, above the limit of %d", p, b.files, api.KubernetesScanMaxFiles)
	}
	if info, err := entry.Info(); err == nil && b.bytes+info.Size() > api.KubernetesScanMaxBytes {
		return nil, fmt.Errorf("reposcan: %s brings Kubernetes manifests to %d bytes, above the limit of %d", p, b.bytes+info.Size(), api.KubernetesScanMaxBytes)
	}
	body, err := readValidFile(fsys, p)
	if err != nil {
		return nil, err
	}
	b.bytes += int64(len(body))
	if b.bytes > api.KubernetesScanMaxBytes {
		return nil, fmt.Errorf("reposcan: %s brings Kubernetes manifests to %d bytes, above the limit of %d", p, b.bytes, api.KubernetesScanMaxBytes)
	}
	return body, nil
}

// checkDocuments bounds the documents in one file.
func (b *k8sScanBudget) checkDocuments(p string, docs int) error {
	if docs > api.KubernetesScanMaxDocuments {
		return fmt.Errorf("reposcan: %s has %d YAML documents, above the limit of %d", p, docs, api.KubernetesScanMaxDocuments)
	}
	return nil
}

// k8sDeploymentReplicas reads a Deployment's spec.replicas as its instance
// cap, as Compose's deploy.replicas is read (ADR-20261023120000).
func k8sDeploymentReplicas(s *workloadSeed, doc *yaml.Node) ([]string, error) {
	var obj map[string]any
	if err := doc.Decode(&obj); err != nil {
		return nil, err
	}
	raw, ok := k8sMap(obj, "spec")["replicas"]
	if !ok || raw == nil {
		return nil, nil
	}
	n, err := composeCount(raw)
	if err != nil {
		return nil, fmt.Errorf("spec.replicas: %w", err)
	}
	if n == 0 {
		return []string{"spec.replicas 0 ignored: instances start on demand; remove the Deployment to stop deploying it"}, nil
	}
	k8sSetReplicas(s, n)
	return nil, nil
}

func k8sSetReplicas(s *workloadSeed, n int) {
	if s.resources == nil {
		s.resources = &api.ComposeResources{}
	}
	s.resources.Replicas = &n
}

// k8sReadAutoscaler reads the fields of an HPA the scan applies or reports.
// minReplicas is never imported: instances start on demand and park when
// idle, and keeping some warm is a billed choice made in gregale.yaml.
func k8sReadAutoscaler(src string, doc *yaml.Node, h k8sHeader) (k8sAutoscaler, error) {
	var obj map[string]any
	if err := doc.Decode(&obj); err != nil {
		return k8sAutoscaler{}, err
	}
	spec := k8sMap(obj, "spec")
	ref := k8sMap(spec, "scaleTargetRef")
	a := k8sAutoscaler{src: src, name: h.Metadata.Name}
	a.targetKind, _ = ref["kind"].(string)
	a.targetName, _ = ref["name"].(string)
	if raw, ok := spec["maxReplicas"]; ok && raw != nil {
		n, err := composeCount(raw)
		if err != nil {
			return k8sAutoscaler{}, fmt.Errorf("spec.maxReplicas: %w", err)
		}
		if n > 0 {
			a.maxReplicas = &n
		}
	}
	if raw, ok := spec["minReplicas"]; ok && raw != nil {
		a.warnings = append(a.warnings, fmt.Sprintf("minReplicas %v not imported: instances start on demand and park when idle; keep instances warm with scaling.min_instances in gregale.yaml, billed while resident", raw))
	}
	if raw, ok := spec["targetCPUUtilizationPercentage"]; ok && raw != nil {
		a.warnings = append(a.warnings, k8sCPUTargetWarning(raw))
	}
	metrics, _ := spec["metrics"].([]any)
	for _, raw := range metrics {
		metric, _ := raw.(map[string]any)
		a.warnings = append(a.warnings, k8sMetricWarning(metric))
	}
	if _, ok := spec["behavior"]; ok {
		a.warnings = append(a.warnings, "behavior ignored: scale-out and scale-in cooldowns are platform policy")
	}
	return a, nil
}

func k8sMetricWarning(metric map[string]any) string {
	typ, _ := metric["type"].(string)
	if typ != "Resource" {
		return fmt.Sprintf("metric type %s ignored: scale on an application metric with scaling.targets metric custom in gregale.yaml", typ)
	}
	resource := k8sMap(metric, "resource")
	name, _ := resource["name"].(string)
	utilization, ok := k8sMap(resource, "target")["averageUtilization"]
	if !ok {
		utilization, ok = resource["targetAverageUtilization"]
	}
	switch {
	case name == "cpu" && ok:
		return k8sCPUTargetWarning(utilization)
	case name == "cpu":
		return "cpu target ignored: scale on each instance's CPU percentage with scaling.targets metric cpu in gregale.yaml"
	case name == "memory":
		return "memory target ignored: instances do not scale on memory; size them with resources.limits.memory"
	}
	return fmt.Sprintf("resource metric %s ignored: not a Gregale scaling signal", name)
}

func k8sCPUTargetWarning(utilization any) string {
	return fmt.Sprintf("CPU utilization target %v%% ignored: the equivalent is scaling.targets metric cpu value %v in gregale.yaml, which measures each instance's CPU rather than its CPU request", utilization, utilization)
}

// applyK8sAutoscalers sets each HPA's maxReplicas as the instance cap of the
// Deployment it targets, which replaces that Deployment's spec.replicas as
// it does in Kubernetes, and reports what it does not apply.
func applyK8sAutoscalers(seeds []workloadSeed, autoscalers []k8sAutoscaler) []string {
	var warnings []string
	matched := map[string]string{}
	for _, a := range autoscalers {
		label := fmt.Sprintf("reposcan: %s: kind HorizontalPodAutoscaler %q", a.src, a.name)
		if a.targetKind != "Deployment" {
			warnings = append(warnings, fmt.Sprintf("%s ignored: it scales %s %q, and only Deployments deploy as scaled apps", label, a.targetKind, a.targetName))
			continue
		}
		index := -1
		for i := range seeds {
			if seeds[i].class == ClassHTTP && seeds[i].name == a.targetName {
				index = i
				break
			}
		}
		if index < 0 {
			warnings = append(warnings, fmt.Sprintf("%s ignored: Deployment %q is not an app in the scan", label, a.targetName))
			continue
		}
		if other, ok := matched[a.targetName]; ok {
			warnings = append(warnings, fmt.Sprintf("%s ignored: HorizontalPodAutoscaler %q already scales Deployment %q", label, other, a.targetName))
			continue
		}
		matched[a.targetName] = a.name
		reasons := a.warnings
		if a.maxReplicas != nil {
			k8sSetReplicas(&seeds[index], *a.maxReplicas)
		} else {
			reasons = append([]string{"maxReplicas missing: the instance cap stays the Deployment's"}, reasons...)
		}
		hpaWarnings := make([]string, 0, len(reasons))
		for _, reason := range reasons {
			hpaWarnings = append(hpaWarnings, fmt.Sprintf("HorizontalPodAutoscaler %q: %s", a.name, reason))
		}
		warnings = append(warnings, k8sSeedWarnings(a.src, a.targetName, hpaWarnings)...)
	}
	return warnings
}

// ReplicasKey names the setting Resources.Replicas came from, for messages
// about it: Compose's deploy.replicas, or a Kubernetes Deployment's replicas
// (or its HorizontalPodAutoscaler's maxReplicas).
func (w Workload) ReplicasKey() string {
	if w.DetectedBy.Detector == detNameK8s {
		return "replicas"
	}
	return "deploy.replicas"
}
