package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// An operator clears a customer's lost factor: the authenticator goes, every
// browser is signed out and the reset is audited. An account without a
// factor, and an operator account, are refused.
func TestObsAccountResetMFA(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := t.Context()
	store := *e.Store()
	target, err := store.CreateAccount(ctx, "lost-factors@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(id string) int {
		t.Helper()
		return e.DoAdmin(t, http.MethodPost, "/v1/admin/ops/accounts/"+id+"/reset-mfa?confirm=true&reason=lost_factors", nil, nil).Code
	}
	if got := reset(target.ID); got != http.StatusConflict {
		t.Fatalf("reset without a factor = %d, want 409", got)
	}
	if err := store.SetMFASecret(ctx, target.ID, []byte("sealed"), [][]byte{make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMFAEnrolled(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, "11111111-1111-1111-1111-111111111111", target.ID, "192.0.2.1", "ua"); err != nil {
		t.Fatal(err)
	}
	if got := reset(target.ID); got != http.StatusOK {
		t.Fatalf("reset = %d, want 200", got)
	}
	after, err := store.AccountByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.MFAEnrolled() || len(after.MFARecoveryCodesHash) != 0 {
		t.Fatal("the factor survived the reset")
	}
	sessions, err := store.ListSessions(ctx, target.ID)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("live sessions after reset = %d (%v)", len(sessions), err)
	}
	events, err := store.ListEvents(ctx, target.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.Kind] = true
	}
	if !seen["operator.action.account_reset_mfa"] || !seen["account.mfa_disabled"] {
		t.Fatalf("audit kinds = %v", seen)
	}

	operator := (*e.Acct()).ID
	if err := store.MarkMFAEnrolled(ctx, operator); err != nil {
		t.Fatal(err)
	}
	if got := reset(operator); got != http.StatusConflict {
		t.Fatalf("reset of the calling operator = %d, want 409", got)
	}
}
