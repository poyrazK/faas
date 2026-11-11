package main_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-60: `webhook account delete <id> --yes` read --yes as a
// second webhook ID.
func TestCmdAccountReleaseWebhookRmAcceptsTrailingYes(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, ``, http.StatusNoContent)
	oldOut := *main.OsStdout
	*main.OsStdout = &bytes.Buffer{}
	t.Cleanup(func() { *main.OsStdout = oldOut })
	if code := main.CmdAccountReleaseWebhooks([]string{"rm", webhookTestID, "--yes"}); code != 0 {
		t.Fatalf("rm --yes = %d", code)
	}
	if *f.SawMethod() != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", *f.SawMethod())
	}
}
