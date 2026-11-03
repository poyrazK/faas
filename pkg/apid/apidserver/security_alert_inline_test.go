package apidserver_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// H99: with the alert outboxes off, a password change, an MFA enrollment and
// an MFA removal each mail the owner, the standard takeover signal; they used
// to write only an audit row. Each kind has its own per-account budget.
func TestInlineSecurityAlertsMailTheOwner(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "owner@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	mailer := &apidserver.RecordingMailer{}
	*srv.Mailer() = mailer

	srv.AlertPasswordChangedInline(t.Context(), acct, "dashboard")
	srv.AlertMFAEnabledInline(t.Context(), acct)
	srv.AlertMFADisabledInline(t.Context(), acct, "recovery_code")
	// A repeat inside the per-kind interval is throttled.
	srv.AlertPasswordChangedInline(t.Context(), acct, "dashboard")

	deadline := time.Now().Add(5 * time.Second)
	for len(mailer.Snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let a wrongly unthrottled repeat land
	sent := mailer.Snapshot()
	if len(sent) != 3 {
		t.Fatalf("alerts sent = %d, want 3 (one per kind, the repeat throttled)", len(sent))
	}
	subjects := map[string]bool{}
	for _, m := range sent {
		if len(m.To) != 1 || m.To[0] != acct.Email {
			t.Fatalf("alert addressed to %v, want the owner", m.To)
		}
		subjects[m.Subject] = true
	}
	for _, want := range []string{"Password updated", "MFA enabled", "MFA disabled"} {
		found := false
		for s := range subjects {
			if strings.Contains(s, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no alert with subject containing %q in %v", want, subjects)
		}
	}
}
