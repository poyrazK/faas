package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mail"
)

// fixedNow returns a stable time so the MessageID fingerprint
// (which includes the day) is reproducible across runs. Mirrors
// the pattern in cmd/gregale/commands_tier_b_test.go.
func fixedNow() time.Time {
	return time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
}

// TestMailDryRun_DefaultFlags pins the headline happy path: no
// unsubscribe URL, the renderer writes four templates
// (quota_warning, account_suspended, account_deletion_pending,
// payment_failed) and the JSON has the expected field set on
// every template. The CLI is the operator's eyeball gate for
// the bulk-sender compliance work — this test pins the field
// shape so a future rename doesn't silently break every `jq`
// pattern in operator playbooks.
func TestMailDryRun_DefaultFlags(t *testing.T) {
	var buf bytes.Buffer
	renders, err := mail.RenderAllTemplates("", fixedNow())
	if err != nil {
		t.Fatalf("RenderAllTemplates: %v", err)
	}
	if err := main.WriteMailDryRun(&buf, renders); err != nil {
		t.Fatalf("writeMailDryRun: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("output does not start with [; got: %.40s", out)
	}

	var roundTrip []mail.RenderTemplate
	if err := json.Unmarshal(buf.Bytes(), &roundTrip); err != nil {
		t.Fatalf("re-parse: %v\nbody=%s", err, out)
	}
	if len(roundTrip) != 4 {
		t.Errorf("template count = %d, want 4 (quota + suspended + deletion + payment)", len(roundTrip))
	}
	names := map[string]bool{}
	for _, r := range roundTrip {
		names[r.Name] = true
		if r.Subject == "" {
			t.Errorf("%s: empty subject", r.Name)
		}
		if r.TextBody == "" {
			t.Errorf("%s: empty text body", r.Name)
		}
		if r.MessageID == "" {
			t.Errorf("%s: empty message id", r.Name)
		}
	}
	for _, want := range []string{"quota_warning", "account_suspended", "account_deletion_pending", "payment_failed"} {
		if !names[want] {
			t.Errorf("missing template %q in %v", want, names)
		}
	}
}

// TestMailDryRun_WithUnsubscribeURL pins that the CLI passes
// through the --unsubscribe-url flag and the renderer applies
// the marketing headers (RFC 8058). Pre-PR this branch would
// silently drop the header — the CLI test catches that at the
// operator surface.
func TestMailDryRun_WithUnsubscribeURL(t *testing.T) {
	var buf bytes.Buffer
	renders, err := mail.RenderAllTemplates("https://faas.example.test/u", fixedNow())
	if err != nil {
		t.Fatalf("RenderAllTemplates: %v", err)
	}
	if err := main.WriteMailDryRun(&buf, renders); err != nil {
		t.Fatal(err)
	}
	var roundTrip []mail.RenderTemplate
	if err := json.Unmarshal(buf.Bytes(), &roundTrip); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	var quota *mail.RenderTemplate
	for i := range roundTrip {
		if roundTrip[i].Name == "quota_warning" {
			quota = &roundTrip[i]
			break
		}
	}
	if quota == nil {
		t.Fatal("quota_warning not in output")
	}
	if quota.Headers["List-Unsubscribe"] != "<https://faas.example.test/u>" {
		t.Errorf("quota List-Unsubscribe = %q, want <https://faas.example.test/u>", quota.Headers["List-Unsubscribe"])
	}
}

// TestCmdMailDispatch_UnknownSubcommand pins that the dispatcher
// returns 1 + a usage line for unknown subcommands — the
// close-set subcommand list is "dry-run" today; a typo must be
// caught loudly rather than silently no-op.
func TestCmdMailDispatch_UnknownSubcommand(t *testing.T) {
	if code := main.CmdMail([]string{"bogus"}); code != 1 {
		t.Errorf("cmdMail(bogus) = %d, want 1", code)
	}
}

// TestCmdMailDispatch_NoArgs pins that `gregale mail` with no
// subcommand returns 1 + usage. Otherwise the operator gets a
// silent exit-zero with no output.
func TestCmdMailDispatch_NoArgs(t *testing.T) {
	if code := main.CmdMail(nil); code != 1 {
		t.Errorf("cmdMail() = %d, want 1", code)
	}
}
