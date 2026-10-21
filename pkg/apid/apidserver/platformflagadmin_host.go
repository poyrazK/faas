package apidserver

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/platformflagadmin"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// platformFlagAdminHost adapts *server to platformflagadmin.Host (ADR-1700),
// reading the server's fields at call time.
type platformFlagAdminHost struct{ s *server }

var _ platformflagadmin.Host = platformFlagAdminHost{}

func (h platformFlagAdminHost) Store() state.Store                { return h.s.store }
func (h platformFlagAdminHost) PlatformFlags() *platformflags.Set { return h.s.platformFlags() }
func (h platformFlagAdminHost) Now() time.Time                    { return timeNow() }
func (h platformFlagAdminHost) AdminAllows(acct state.Account) (bool, *api.Problem) {
	return h.s.adminAllows(acct)
}
