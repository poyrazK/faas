package reconcile

import "github.com/onebox-faas/faas/pkg/api"

// ServiceAccessAuth returns the require_authn and public_auth_mode an app
// gets for a declared service access (ADR-20261124090000), starting from its
// current ones.
//
//   - "public": no Gregale API key. require_authn goes off and a bearer gate
//     opens; basic auth, an IP allowlist and internal-only are owner
//     choices the declaration does not undo.
//   - "key": a Gregale API key of the owning account, where the plan allows
//     the gate (Free does not); the public auth mode is left alone.
//   - "": nothing declared; the settings are returned unchanged.
func ServiceAccessAuth(access string, plan api.Plan, requireAuthn bool, mode string) (bool, string) {
	switch access {
	case api.ServiceAccessPublic:
		if mode == api.AppPublicAuthModeBearer {
			mode = api.AppPublicAuthModeOpen
		}
		return false, mode
	case api.ServiceAccessKey:
		return ServiceAccessKeyAvailable(plan), mode
	}
	return requireAuthn, mode
}

// ServiceAccessRemovedAuth returns an app's settings once its service no
// longer declares access: the plan defaults a new app gets, so deleting
// `access: public` closes the URL again. A mode the owner set to basic
// auth stays.
func ServiceAccessRemovedAuth(plan api.Plan, mode string) (bool, string) {
	if mode == "" || mode == api.AppPublicAuthModeOpen {
		mode = plan.PublicAuthModeDefault()
	}
	return plan.RequireAuthnDefault(), mode
}

// AccessChange is the access a declaration leads to for one app.
type AccessChange struct {
	RequireAuthn   bool
	PublicAuthMode string
	Declared       string
	changed        bool
}

// Changed reports whether the app's access or its recorded declaration moves.
func (c AccessChange) Changed() bool { return c.changed }

// DeclaredAccessChange is the access an app ends with when its source
// declares declared and previously declared previous (ADR-20261124090000):
// a declaration applies, a removed one returns the app to the plan default,
// and an app that never declared access keeps what it has.
func DeclaredAccessChange(declared, previous string, plan api.Plan, requireAuthn bool, mode string) AccessChange {
	wantRequire, wantMode := requireAuthn, mode
	switch {
	case declared != "":
		wantRequire, wantMode = ServiceAccessAuth(declared, plan, requireAuthn, mode)
	case previous != "":
		wantRequire, wantMode = ServiceAccessRemovedAuth(plan, mode)
	}
	return AccessChange{
		RequireAuthn: wantRequire, PublicAuthMode: wantMode, Declared: declared,
		changed: wantRequire != requireAuthn || wantMode != mode || declared != previous,
	}
}

// ServiceAccessKeyAvailable reports whether the plan has the API key gate:
// on by default (Hobby) or allowed to be turned on (Pro, Scale). Free has
// neither.
func ServiceAccessKeyAvailable(plan api.Plan) bool {
	return plan.RequireAuthnDefault() || plan.RequireAuthnAllowed()
}
