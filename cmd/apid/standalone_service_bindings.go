package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func standaloneServiceBindings(raw *[]string, callerSlug string) ([]api.AppServiceBinding, *api.Problem) {
	if raw == nil {
		return nil, nil
	}
	targets, err := api.NormalizeServiceBindingTargets(*raw)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	for _, target := range targets {
		if target == callerSlug {
			return nil, api.ErrValidation("service_binding_targets cannot include the caller app itself")
		}
	}
	return api.ServiceBindingsForTargets(targets), nil
}

func serviceReliabilityForCreate(raw map[string]api.ServiceReliabilityPolicy, bindings []api.AppServiceBinding) (map[string]api.ServiceReliabilityPolicy, *api.Problem) {
	policies, err := api.NormalizeServiceReliabilityPolicies(raw, bindings)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	return policies, nil
}

func serviceReliabilityForUpdate(raw json.RawMessage, prior map[string]api.ServiceReliabilityPolicy, bindings []api.AppServiceBinding, bindingsChanged bool) (map[string]api.ServiceReliabilityPolicy, *api.Problem) {
	policies := prior
	if len(raw) > 0 {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			policies = nil
		} else {
			policies = make(map[string]api.ServiceReliabilityPolicy)
			if err := json.Unmarshal(raw, &policies); err != nil {
				return nil, api.ErrValidation("service_reliability must be an object or null")
			}
		}
	} else if bindingsChanged {
		// Removing a binding also removes its stored override. An explicit
		// replacement map is validated strictly instead of silently filtered.
		allowed := make(map[string]struct{}, len(bindings))
		for _, binding := range bindings {
			allowed[binding.Service] = struct{}{}
		}
		policies = make(map[string]api.ServiceReliabilityPolicy, len(prior))
		for target, policy := range prior {
			if _, ok := allowed[target]; ok {
				policies[target] = policy
			}
		}
	}
	return serviceReliabilityForCreate(policies, bindings)
}

func standaloneServicePolicy(raw *api.ServiceBindingPolicy) (api.ServiceBindingPolicy, *api.Problem) {
	if raw == nil {
		return "", nil
	}
	switch *raw {
	case api.ServiceBindingPolicyAccount, api.ServiceBindingPolicyDeclared:
		return *raw, nil
	default:
		return "", api.ErrValidation(fmt.Sprintf("service_binding_policy must be account or declared, got %q", *raw))
	}
}

func standaloneServiceTransport(raw *api.ServiceBindingTransport) (api.ServiceBindingTransport, *api.Problem) {
	if raw == nil {
		return "", nil
	}
	transport, err := api.NormalizeServiceBindingTransport(*raw)
	if err != nil || transport == "" {
		return "", api.ErrValidation(fmt.Sprintf("service_binding_transport must be http or https, got %q", *raw))
	}
	return transport, nil
}
