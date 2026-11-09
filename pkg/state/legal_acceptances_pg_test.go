//go:build !no_pg

package state

import "testing"

func TestPgStore_CreateAccountWithPersonalOrg_TermsAcceptance(t *testing.T) {
	testTermsAcceptanceWithAccount(t, newPgStore(t))
}
