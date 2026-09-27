package main

import (
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
