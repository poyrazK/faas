package reconcile

import (
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

const (
	serviceEnvPrefix = api.ServiceBindingEnvPrefix
	serviceEnvSuffix = api.ServiceBindingEnvSuffix
)

func serviceEnvForWorkloadWithAvailable(base map[string]string, w reposcan.Workload, available map[string]struct{}) map[string]string {
	return serviceEnvForWorkloadWithTransport(base, w, available, api.ServiceBindingTransport(w.ServiceBindingTransport))
}

func serviceEnvForWorkloadWithTransport(base map[string]string, w reposcan.Workload, available map[string]struct{}, transport api.ServiceBindingTransport) map[string]string {
	return api.ServiceBindingEnvForTransport(base, serviceBindingsForWorkloadWithAvailable(w, available), transport)
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
			Binding: api.ServiceBindingEnvKey(key),
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

func serviceBindingPolicyForNewWorkload(w reposcan.Workload) api.ServiceBindingPolicy {
	if w.ServiceBindingPolicy == "" {
		return api.ServiceBindingPolicyDeclared
	}
	return api.ServiceBindingPolicy(w.ServiceBindingPolicy).Effective()
}

func serviceBindingPolicyForExistingWorkload(w reposcan.Workload, existing api.ServiceBindingPolicy) api.ServiceBindingPolicy {
	if w.ServiceBindingPolicy == "" {
		return existing.Effective()
	}
	return api.ServiceBindingPolicy(w.ServiceBindingPolicy).Effective()
}

func serviceBindingTransportForNewWorkload(w reposcan.Workload) api.ServiceBindingTransport {
	if w.ServiceBindingTransport == "" {
		return ""
	}
	return api.ServiceBindingTransport(w.ServiceBindingTransport).Effective()
}

func serviceBindingTransportForExistingWorkload(w reposcan.Workload, existing api.ServiceBindingTransport) api.ServiceBindingTransport {
	if w.ServiceBindingTransport == "" {
		return existing
	}
	return api.ServiceBindingTransport(w.ServiceBindingTransport).Effective()
}

func allowedServiceCallersEqual(left, right *[]string) bool {
	if (left == nil) != (right == nil) {
		return false
	}
	if left == nil {
		return true
	}
	if len(*left) != len(*right) {
		return false
	}
	for i := range *left {
		if (*left)[i] != (*right)[i] {
			return false
		}
	}
	return true
}

func allowedServiceCallScopesEqual(left, right *api.ServiceCallerScopes) bool {
	if (left == nil) != (right == nil) {
		return false
	}
	if left == nil || len(*left) != len(*right) {
		return left == nil && right == nil
	}
	for caller, leftScope := range *left {
		rightScope, ok := (*right)[caller]
		if !ok || !stringListsEqual(leftScope.Methods, rightScope.Methods) ||
			!stringListsEqual(leftScope.PathPrefixes, rightScope.PathPrefixes) {
			return false
		}
	}
	return true
}

func stringListsEqual(left, right []string) bool {
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

func previewServiceCallsPolicyForWorkload(w reposcan.Workload) api.PreviewServiceCallsPolicy {
	return api.PreviewServiceCallsPolicy(w.PreviewServiceCallsPolicy).Effective()
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
