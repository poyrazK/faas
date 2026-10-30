package reposcan

import (
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// K8sEnvImportAnnotation opts a Deployment or CronJob into importing its plain
// environment values (ADR-20261029093621 §5), as `x-gregale-env-import: true`
// opts a Compose file in (ADR-20261023085230).
const K8sEnvImportAnnotation = "gregale.dev/env-import"

// k8sEnvFromRef is one envFrom source of the app container.
type k8sEnvFromRef struct {
	kind     string // ConfigMap or Secret
	name     string
	prefix   string
	optional bool
}

// k8sService is a Service the scan read, to rewrite values that call it.
type k8sService struct {
	name, namespace string
	selector        map[string]string
}

// k8sEnvSources are the ConfigMaps, Secrets and Services the scan read.
// Secret values are never read, only their keys. ConfigMap values are kept
// only when reading values for the opt-in import, never during Scan.
type k8sEnvSources struct {
	configMaps map[string]map[string]string
	secrets    map[string][]string
	services   []k8sService
	withValues bool
}

func (s *k8sEnvSources) add(node *yaml.Node, h k8sHeader) error {
	switch h.Kind {
	case "ConfigMap", "Secret", "Service":
	default:
		return nil
	}
	var obj map[string]any
	if err := node.Decode(&obj); err != nil {
		return err
	}
	switch h.Kind {
	case "ConfigMap":
		values := map[string]string{}
		for key, raw := range k8sMap(obj, "data") {
			values[key] = ""
			if s.withValues {
				values[key] = fmt.Sprint(raw)
			}
		}
		if s.configMaps == nil {
			s.configMaps = map[string]map[string]string{}
		}
		s.configMaps[h.Metadata.Name] = values
	case "Secret":
		keys := slices.Collect(maps.Keys(k8sMap(obj, "data")))
		keys = append(keys, slices.Collect(maps.Keys(k8sMap(obj, "stringData")))...)
		slices.Sort(keys)
		if s.secrets == nil {
			s.secrets = map[string][]string{}
		}
		s.secrets[h.Metadata.Name] = slices.Compact(keys)
	case "Service":
		svc := k8sService{name: h.Metadata.Name, selector: map[string]string{}}
		svc.namespace, _ = k8sMap(obj, "metadata")["namespace"].(string)
		for key, raw := range k8sMap(obj, "spec", "selector") {
			svc.selector[key] = fmt.Sprint(raw)
		}
		s.services = append(s.services, svc)
	}
	return nil
}

// k8sEnvRefs records the app container's envFrom sources on the seed and
// drops env names whose value Kubernetes fills from the pod itself, which
// have no meaning on Gregale.
func k8sEnvRefs(s *workloadSeed, container map[string]any) []string {
	var warnings []string
	env, _ := container["env"].([]any)
	for _, raw := range env {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		from := k8sMap(entry, "valueFrom")
		for _, field := range []string{"fieldRef", "resourceFieldRef"} {
			if _, ok := from[field]; ok {
				s.envKeys = slices.DeleteFunc(s.envKeys, func(key string) bool { return key == name })
				warnings = append(warnings, fmt.Sprintf("env %s valueFrom.%s ignored: Gregale does not expose pod fields; set a value with `gregale env set` if the app needs one", name, field))
			}
		}
	}
	envFrom, _ := container["envFrom"].([]any)
	for _, raw := range envFrom {
		entry, _ := raw.(map[string]any)
		prefix, _ := entry["prefix"].(string)
		for kind, field := range map[string]string{"ConfigMap": "configMapRef", "Secret": "secretRef"} {
			ref, ok := entry[field].(map[string]any)
			if !ok {
				continue
			}
			name, _ := ref["name"].(string)
			optional, _ := ref["optional"].(bool)
			s.k8sEnvFrom = append(s.k8sEnvFrom, k8sEnvFromRef{kind: kind, name: name, prefix: prefix, optional: optional})
		}
	}
	return warnings
}

// applyK8sEnvFrom adds the keys of each envFrom ConfigMap and Secret the scan
// read to its workload's env names, and reports a source it did not read.
func applyK8sEnvFrom(seeds []workloadSeed, sources k8sEnvSources) []string {
	var warnings []string
	for i := range seeds {
		s := &seeds[i]
		var missing []string
		for _, ref := range s.k8sEnvFrom {
			var keys []string
			found := false
			switch ref.kind {
			case "ConfigMap":
				var values map[string]string
				values, found = sources.configMaps[ref.name]
				keys = slices.Collect(maps.Keys(values))
			case "Secret":
				keys, found = sources.secrets[ref.name]
			}
			if !found {
				if !ref.optional {
					missing = append(missing, fmt.Sprintf("envFrom %s %q is not in the scan, so its keys are unknown; set them with `gregale env set`", ref.kind, ref.name))
				}
				continue
			}
			for _, key := range keys {
				s.envKeys = append(s.envKeys, ref.prefix+key)
			}
		}
		slices.Sort(s.envKeys)
		s.envKeys = slices.Compact(s.envKeys)
		warnings = append(warnings, k8sSeedWarnings(sourceFile(s.source), s.name, missing)...)
	}
	return warnings
}

// sourceFile is the file part of a seed source ("k8s/app.yaml: api").
func sourceFile(source string) string {
	file, _, _ := strings.Cut(source, ": ")
	return file
}

// k8sEnvWorkload is a Deployment or CronJob read for its environment.
type k8sEnvWorkload struct {
	name, namespace string
	kind            string
	optIn           bool
	labels          map[string]string
	container       map[string]any
}

// readKubernetesEnv reads the app container environment of every Deployment
// and CronJob, values included, for the opt-in import (ADR-20261029093621
// §5). Like ReadComposeEnv it runs apart from Scan, so values never enter a
// Result, a plan or a log. Secret values are never read: a key a container
// takes from a Secret is listed in SecretRefs.
func readKubernetesEnv(fsys fs.FS) (ComposeEnv, error) {
	sources := k8sEnvSources{withValues: true}
	var workloads []k8sEnvWorkload
	err := k8sWalk(fsys, func(string) {}, func(o k8sObject) error {
		switch o.header.Kind {
		case "Deployment", "CronJob":
			w, err := k8sReadEnvWorkload(o)
			if err != nil {
				return fmt.Errorf("reposcan: %s %s %q: %w", o.src, o.header.Kind, o.header.Metadata.Name, err)
			}
			workloads = append(workloads, w)
		default:
			if err := sources.add(o.node, o.header); err != nil {
				return fmt.Errorf("reposcan: %s %s %q: %w", o.src, o.header.Kind, o.header.Metadata.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return ComposeEnv{}, err
	}
	hosts := k8sServiceTargets(sources.services, workloads)
	out := ComposeEnv{
		Services:       map[string]map[string]*string{},
		ImportServices: map[string]bool{},
		Kubernetes:     map[string]bool{},
		SecretRefs:     map[string][]string{},
	}
	for _, w := range workloads {
		entries, secretRefs := k8sContainerEnv(w.container, sources)
		for key, value := range entries {
			if value != nil {
				rewritten := k8sRewriteServiceHost(*value, w.namespace, hosts)
				entries[key] = &rewritten
			}
		}
		out.Services[w.name] = entries
		out.Kubernetes[w.name] = true
		if w.optIn {
			out.ImportServices[w.name] = true
		}
		if len(secretRefs) > 0 {
			out.SecretRefs[w.name] = secretRefs
		}
	}
	return out, nil
}

func k8sReadEnvWorkload(o k8sObject) (k8sEnvWorkload, error) {
	var obj map[string]any
	if err := o.node.Decode(&obj); err != nil {
		return k8sEnvWorkload{}, err
	}
	w := k8sEnvWorkload{name: o.header.Metadata.Name, kind: o.header.Kind, labels: map[string]string{}}
	w.namespace, _ = k8sMap(obj, "metadata")["namespace"].(string)
	w.optIn = k8sAnnotationTrue(k8sMap(obj, "metadata", "annotations")[K8sEnvImportAnnotation])
	templatePath := []string{"spec", "template"}
	if w.kind == "CronJob" {
		templatePath = []string{"spec", "jobTemplate", "spec", "template"}
	}
	template := k8sMap(obj, templatePath...)
	for key, raw := range k8sMap(template, "metadata", "labels") {
		w.labels[key] = fmt.Sprint(raw)
	}
	containers, _ := k8sMap(template, "spec")["containers"].([]any)
	names := make([]string, len(containers))
	for i, raw := range containers {
		container, _ := raw.(map[string]any)
		names[i], _ = container["name"].(string)
	}
	index, err := k8sAppContainerIndex(k8sMap(template, "metadata", "annotations"), names)
	if err != nil {
		return k8sEnvWorkload{}, err
	}
	if index < len(containers) {
		w.container, _ = containers[index].(map[string]any)
	}
	return w, nil
}

func k8sAnnotationTrue(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// k8sContainerEnv resolves a container's environment as Kubernetes does:
// envFrom sources in order, then env entries, which win. A value from a
// ConfigMap the scan did not read, or from the pod itself, is left out; a key
// from a Secret is returned in secretRefs, never with a value.
func k8sContainerEnv(container map[string]any, sources k8sEnvSources) (entries map[string]*string, secretRefs []string) {
	entries = map[string]*string{}
	secret := map[string]bool{}
	envFrom, _ := container["envFrom"].([]any)
	for _, raw := range envFrom {
		entry, _ := raw.(map[string]any)
		prefix, _ := entry["prefix"].(string)
		if ref, ok := entry["configMapRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			for key, value := range sources.configMaps[name] {
				value := value
				entries[prefix+key] = &value
				delete(secret, prefix+key)
			}
		}
		if ref, ok := entry["secretRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			for _, key := range sources.secrets[name] {
				delete(entries, prefix+key)
				secret[prefix+key] = true
			}
		}
	}
	env, _ := container["env"].([]any)
	for _, raw := range env {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		delete(entries, name)
		delete(secret, name)
		if value, ok := entry["value"]; ok && value != nil {
			text := fmt.Sprint(value)
			entries[name] = &text
			continue
		}
		from := k8sMap(entry, "valueFrom")
		if ref := k8sMap(from, "configMapKeyRef"); len(ref) > 0 {
			configMap, _ := ref["name"].(string)
			key, _ := ref["key"].(string)
			if value, ok := sources.configMaps[configMap][key]; ok {
				entries[name] = &value
			}
		}
		if _, ok := from["secretKeyRef"]; ok {
			secret[name] = true
		}
	}
	secretRefs = slices.Sorted(maps.Keys(secret))
	return entries, secretRefs
}

// k8sServiceTargets maps each Service, by "name" and "name.namespace", to the
// one workload its selector picks in its namespace. A Service that selects no
// workload, or more than one, maps to nothing.
func k8sServiceTargets(services []k8sService, workloads []k8sEnvWorkload) map[string]string {
	targets := map[string]string{}
	for _, svc := range services {
		if len(svc.selector) == 0 {
			continue
		}
		var picked []string
		for _, w := range workloads {
			if w.kind != "Deployment" || k8sNamespace(w.namespace) != k8sNamespace(svc.namespace) {
				continue
			}
			if k8sSelects(svc.selector, w.labels) {
				picked = append(picked, w.name)
			}
		}
		if len(picked) != 1 {
			continue
		}
		targets[strings.ToLower(svc.name)+"."+k8sNamespace(svc.namespace)] = picked[0]
	}
	return targets
}

func k8sNamespace(ns string) string {
	if ns == "" {
		return "default"
	}
	return strings.ToLower(ns)
}

func k8sSelects(selector, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

// k8sRewriteServiceHost replaces a cluster address of a scanned Service,
// `http://billing`, `billing.<ns>`, `billing.<ns>.svc` or
// `billing.<ns>.svc.cluster.local` with any port, by the name of the
// workload the Service selects. The import then rewrites an HTTP URL to that
// workload's Gregale address and skips any other form as a service address,
// as it does for a Compose service name (ADR-20261026181020). A short name
// resolves in the calling workload's namespace, as cluster DNS does.
func k8sRewriteServiceHost(value, namespace string, targets map[string]string) string {
	trimmed := strings.TrimSpace(value)
	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Hostname() == "" {
			return value
		}
		target, ok := k8sServiceTarget(parsed.Hostname(), namespace, targets)
		if !ok {
			return value
		}
		port := parsed.Port()
		parsed.Host = target
		if port != "" {
			parsed.Host = net.JoinHostPort(target, port)
		}
		return parsed.String()
	}
	host, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		host, port = trimmed, ""
	}
	target, ok := k8sServiceTarget(host, namespace, targets)
	if !ok {
		return value
	}
	if port != "" {
		return net.JoinHostPort(target, port)
	}
	return target
}

func k8sServiceTarget(host, namespace string, targets map[string]string) (string, bool) {
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
	ns := k8sNamespace(namespace)
	switch {
	case len(labels) == 1:
	case len(labels) == 2,
		len(labels) == 3 && labels[2] == "svc",
		len(labels) == 5 && labels[2] == "svc" && labels[3] == "cluster" && labels[4] == "local":
		ns = labels[1]
	default:
		return "", false
	}
	target, ok := targets[labels[0]+"."+ns]
	return target, ok
}
