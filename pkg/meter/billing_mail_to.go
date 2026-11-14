package meter

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// billingMailTo returns the address billing mail for a subject goes to. An
// account, or an organization with a billing email, uses its own. An
// organization that never checked out has no billing email (it is set at
// its first checkout), so its mail goes to its first active owner instead
// (billing review R10-3); empty when there is none or the lookup fails.
func billingMailTo(ctx context.Context, store state.Store, subject state.Account) string {
	if subject.Email != "" || subject.ResourceOrgID == "" || store == nil {
		return subject.Email
	}
	members, err := store.ListOrgMembers(ctx, subject.ResourceOrgID)
	if err != nil {
		return ""
	}
	for _, m := range members {
		if m.Role != state.OrgRoleOwner || m.RemovedAt != nil {
			continue
		}
		owner, err := store.AccountByID(ctx, m.AccountID)
		if err == nil && owner.Email != "" {
			return owner.Email
		}
	}
	return ""
}
