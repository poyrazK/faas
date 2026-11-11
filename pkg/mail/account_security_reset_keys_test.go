package mail_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

// The reset mail lists every key it left working and says how to revoke one.
func TestKeysAfterResetBody(t *testing.T) {
	subject, body := mail.KeysAfterResetBody("owner@example.com",
		[]string{"ci (created 2026-11-01)", "deploy (created 2026-11-02)"}, "https://gregale.dev/dashboard/account")
	if subject == "" {
		t.Fatal("empty subject")
	}
	for _, want := range []string{"owner@example.com", "  - ci (created 2026-11-01)\n", "  - deploy (created 2026-11-02)\n",
		"https://gregale.dev/dashboard/account", "gregale key delete ID"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}
