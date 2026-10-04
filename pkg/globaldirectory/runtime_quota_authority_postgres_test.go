//go:build !no_pg

package globaldirectory

import "testing"

func TestGlobalRuntimeQuotaAuthorityPostgres(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { _, f := postgresQuotaFixture(t, shared, 1); exerciseQuotaAuthorityProtocol(t, f) })
	}
}
