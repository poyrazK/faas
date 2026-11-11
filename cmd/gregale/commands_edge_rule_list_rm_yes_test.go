package main_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-60: `edge rule-list rm <name> --yes` was refused while
// `edge rule rm` requires --yes.
func TestCmdEdgeRuleListRmAcceptsYes(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, ``, http.StatusNoContent)
	oldOut := *main.OsStdout
	*main.OsStdout = &bytes.Buffer{}
	t.Cleanup(func() { *main.OsStdout = oldOut })
	if code := main.CmdEdgeRuleListsRm([]string{"office-ips", "--yes"}); code != 0 {
		t.Fatalf("rm --yes = %d", code)
	}
	if *f.SawMethod() != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", *f.SawMethod())
	}
}
