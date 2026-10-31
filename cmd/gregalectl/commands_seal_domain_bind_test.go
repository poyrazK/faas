package main

import (
	"strings"
	"testing"
)

// adr: 20261023173624 — the binding pass applies to the runtime domain only
// and, like its re-seal, needs the operator's readers-verified statement.
func TestSealDomainMigrateBindAccountsGuards(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--domain", "outbound", "--bind-accounts", "--readers-verified"}, "--domain runtime only"},
		{[]string{"--domain", "build", "--bind-accounts"}, "--domain runtime only"},
		{[]string{"--domain", "runtime", "--bind-accounts"}, "--readers-verified"},
	} {
		err := runSealDomainMigrate(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("migrate %v = %v, want an error naming %q", tc.args, err, tc.want)
		}
	}
}
