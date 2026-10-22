package schemalog

import (
	"regexp"
	"strings"
	"testing"
)

// adr: 20261016151947 — replication slot names are server-wide, so each
// database's slot carries its ID, in PostgreSQL's slot alphabet.
func TestSlot(t *testing.T) {
	long := strings.Repeat("A-", 40)
	valid := regexp.MustCompile(`^[a-z0-9_]{1,63}$`)
	for id, want := range map[string]string{
		"db_a":                                 "gregale_live_db_a",
		"3F1C0B6E-9A2B-44D1-8C7E-0123456789AB": "gregale_live_3f1c0b6e_9a2b_44d1_8c7e_0123456789ab",
		long:                                   ("gregale_live_" + strings.Repeat("a_", 40))[:63],
	} {
		if got := Slot(id); got != want || !valid.MatchString(got) {
			t.Errorf("Slot(%q) = %q, want %q", id, got, want)
		}
	}
}
