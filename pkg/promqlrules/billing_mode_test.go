package promqlrules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBillingReconciliationUnavailableRequiresLiveMode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "expr: meterd_billing_mode_enabled == 1 and on(provider) meterd_billing_reconcile_supported == 0"
	if !strings.Contains(string(raw), want) {
		t.Fatal("BillingReconciliationUnavailable is not gated on live billing mode")
	}
}
