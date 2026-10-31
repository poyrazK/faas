package reposcan

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kubernetes objects the scan does not turn into workloads are reported
// instead of dropped silently (ADR-20261029093621 §2, the Kubernetes form of
// ADR-1337). k8sQuietKinds have nothing to explain: the platform owns
// placement, admission and namespaces. k8sKindReasons say why a kind has no
// effect and what to use instead; any other kind gets a generic warning.
var k8sQuietKinds = map[string]bool{
	"Namespace": true, "PodDisruptionBudget": true, "PriorityClass": true,
	"LimitRange": true, "ResourceQuota": true,
}

const k8sIdentityReason = "people and CI use scoped keys and organization roles; apps get cloud credentials through workload identity (docs/workload-identity.md)"

var k8sKindReasons = map[string]string{
	"Job":                     "one-off Jobs run with `gregale job create` and `gregale job run`",
	"PersistentVolumeClaim":   composeVolumesReason,
	"PersistentVolume":        composeVolumesReason,
	"StorageClass":            composeVolumesReason,
	"NetworkPolicy":           "apps reach each other only through declared service bindings, private and metadata ranges are always blocked, and `gregale app egress-allowlist` narrows outbound traffic",
	"ServiceAccount":          k8sIdentityReason,
	"Role":                    k8sIdentityReason,
	"RoleBinding":             k8sIdentityReason,
	"ClusterRole":             k8sIdentityReason,
	"ClusterRoleBinding":      k8sIdentityReason,
	"HorizontalPodAutoscaler": "set the instance range with `gregale app scale` or `scaling` in gregale.yaml; instances start on demand and park when idle",
	"ConfigMap":               "its values are not imported; set them with `gregale env set`",
}

// k8sCloudRoleAnnotations name the cloud role a ServiceAccount assumes on
// EKS, GKE and AKS. Gregale's equivalent is a workload identity trust policy.
var k8sCloudRoleAnnotations = []string{
	"eks.amazonaws.com/role-arn",
	"iam.gke.io/gcp-service-account",
	"azure.workload.identity/client-id",
}

// k8sHeader is what every Kubernetes object carries. It is decoded before
// the kind is known, so it must not include kind-specific fields: a field
// one kind uses differently would fail the scan for an object it never reads.
type k8sHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
		// any, not string: an unquoted `true` must not fail the scan.
		Annotations map[string]any `yaml:"annotations"`
	} `yaml:"metadata"`
}

// k8sObjectHeader reports whether a decoded YAML document is a Kubernetes
// object: a mapping with a non-empty scalar apiVersion and kind. Anything
// else under a manifest directory (an Ansible playbook, a values file) is not
// one and is skipped by the caller.
func k8sObjectHeader(doc *yaml.Node) (k8sHeader, bool, error) {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode || !k8sHasScalar(root, "apiVersion") || !k8sHasScalar(root, "kind") {
		return k8sHeader{}, false, nil
	}
	var h k8sHeader
	if err := root.Decode(&h); err != nil {
		return k8sHeader{}, true, err
	}
	return h, true, nil
}

func k8sHasScalar(mapping *yaml.Node, key string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			value := mapping.Content[i+1]
			return value.Kind == yaml.ScalarNode && strings.TrimSpace(value.Value) != ""
		}
	}
	return false
}

// k8sRenderedSourceWarning reports a Helm chart or Kustomize directory. Their
// files are templates and patches, not the objects that run, so the scan
// skips the directory and names the command that renders it.
func k8sRenderedSourceWarning(fsys fs.FS, dir string) (string, bool) {
	if k8sFileExists(fsys, path.Join(dir, "Chart.yaml")) {
		return fmt.Sprintf("reposcan: %s: skipped Helm chart; render it with `helm template <release> %s` and commit the output outside this directory", dir, dir), true
	}
	for _, name := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if k8sFileExists(fsys, path.Join(dir, name)) {
			return fmt.Sprintf("reposcan: %s: skipped Kustomize directory; render it with `kubectl kustomize %s` and commit the output outside this directory", dir, dir), true
		}
	}
	return "", false
}

func k8sFileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// k8sKindWarning explains an object that is not a workload. It never
// includes a value from the object: Secret and ConfigMap data stay in the
// source.
func k8sKindWarning(src string, doc *yaml.Node, h k8sHeader) string {
	if k8sQuietKinds[h.Kind] {
		return ""
	}
	if h.Kind == "DaemonSet" {
		return "reposcan: " + src + ": refusing DaemonSet " + h.Metadata.Name +
			" (there are no per-node workloads; the platform owns its nodes)"
	}
	label := fmt.Sprintf("reposcan: %s: kind %s %q", src, h.Kind, h.Metadata.Name)
	switch h.Kind {
	case "Service":
		if typ := k8sServiceType(doc); typ == "LoadBalancer" || typ == "NodePort" {
			return label + ": type " + typ + " ignored: every app gets an HTTPS URL"
		}
		return ""
	case "Secret":
		reason := "values are never imported; set them with `gregale env set`"
		if k8sSecretHasData(doc) {
			reason += "; base64 is not encryption, so the values committed here are in the repository's history and should be rotated"
		}
		return label + " ignored: " + reason
	case "Ingress", "HTTPRoute":
		reason := "every app gets an HTTPS URL"
		switch hosts := k8sRouteHosts(doc, h.Kind); len(hosts) {
		case 0:
		case 1:
			reason += "; add " + hosts[0] + " with `gregale domain create --app <app> --domain " + hosts[0] + "`"
		default:
			reason += "; add " + strings.Join(hosts, ", ") + " with `gregale domain create --app <app> --domain <host>`"
		}
		return label + " ignored: " + reason
	}
	reason, ok := k8sKindReasons[h.Kind]
	if !ok {
		return label + " ignored: the scan does not read this kind"
	}
	if h.Kind == "ServiceAccount" {
		for _, annotation := range k8sCloudRoleAnnotations {
			if value, ok := h.Metadata.Annotations[annotation]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
				reason += "; for the role in " + annotation + ", trust the subject app:<app_id>"
				break
			}
		}
	}
	return label + " ignored: " + reason
}

// The decoders below only refine a warning, so a field of an unexpected
// shape degrades the explanation instead of failing the scan: yaml.v3 fills
// what it can before it returns the type error, which is deliberately
// discarded.

func k8sServiceType(doc *yaml.Node) string {
	var obj struct {
		Spec struct {
			Type string `yaml:"type"`
		} `yaml:"spec"`
	}
	_ = doc.Decode(&obj)
	return obj.Spec.Type
}

func k8sSecretHasData(doc *yaml.Node) bool {
	var obj struct {
		Data       map[string]any `yaml:"data"`
		StringData map[string]any `yaml:"stringData"`
	}
	_ = doc.Decode(&obj)
	return len(obj.Data) > 0 || len(obj.StringData) > 0
}

func k8sRouteHosts(doc *yaml.Node, kind string) []string {
	var obj struct {
		Spec struct {
			Rules []struct {
				Host string `yaml:"host"`
			} `yaml:"rules"`
			Hostnames []string `yaml:"hostnames"`
		} `yaml:"spec"`
	}
	_ = doc.Decode(&obj)
	hosts := obj.Spec.Hostnames
	if kind == "Ingress" {
		hosts = nil
		for _, rule := range obj.Spec.Rules {
			hosts = append(hosts, rule.Host)
		}
	}
	var out []string
	for _, host := range hosts {
		if host = strings.TrimSpace(host); host != "" && !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	slices.Sort(out)
	return out
}
