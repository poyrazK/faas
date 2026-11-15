package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stampTargetIdentity clears customer-supplied platform claims and stamps the
// scheduler-selected identity of tgt on r, in its headers and its context.
// The vmmd forwarder takes the instance it forwards to from these headers
// (forwardproxy.go), so a retry that replays the request onto a sibling must
// restamp for that sibling; replaying the first target's identity sent the
// retry back to the failed instance.
func stampTargetIdentity(r *http.Request, app App, tgt Target) (*http.Request, api.PlatformIdentity) {
	identity := platformIdentityForTarget(r.Context(), r, app, tgt)
	identity.ApplyGuestHeaders(r.Header)
	// Keep the same scheduler-authored identity on the request context so
	// request-scoped logs and any subsequent schedd/vmmd metadata hop cannot
	// drift from the headers sent to the guest.
	return r.WithContext(wire.WithPlatformIdentity(r.Context(), identity)), identity
}
