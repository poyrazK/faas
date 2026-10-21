// adr: 734
package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestAppHealthNextCheckSpacesOnlyQuietApps pins ADR-734 amendment 1: only an
// unchanged assessment with known capacity, no instances, no required
// capacity and no counted requests waits the quiet interval, including an
// idle app whose request evidence is unknown; anything else, including
// unknown capacity, keeps the 30-second target.
func TestAppHealthNextCheckSpacesOnlyQuietApps(t *testing.T) {
	quiet := func() api.AppHealthResponse {
		return api.AppHealthResponse{Status: "healthy", Capacity: api.AppHealthCapacity{Known: true}}
	}
	transition := []api.AppHealthHistoryEntry{{Kind: "transition"}}
	for _, tc := range []struct {
		name    string
		entries []api.AppHealthHistoryEntry
		edit    func(*api.AppHealthResponse)
		want    time.Duration
	}{
		{"quiet without request metrics", nil, func(*api.AppHealthResponse) {}, api.AppHealthCollectorQuietInterval},
		{"quiet with zero requests", nil, func(a *api.AppHealthResponse) {
			a.Requests = &api.AppHealthRequests{Known: true}
		}, api.AppHealthCollectorQuietInterval},
		{"changed assessment", transition, func(*api.AppHealthResponse) {}, api.AppHealthCollectorInterval},
		{"recent requests", nil, func(a *api.AppHealthResponse) {
			a.Requests = &api.AppHealthRequests{Known: true, RequestCount: 1}
		}, api.AppHealthCollectorInterval},
		{"idle app without request series", nil, func(a *api.AppHealthResponse) {
			// Production's idle apps: requests unknown (request_evidence_unavailable).
			a.Status = "unknown"
			a.Requests = &api.AppHealthRequests{}
		}, api.AppHealthCollectorQuietInterval},
		{"requests counted while evidence incomplete", nil, func(a *api.AppHealthResponse) {
			a.Requests = &api.AppHealthRequests{RequestCount: 3}
		}, api.AppHealthCollectorInterval},
		{"ready instance", nil, func(a *api.AppHealthResponse) { a.Capacity.Ready = 1 }, api.AppHealthCollectorInterval},
		{"starting instance", nil, func(a *api.AppHealthResponse) { a.Capacity.Starting = 1 }, api.AppHealthCollectorInterval},
		{"required capacity", nil, func(a *api.AppHealthResponse) { a.Capacity.Required = 1 }, api.AppHealthCollectorInterval},
		{"unknown capacity", nil, func(a *api.AppHealthResponse) { a.Capacity.Known = false }, api.AppHealthCollectorInterval},
		{"unknown capacity and status", nil, func(a *api.AppHealthResponse) {
			a.Status = "unknown"
			a.Capacity.Known = false
		}, api.AppHealthCollectorInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := quiet()
			tc.edit(&a)
			if got := appHealthNextCheck(tc.entries, a); got != tc.want {
				t.Fatalf("next check = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestAppHealthQuietIntervalNeverExpiresEvidence: a quiet app's next
// observation, even after a lost lease, a timed-out collection and one idle
// sweep, lands before its previous evidence expires, so the quiet cadence
// never records a gap.
func TestAppHealthQuietIntervalNeverExpiresEvidence(t *testing.T) {
	worst := api.AppHealthCollectorQuietInterval + api.AppHealthCollectorLease + api.AppHealthCollectionTimeout + api.AppHealthCollectorIdleInterval
	if worst >= api.AppHealthEvidenceMaxAge {
		t.Fatalf("quiet interval worst case %s >= evidence max age %s", worst, api.AppHealthEvidenceMaxAge)
	}
	if api.AppHealthCollectorQuietInterval < api.AppHealthCollectorInterval {
		t.Fatalf("quiet interval %s is shorter than the collector interval %s", api.AppHealthCollectorQuietInterval, api.AppHealthCollectorInterval)
	}
}
