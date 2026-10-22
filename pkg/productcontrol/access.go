package productcontrol

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

var accessRole = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func ValidAccessRole(role string) bool {
	return len(role) <= api.ProductControlAccessRoleMaxBytes && accessRole.MatchString(role)
}

func ValidateAccess(policy *api.ProductAccessPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.Roles == nil || len(policy.Roles) > api.ProductControlAccessMaxRoles {
		return fmt.Errorf("access.roles must be an object within the role limit")
	}
	for role, permissions := range policy.Roles {
		if !ValidAccessRole(role) || permissions == nil || len(permissions) > api.ProductControlAccessMaxPermissions {
			return fmt.Errorf("invalid product access role %q", role)
		}
		seen := map[api.ProductPermission]bool{}
		for _, permission := range permissions {
			if seen[permission] || !slices.Contains([]api.ProductPermission{api.ProductPermissionViewWork, api.ProductPermissionRunWork, api.ProductPermissionViewBilling, api.ProductPermissionManagePlan, api.ProductPermissionManageTeam, api.ProductPermissionManageBilling}, permission) {
				return fmt.Errorf("invalid or duplicate product permission for role %q", role)
			}
			seen[permission] = true
		}
	}
	return nil
}

// DefaultAccess returns a private policy. Unknown roles have no product access.
// Nil catalog access uses this default, including previously published catalogs.
func DefaultAccess() api.ProductAccessPolicy {
	return api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{
		"owner":  {api.ProductPermissionViewWork, api.ProductPermissionRunWork, api.ProductPermissionViewBilling, api.ProductPermissionManageBilling},
		"admin":  {api.ProductPermissionViewWork, api.ProductPermissionRunWork, api.ProductPermissionViewBilling},
		"member": {api.ProductPermissionViewWork},
	}}
}

func EffectiveAccess(policy *api.ProductAccessPolicy, role string) api.ProductPermissions {
	var out api.ProductPermissions
	if !ValidAccessRole(role) || ValidateAccess(policy) != nil {
		return out
	}
	if policy == nil {
		defaults := DefaultAccess()
		policy = &defaults
	}
	for _, permission := range policy.Roles[role] {
		switch permission {
		case api.ProductPermissionViewWork:
			out.ViewWork = true
		case api.ProductPermissionRunWork:
			out.ViewWork, out.RunWork = true, true
		case api.ProductPermissionViewBilling:
			out.ViewBilling = true
		case api.ProductPermissionManagePlan:
			out.ManagePlan = true
		case api.ProductPermissionManageTeam:
			out.ManageTeam = true
		case api.ProductPermissionManageBilling:
			out.ManageBilling = true
		}
	}
	return out
}

func AccessConfiguration(catalog api.ProductCatalog) api.ProductAccessConfiguration {
	policy := catalog.Spec.Access
	if policy == nil {
		defaults := DefaultAccess()
		policy = &defaults
	}
	copy := api.ProductAccessPolicy{Roles: make(map[string][]api.ProductPermission, len(policy.Roles))}
	for role, permissions := range policy.Roles {
		copy.Roles[role] = append([]api.ProductPermission{}, permissions...)
	}
	return api.ProductAccessConfiguration{Revision: catalog.Revision, AuthApp: catalog.Spec.AuthApp, Default: catalog.Spec.Access == nil, Policy: copy}
}

func PreviewAccess(catalog api.ProductCatalog, policy api.ProductAccessPolicy) (api.ProductAccessChangePreview, error) {
	before := AccessConfiguration(catalog)
	spec := catalog.Spec
	spec.Access = &policy
	after, err := Compile(spec)
	if err != nil {
		return api.ProductAccessChangePreview{}, err
	}
	return api.ProductAccessChangePreview{Before: before, After: AccessConfiguration(after), Change: api.ProductAccessChangeRequest{ExpectedHead: catalog.Revision, ExpectedRevision: after.Revision, Policy: *after.Spec.Access}}, nil
}
