package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// "list requires --app and 0 operation ID positional argument(s)" read
// badly (Hunt 12 H12-48).
func TestCustomerOperationsArgumentMessage(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"customer-operations", "list"}, "list requires --app and takes no operation ID"},
		{[]string{"customer-operations", "get", "--app", "web"}, "get requires --app and one operation ID"},
	}
	for _, tt := range tests {
		main.ResetJSONOut(t)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		t.Setenv("FAAS_API", "http://127.0.0.1:1")
		_, stderr, restore := main.SwapIO(t)
		code := main.Run(tt.args)
		restore()
		if got := stderr(); code == 0 || !strings.Contains(got, tt.want) {
			t.Errorf("run(%v) exit=%d, want %q in: %q", tt.args, code, tt.want, got)
		}
	}
}
