package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestDataAPIVerifyRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{nil, {"unexpected"}, {"--checks", "checks.json", "--directory", ""}, {"--unknown"}} {
		if main.CmdDataAPIVerify(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
