package main

import (
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// The gateway's production authenticator is a narrow adapter; tests may use
// the full Store directly. Both must resolve the same organization boundary.
func wrapControlPlaneAppAccess(auth *authmw.Middleware, next authmw.AccountHandler) authmw.AccountHandler {
	var store appaccess.Store
	if adapter, ok := auth.Authn.(authAdapter); ok {
		store = adapter.store
	} else if source, ok := auth.Authn.(appaccess.Store); ok {
		store = source
	}
	return appaccess.Wrap(store, auth.Audit, next)
}
