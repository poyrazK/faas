package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	accesssql "github.com/onebox-faas/faas/pkg/state/orgaccess/sqlc"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// lockIssueResponseActor takes the membership lock before the app lock. A
// concurrent removal or demotion therefore either wins before authorization or
// waits until the response transaction commits. The role set mirrors the
// dashboard's OrgActionDeployApp permission for response mutations.
func lockIssueResponseActor(ctx context.Context, tx pgx.Tx, app, owner, actor string) (bool, error) {
	q := sqlc.New()
	appID, actorID := issueUUID(app), issueUUID(actor)
	before, err := q.IssuePolicyApp(ctx, tx, appID)
	if err != nil {
		return false, mapErr(err)
	}
	if issueID(before.AccountID) != owner || !actorID.Valid {
		return false, ErrNotFound
	}
	manager := false
	if !before.OrgID.Valid {
		if actorID != before.AccountID {
			return false, ErrNotFound
		}
		manager = true
	} else {
		org, err := q.IssuePolicyOrg(ctx, tx, before.OrgID)
		if err != nil {
			return false, mapErr(err)
		}
		if org.PersonalOrg {
			if org.PersonalOwnerAccountID != before.AccountID || actorID != before.AccountID {
				return false, ErrNotFound
			}
			manager = true
		} else {
			member, err := accesssql.New().LockMembership(ctx, tx, accesssql.LockMembershipParams{OrgID: before.OrgID, AccountID: actorID})
			if errors.Is(err, pgx.ErrNoRows) {
				return false, ErrNotFound
			}
			if err != nil {
				return false, err
			}
			switch OrgRole(member.Role) {
			case OrgRoleOwner, OrgRoleAdmin:
				manager = true
			case OrgRoleDeveloper:
			default:
				return false, ErrNotFound
			}
		}
	}
	locked, err := q.IssueLockApp(ctx, tx, appID)
	if err != nil {
		return false, mapErr(err)
	}
	if locked.AccountID != before.AccountID || locked.OrgID != before.OrgID {
		return false, ErrIssuePolicyChanged
	}
	return manager, nil
}
