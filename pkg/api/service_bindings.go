package api

import (
	"fmt"
	"sort"
	"strings"
)

// ServiceBindingPolicy controls which same-account internal services an app
// may call. The empty persisted value is the backwards-compatible account
// policy.
type ServiceBindingPolicy string

const (
	// ServiceBindingPolicyAccount preserves the original service-mesh
	// contract: any app may call another app owned by the same account.
	ServiceBindingPolicyAccount ServiceBindingPolicy = "account"
	// ServiceBindingPolicyDeclared limits the caller to targets present in its
	// repository-declared service binding inventory.
	ServiceBindingPolicyDeclared ServiceBindingPolicy = "declared"
)

// Effective returns the policy used at runtime. Unknown non-empty values fail
// closed to declared so an older gateway cannot accidentally widen a policy
// written by a newer control plane.
func (p ServiceBindingPolicy) Effective() ServiceBindingPolicy {
	switch p {
	case "", ServiceBindingPolicyAccount:
		return ServiceBindingPolicyAccount
	case ServiceBindingPolicyDeclared:
		return ServiceBindingPolicyDeclared
	default:
		return ServiceBindingPolicyDeclared
	}
}

// AppServiceBinding is one repository-declared dependency from the returned
// app to another app in the same account. Binding is the platform-owned
// environment key injected into the caller; Service is the target app's
// stable internal name.
//
// The account policy uses this as discovery metadata only. The opt-in declared
// policy also uses it as the caller's outbound service authorization list.
type AppServiceBinding struct {
	Binding string `json:"binding"`
	Service string `json:"service"`
}

// NormalizeAllowedServiceCallers validates and canonicalizes a target policy.
// It returns a non-nil empty slice for an explicit deny-all list. Callers are
// logical app names rather than generated preview slugs.
func NormalizeAllowedServiceCallers(raw []string) ([]string, error) {
	if len(raw) > AllowedServiceCallersMax {
		return nil, fmt.Errorf("allowed_service_callers exceeds %d names", AllowedServiceCallersMax)
	}
	seen := make(map[string]struct{}, len(raw))
	callers := make([]string, 0, len(raw))
	for _, value := range raw {
		name := strings.ToLower(strings.TrimSpace(value))
		if !validServiceCallerName(name) {
			return nil, fmt.Errorf("allowed_service_callers contains invalid app name %q", value)
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			callers = append(callers, name)
		}
	}
	sort.Strings(callers)
	return callers, nil
}

func validServiceCallerName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
