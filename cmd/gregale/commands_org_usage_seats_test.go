package main_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-54: `org usage` printed "used: 1" and "limit: 200" with no unit.
func TestCmdOrgUsageNamesSeats(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, `{"plan":"scale","used":1,"limit":200}`, http.StatusOK)
	oldOut := *main.OsStdout
	var out bytes.Buffer
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = oldOut })
	if code := main.CmdOrgsSeatUsage([]string{"--org", "acme"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "seats: 1 used of 200") {
		t.Fatalf("output = %q", out.String())
	}
}
