package appaccess

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// Standing reports whether actorID still holds action on app, for a
// credential that apid issued through authorize and that is used later
// without a request apid sees: the dev bridge relay checks it on every
// request and every second on an open socket. It applies authorize's
// organization rules, so a credential stops working once its holder leaves
// the company, loses the role, or the organization stops being active, rather
// than when it expires. Only the creator of an unattributed or personal app
// stands on it. A missing membership reads as false; other read failures are
// errors.
func Standing(ctx context.Context, store Store, actorID string, app state.App, action authz.OrgAction) (bool, error) {
	if app.OrgID == "" {
		return app.AccountID == actorID, nil
	}
	org, err := store.OrgByID(ctx, app.OrgID)
	if err != nil {
		return false, err
	}
	if org.Personal {
		return app.AccountID == actorID && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == actorID, nil
	}
	if org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
		return false, nil
	}
	if org.Status == state.OrgStatusSuspended &&
		action != authz.OrgActionReadApp && action != authz.OrgActionReadAppLogs {
		return false, nil
	}
	member, err := store.OrgMemberByAccount(ctx, org.ID, actorID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if member.RemovedAt != nil {
		return false, nil
	}
	snapshot, err := store.OrgAccessSnapshot(ctx, org.ID, actorID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return authz.Can(snapshot.Role, action), nil
}
