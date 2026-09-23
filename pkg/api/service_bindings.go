package api

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
