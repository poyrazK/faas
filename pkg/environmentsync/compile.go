package environmentsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var immutableImageRE = regexp.MustCompile(`^[^\s]+@sha256:[a-f0-9]{64}$`)

// Compile makes a detached, canonical definition and emits its managed field
// set. It preserves omission versus explicit empty collections and never
// invents ownership for detected defaults or platform-generated metadata.
func Compile(input api.EnvironmentDefinition) (DesiredState, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return DesiredState{}, fmt.Errorf("encode environment definition: %w", err)
	}
	if len(encoded) > api.EnvironmentGitOpsMaxDefinitionBytes {
		return DesiredState{}, fmt.Errorf("environment definition exceeds %d bytes", api.EnvironmentGitOpsMaxDefinitionBytes)
	}
	var d api.EnvironmentDefinition
	if err := json.Unmarshal(encoded, &d); err != nil {
		return DesiredState{}, err
	}
	if d.APIVersion != APIVersion || !api.ValidProjectSlug(d.Project) || !api.ValidProjectEnvironmentSlug(d.Environment) {
		return DesiredState{}, fmt.Errorf("definition needs api_version %q and valid project/environment slugs", APIVersion)
	}
	if d.Workloads == nil {
		return DesiredState{}, fmt.Errorf("environment definition must explicitly declare its workloads map")
	}
	var fields []Field
	if d.Configuration != nil {
		raw, _ := json.Marshal(d.Configuration)
		canonical, _, err := api.NormalizeProjectEnvironmentConfig(raw)
		if err != nil {
			return DesiredState{}, err
		}
		_ = json.Unmarshal(canonical, &d.Configuration)
		for key, value := range d.Configuration {
			fields = append(fields, Field{Resource: "environment", Path: "configuration/" + key, Value: value})
		}
	}
	appNames := make(map[string]bool)
	for _, name := range sortedKeys(d.Workloads) {
		w := d.Workloads[name]
		if !api.ValidAppSlug(name) || w.App != "" && !api.ValidAppSlug(w.App) {
			return DesiredState{}, fmt.Errorf("invalid workload identity %q", name)
		}
		if w.App != "" {
			if appNames[w.App] {
				return DesiredState{}, fmt.Errorf("an app cannot map to two logical workloads")
			}
			appNames[w.App] = true
		}
		resource := "workload/" + name
		workloadFields, err := compileWorkload(resource, name, &w, d.Workloads)
		if err != nil {
			return DesiredState{}, fmt.Errorf("workload %q: %w", name, err)
		}
		d.Workloads[name] = w
		fields = append(fields, workloadFields...)
	}
	if err := validateDependencies(d.Workloads); err != nil {
		return DesiredState{}, err
	}
	sortFields(fields)
	canonical, err := json.Marshal(d)
	if err != nil {
		return DesiredState{}, err
	}
	return DesiredState{Definition: d, Digest: digest(canonical), Fields: fields}, nil
}

func compileWorkload(resource, name string, w *api.EnvironmentWorkload, workloads map[string]api.EnvironmentWorkload) ([]Field, error) {
	var fields []Field
	add := func(field string, value any) {
		raw, _ := json.Marshal(value) // validated API types contain only JSON-safe values
		fields = append(fields, Field{Resource: resource, Path: field, Value: raw})
	}
	// Workload membership is intent even when no other settings are managed.
	// Actual app IDs belong to the separately persisted identity mapping.
	add("presence", true)
	if w.Source != nil {
		if err := normalizeSource(w.Source); err != nil {
			return nil, err
		}
		add("source", w.Source)
	}
	if len(w.Runtime) != 0 {
		runtimeFields, err := compileRuntime(w.Runtime)
		if err != nil {
			return nil, err
		}
		w.Runtime, _ = json.Marshal(runtimeFields)
		for key, value := range runtimeFields {
			fields = append(fields, Field{Resource: resource, Path: "runtime/" + key, Value: value})
		}
	}
	for key, value := range w.Variables {
		if (api.PutAppEnvRequest{Value: value}).Validate(api.MustLimitsFor(api.PlanScale).EnvValueMaxBytes) != nil {
			return nil, fmt.Errorf("variable %q exceeds the platform value limit", key)
		}
		if problem := api.ValidateEnvKey(key); problem != nil {
			return nil, fmt.Errorf("invalid variable key %q", key)
		}
		// Match the existing non-secret configuration key contract. Values
		// are never included in validation errors.
		raw, _ := json.Marshal(map[string]string{key: value})
		if _, _, err := api.NormalizeProjectEnvironmentConfig(raw); err != nil {
			return nil, fmt.Errorf("variable %q must be supplied through secret_refs", key)
		}
		if _, exists := w.SecretRefs[key]; exists {
			return nil, fmt.Errorf("variable %q also appears in secret_refs", key)
		}
		add("variables/"+key, value)
	}
	for key, ref := range w.SecretRefs {
		if api.ValidateEnvKey(key) != nil || !strings.HasPrefix(ref, "secret:") || api.ValidateEnvKey(strings.TrimPrefix(ref, "secret:")) != nil {
			return nil, fmt.Errorf("secret_refs %q requires a valid secret:NAME reference", key)
		}
		add("secret_refs/"+key, ref)
	}
	if w.Routes != nil {
		if err := normalizeRoutes(w.Routes); err != nil {
			return nil, err
		}
		add("routes", w.Routes)
	}
	if w.Policies != nil {
		if err := normalizePolicies(*w.Policies); err != nil {
			return nil, err
		}
		add("policies", w.Policies)
	}
	for key, binding := range w.QueueBindings {
		if !api.ValidAppSlug(key) || !api.ValidAppSlug(binding.QueueName) {
			return nil, fmt.Errorf("queue binding %q needs valid binding and queue names", key)
		}
		if binding.Mode == "" {
			binding.Mode = "pull"
		}
		if binding.Mode != "pull" && binding.Mode != "push" || binding.WorkloadClass != "worker" && binding.WorkloadClass != "job" {
			return nil, fmt.Errorf("queue binding %q needs pull/push mode and worker/job workload_class", key)
		}
		if binding.MaxConcurrency == 0 {
			binding.MaxConcurrency = 1
		}
		if binding.MaxConcurrency < 1 || binding.MaxConcurrency > api.QueueBindingMaxConcurrency {
			return nil, fmt.Errorf("queue binding %q max_concurrency must be between 1 and %d", key, api.QueueBindingMaxConcurrency)
		}
		if binding.RetryPolicy != nil {
			if binding.RetryPolicy.Validate() != nil || binding.RetryPolicy.BaseSeconds > api.QueueBindingRetryMaxBaseSeconds || binding.RetryPolicy.MaxSeconds > api.QueueBindingRetryMaxSeconds {
				return nil, fmt.Errorf("queue binding %q has an invalid retry policy", key)
			}
			if *binding.RetryPolicy == (api.RetryPolicyDTO{}) {
				binding.RetryPolicy = nil
			}
		}
		if binding.Enabled == nil {
			enabled := true
			binding.Enabled = &enabled
		}
		w.QueueBindings[key] = binding
		add("queue_bindings/"+key, binding)
	}
	bindingEnvKeys := make(map[string]bool)
	for key, binding := range w.ServiceBindings {
		if !api.ValidAppSlug(key) || api.ValidateEnvKey(binding.EnvKey) != nil || binding.Workload == name {
			return nil, fmt.Errorf("invalid service binding %q", key)
		}
		if bindingEnvKeys[binding.EnvKey] {
			return nil, fmt.Errorf("service bindings overlap environment key %q", binding.EnvKey)
		}
		bindingEnvKeys[binding.EnvKey] = true
		if _, exists := workloads[binding.Workload]; !exists {
			return nil, fmt.Errorf("service binding %q references an undeclared workload", key)
		}
		if _, exists := w.Variables[binding.EnvKey]; exists {
			return nil, fmt.Errorf("service binding %q overlaps a variable", key)
		}
		if _, exists := w.SecretRefs[binding.EnvKey]; exists {
			return nil, fmt.Errorf("service binding %q overlaps a secret reference", key)
		}
		add("service_bindings/"+key, binding)
	}
	return fields, nil
}

func normalizeSource(source *api.EnvironmentWorkloadSource) error {
	if source.Kind == "" {
		source.Kind = "source"
	}
	switch source.Kind {
	case "source", "dockerfile":
		if source.Directory == "" {
			source.Directory = "."
		}
		if !validRelativePath(source.Directory) || source.Image != "" {
			return fmt.Errorf("source directory must stay within the Git tree and cannot include an image")
		}
		source.Directory = path.Clean(source.Directory)
		if source.Kind == "dockerfile" && source.Dockerfile == "" {
			source.Dockerfile = "Dockerfile"
		}
		if source.Dockerfile != "" && !validRelativePath(source.Dockerfile) {
			return fmt.Errorf("dockerfile must stay within the source directory")
		}
		if source.Dockerfile != "" {
			source.Dockerfile = path.Clean(source.Dockerfile)
		}
	case "image":
		if !immutableImageRE.MatchString(source.Image) || source.Directory != "" || source.Dockerfile != "" {
			return fmt.Errorf("image source requires an immutable @sha256 digest and no directory/dockerfile")
		}
	default:
		return fmt.Errorf("unsupported source kind %q", source.Kind)
	}
	return nil
}

func validRelativePath(value string) bool {
	clean := path.Clean(value)
	return value != "" && !strings.ContainsAny(value, "\\\x00") && !path.IsAbs(value) && clean != ".." && !strings.HasPrefix(clean, "../")
}

func compileRuntime(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var manifest api.AppManifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return nil, fmt.Errorf("invalid runtime contract: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("runtime must be an object")
	}
	for _, key := range []string{"env", "env_secrets"} {
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("runtime.%s must use variables or secret_refs", key)
		}
	}
	if err := manifest.ValidateLifecyclePlan(api.PlanScale); err != nil {
		return nil, fmt.Errorf("invalid runtime contract: %w", err)
	}
	typed, _ := json.Marshal(manifest)
	var normalized map[string]json.RawMessage
	_ = json.Unmarshal(typed, &normalized)
	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("runtime.%s cannot be null; omit it to leave it unmanaged", key)
		}
		if typedValue, exists := normalized[key]; exists {
			value = typedValue
		}
		canonical, err := canonicalJSON(value)
		if err != nil {
			return nil, err
		}
		fields[key] = canonical
	}
	return fields, nil
}

func normalizeRoutes(contract *api.EnvironmentRouteContract) error {
	if len(contract.DeclaredRoutes) > api.EnvironmentGitOpsMaxDeclaredRoutes {
		return fmt.Errorf("too many declared routes")
	}
	if contract.DeclaredRoutes == nil {
		return fmt.Errorf("routes requires declared_routes; use [] for an explicit empty collection")
	}
	if contract.OnlyAllowDeclaredRoutes && len(contract.DeclaredRoutes) == 0 {
		return fmt.Errorf("route enforcement requires at least one declared route")
	}
	seen := make(map[string]bool)
	for i, route := range contract.DeclaredRoutes {
		if !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#\x00\r\n") || len(route.Methods) == 0 {
			return fmt.Errorf("invalid declared route")
		}
		methods, err := normalizeMethods(route.Methods)
		if err != nil {
			return err
		}
		route.Methods = methods
		for _, method := range methods {
			key := route.Path + " " + method
			if seen[key] {
				return fmt.Errorf("duplicate declared route %q", key)
			}
			seen[key] = true
		}
		contract.DeclaredRoutes[i] = route
	}
	slices.SortFunc(contract.DeclaredRoutes, func(a, b api.DeclaredRoute) int {
		if cmp := strings.Compare(a.Path, b.Path); cmp != 0 {
			return cmp
		}
		return strings.Compare(strings.Join(a.Methods, ","), strings.Join(b.Methods, ","))
	})
	return nil
}

func normalizeMethods(input []string) ([]string, error) {
	methods := make([]string, 0, len(input))
	for _, method := range input {
		method = strings.ToUpper(strings.TrimSpace(method))
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		default:
			return nil, fmt.Errorf("unsupported HTTP method %q", method)
		}
		methods = append(methods, method)
	}
	slices.Sort(methods)
	return slices.Compact(methods), nil
}

func validateDependencies(workloads map[string]api.EnvironmentWorkload) error {
	visiting, visited := make(map[string]bool), make(map[string]bool)
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("service binding dependency cycle at workload %q", name)
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, key := range sortedKeys(workloads[name].ServiceBindings) {
			if err := visit(workloads[name].ServiceBindings[key].Workload); err != nil {
				return err
			}
		}
		visiting[name], visited[name] = false, true
		return nil
	}
	for _, name := range sortedKeys(workloads) {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortFields(fields []Field) {
	slices.SortFunc(fields, func(a, b Field) int { return strings.Compare(a.Key(), b.Key()) })
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected one JSON value")
	}
	return json.Marshal(value)
}

func decodeStrict(raw []byte, value any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
