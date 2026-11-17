// adr: 1027 — a client whose database schedd refuses to wake is told why,
// not to retry shortly (amendment 2026-11-10).
package pgproxy

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbrefusal"
)

func TestWakeRefusedTellsTheClientWhy(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{dbrefusal.Status("wake", dbrefusal.Held), "08004"},
		{dbrefusal.Status("wake", dbrefusal.ComputeQuota), "53400"},
		{errors.New("connection refused"), ""},
	} {
		got := wakeRefused(tc.err)
		switch {
		case tc.code == "" && got != nil:
			t.Errorf("wakeRefused(%v) = %v, want nil", tc.err, got)
		case tc.code != "" && (got == nil || got.code != tc.code):
			t.Errorf("wakeRefused(%v) = %v, want %s", tc.err, got, tc.code)
		}
	}
}
