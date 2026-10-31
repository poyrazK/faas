package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/state"
)

// relayStore is what the relay reads to resolve a bridge session.
type relayStore interface {
	appaccess.Store
	DevBridgeByID(ctx context.Context, accountID, id string) (devbridge.Session, error)
	ProjectEnvironmentByID(ctx context.Context, id string) (state.ProjectEnvironment, error)
}

// devBridgeAction is what creating a bridge requires on its target app. The
// bridge routes have no reviewed organization permission, so apid falls back
// to the creator holding the owner/admin action (appaccess.authorize).
const devBridgeAction = authz.OrgActionManageAppSecurity

// relayLookup resolves a bridge session for the relay. Every lookup repeats
// what apid checked when it issued the session: the owner account is active,
// the environment is an unprotected non-production one, and the target app is
// active in it. It also repeats the organization check. A bridge into a
// company app routes the company's environment traffic to the creator's
// laptop and lets it call the company's dependencies, so the bridge ends as
// soon as the creator leaves the company or loses the permission, not an
// hour later when the session expires. Each request, and each second of an
// open socket, comes back here.
func relayLookup(store relayStore) devbridge.LookupSession {
	return func(ctx context.Context, account, id string) (devbridge.Session, error) {
		owner, err := store.AccountByID(ctx, account)
		if err != nil || owner.Status != state.AccountActive || owner.AbuseHoldAt != nil {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		session, err := store.DevBridgeByID(ctx, account, id)
		if err != nil {
			return session, err
		}
		env, err := store.ProjectEnvironmentByID(ctx, session.Scope.EnvironmentID)
		if err != nil || env.AccountID != account || env.ProjectID != session.Scope.ProjectID || env.Protected || env.Slug == "production" || env.Slug == "default" {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		app, err := store.AppByID(ctx, session.Scope.TargetAppID)
		if err != nil || app.AccountID != account || app.ProjectID != env.ProjectID || app.Status != state.AppActive {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		if standing, err := appaccess.Standing(ctx, store, account, app, devBridgeAction); err != nil || !standing {
			return devbridge.Session{}, devbridge.ErrUnauthorized
		}
		return session, nil
	}
}
