// adr: 20261104170000
package ingressroute

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type rawOwnerReader struct {
	account state.Account
	org     state.Org
	err     error
}

func (r rawOwnerReader) AccountByID(context.Context, string) (state.Account, error) {
	return r.account, r.err
}
func (r rawOwnerReader) OrgByID(context.Context, string) (state.Org, error) { return r.org, r.err }

func TestRawIngressOwnerReadFailsClosed(t *testing.T) {
	failure := errors.New("owner lookup unavailable")
	if err := RequireActiveOwner(t.Context(), rawOwnerReader{err: failure}, state.App{AccountID: "account"}); !errors.Is(err, failure) {
		t.Fatalf("lookup failure=%v", err)
	}
	if err := RequireActiveOwner(t.Context(), rawOwnerReader{err: failure}, state.App{AccountID: "account", OrgID: "company"}); !errors.Is(err, failure) {
		t.Fatalf("organization lookup failure=%v", err)
	}
}

func TestRawIngressUsesCompanyRuntimeEntitlement(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, status := range []state.OrgStatus{state.OrgStatusActive, state.OrgStatusPastDue, state.OrgStatusSuspended} {
			t.Run(string(status)+"/held="+map[bool]string{false: "false", true: "true"}[held], func(t *testing.T) {
				reader := rawOwnerReader{account: state.Account{Status: state.AccountSuspended}, org: state.Org{ID: "company", ResourceOwnershipEnabled: true, Status: status}}
				if held {
					now := time.Now()
					reader.org.AbuseHoldAt = &now
				}
				err := RequireActiveOwner(t.Context(), reader, state.App{AccountID: "creator", OrgID: "company"})
				allowed := !held && (status == state.OrgStatusActive || status == state.OrgStatusPastDue)
				if (err == nil) != allowed {
					t.Fatalf("allowed=%v err=%v", allowed, err)
				}
			})
		}
	}
}
