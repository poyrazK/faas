package apidserver_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 127 — a fleet with more apps than one regression pass walks is
// walked in turn: each pass starts after the previous pass's last app and
// wraps, so no app is skipped every pass.
func TestRegressionAppsAfterWalksEveryApp(t *testing.T) {
	ids := make([]pgtype.UUID, 5)
	for i := range ids {
		ids[i] = pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-00000000000" + string(rune('1'+i))), Valid: true}
	}
	shuffled := []pgtype.UUID{ids[3], ids[0], ids[4], ids[2], ids[1]}

	seen := map[string]int{}
	after := ""
	for range 5 {
		pass := apidserver.RegressionAppsAfter(shuffled, after, 2)
		if len(pass) != 2 {
			t.Fatalf("pass after %q = %d apps, want 2", after, len(pass))
		}
		for _, id := range pass {
			seen[id.String()]++
		}
		after = pass[len(pass)-1].String()
	}
	// Ten slots over five apps: every app walked exactly twice.
	for _, id := range ids {
		if seen[id.String()] != 2 {
			t.Fatalf("walks = %v, want every app twice", seen)
		}
	}
	if got := apidserver.RegressionAppsAfter(shuffled, "", 10); len(got) != 5 || got[0] != ids[0] {
		t.Fatalf("under the cap = %v, want all five from the first", got)
	}
}
