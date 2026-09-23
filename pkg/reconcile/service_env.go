package reconcile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

const (
	serviceEnvPrefix = "GREGALE_SERVICE_"
	serviceEnvSuffix = "_URL"
	serviceEnvPort   = 10080
)

func serviceEnvForWorkloadWithAvailable(base map[string]string, w reposcan.Workload, available map[string]struct{}) map[string]string {
	env := make(map[string]string, len(base)+len(w.DependsOn))
	for key, value := range base {
		if strings.HasPrefix(key, serviceEnvPrefix) && strings.HasSuffix(key, serviceEnvSuffix) {
			continue
		}
		env[key] = value
	}
	for _, binding := range serviceBindingsForWorkloadWithAvailable(w, available) {
		env[binding.Binding] = fmt.Sprintf("http://%s.svc.gregale:%d", binding.Service, serviceEnvPort)
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func serviceBindingsForWorkloadWithAvailable(w reposcan.Workload, available map[string]struct{}) []api.AppServiceBinding {
	bindings := make([]api.AppServiceBinding, 0, len(w.DependsOn))
	seen := make(map[string]struct{}, len(w.DependsOn))
	for _, dependency := range w.DependsOn {
		service := strings.TrimSpace(dependency)
		if service == "" {
			continue
		}
		key := strings.ToLower(service)
		if available != nil {
			if _, ok := available[key]; !ok {
				continue
			}
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		bindings = append(bindings, api.AppServiceBinding{
			Binding: serviceEnvKey(key),
			Service: key,
		})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Service != bindings[j].Service {
			return bindings[i].Service < bindings[j].Service
		}
		return bindings[i].Binding < bindings[j].Binding
	})
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

func serviceBindingsEqual(left, right []api.AppServiceBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func serviceBindingPolicyForWorkload(w reposcan.Workload) api.ServiceBindingPolicy {
	return api.ServiceBindingPolicy(w.ServiceBindingPolicy).Effective()
}

func previewServiceCallsPolicyForWorkload(w reposcan.Workload) api.PreviewServiceCallsPolicy {
	return api.PreviewServiceCallsPolicy(w.PreviewServiceCallsPolicy).Effective()
}

func serviceEnvKey(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	b.Grow(len(serviceEnvPrefix) + len(name) + len(serviceEnvSuffix))
	b.WriteString(serviceEnvPrefix)
	for _, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString(serviceEnvSuffix)
	return b.String()
}

func serviceEnvEqual(actual, expected map[string]string) bool {
	for key, value := range actual {
		if strings.HasPrefix(key, serviceEnvPrefix) && strings.HasSuffix(key, serviceEnvSuffix) {
			if expected[key] != value {
				return false
			}
		}
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}
