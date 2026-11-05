package dashboard_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// The decider is process-wide wiring, so this test does not run in parallel.
func TestPageOnHidesCapabilitiesTheDeciderTurnsOff(t *testing.T) {
	page := dashboard.Page{Account: &dashboard.AccountView{ID: "acct-1", Plan: "pro"}}
	if !page.On("realtime") {
		t.Fatal("with no decider installed every capability must count as released")
	}
	dashboard.SetCapabilityDecider(func(accountID, plan, capability string) bool {
		return accountID == "acct-1" && plan == "pro" && capability != "realtime"
	})
	t.Cleanup(func() { dashboard.SetCapabilityDecider(func(string, string, string) bool { return true }) })
	if page.On("realtime") {
		t.Fatal("a capability the decider turns off must be hidden")
	}
	if !page.On("crons") {
		t.Fatal("a capability the decider releases must be shown")
	}
	if !(dashboard.Page{}).On("realtime") {
		t.Fatal("a page with no signed-in account must not hide links")
	}
}
