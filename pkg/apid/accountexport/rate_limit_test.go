package accountexport_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/accountexport"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExportRetryAfterUsesLatestExport(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		newer   int
		seconds int
	}{
		{"nearly reset", 23 * time.Hour, 0, 3600},
		{"other recent action", 12 * time.Hour, 1, 43200},
		{"other actions span pages", 23 * time.Hour, 105, 3600},
		{"round remaining fraction up", 24*time.Hour - 500*time.Millisecond, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := state.NewMemStore()
			now := time.Now().UTC()
			id := uuid.NewString()
			for i := range tc.newer {
				if err := st.AppendGdprRequest(t.Context(), state.GdprRequest{ID: uuid.NewString(), AccountID: id, AccountEmail: "export@example.com", Action: state.GdprActionRestore, RequestedAt: now.Add(-time.Duration(i+1) * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
			// Insert out of order: only requested_at determines the window.
			for i, age := range []time.Duration{tc.age, 48 * time.Hour} {
				if err := st.AppendGdprRequest(t.Context(), state.GdprRequest{ID: uuid.NewString(), AccountID: id, AccountEmail: fmt.Sprintf("export-%d@example.com", i), Action: state.GdprActionExport, RequestedAt: now.Add(-age)}); err != nil {
					t.Fatal(err)
				}
			}
			seconds, err := accountexport.RetryAfter(t.Context(), st, id, now)
			if err != nil || seconds != tc.seconds {
				t.Fatalf("retry after=%d err=%v, want %d", seconds, err, tc.seconds)
			}
		})
	}
}
