// adr: 051
package fcvm

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A Node app that hard-coded 8080 while its deployment routed to the inferred
// 3000 failed as "the app never listened or exited during startup", although
// characterization had seen it listening. The failure now names both ports.
func TestPortMismatchProblemNamesTheObservedPort(t *testing.T) {
	refused := api.NewProblem(422, api.CodeAppNotListening, "no process listening on $PORT", "ECONNREFUSED")
	for _, tc := range []struct {
		name    string
		report  api.CharacterizationReport
		appPort int
		err     error
		want    bool
	}{
		{name: "other port", report: api.CharacterizationReport{ObservedPort: 8080, ListeningAddrs: []string{"0.0.0.0:8080"}}, appPort: 3000, err: refused, want: true},
		{name: "default routed port", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"[::]:3000"}}, err: refused, want: true},
		{name: "routed port", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"0.0.0.0:3000"}}, appPort: 3000, err: refused},
		{name: "no listener", report: api.CharacterizationReport{}, appPort: 3000, err: refused},
		{name: "other readiness failure", report: api.CharacterizationReport{ObservedPort: 8080}, appPort: 3000, err: errors.New("guest exited")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := portMismatchProblem(tc.report, tc.appPort, tc.err)
			if !tc.want {
				if got != nil {
					t.Fatalf("portMismatchProblem = %#v, want nil", got)
				}
				return
			}
			p := api.AsProblem(got)
			if p == nil || p.Code != api.CodeAppNotListening {
				t.Fatalf("portMismatchProblem = %#v, want app_not_listening", got)
			}
			routed := tc.appPort
			if routed == 0 {
				routed = api.DefaultAppPort
			}
			for _, want := range []string{
				"listens on port " + itoa(tc.report.ObservedPort),
				"routes to port " + itoa(routed),
				"hosting.port",
			} {
				if !strings.Contains(p.Detail, want) {
					t.Fatalf("detail %q does not say %q", p.Detail, want)
				}
			}
		})
	}
}

// The CLI reads "on :N got ECONNREFUSED (... deadline=...)" as an app that
// started too slowly and rewrites the hint, so the mismatch detail must not
// carry the wrapped probe text.
func TestPortMismatchProblemOmitsTheProbeText(t *testing.T) {
	refused := api.NewProblem(422, api.CodeAppNotListening, "no process listening on $PORT",
		"startup_phase=handler_boot: readiness probe on :3000 got ECONNREFUSED (refused_count=3, deadline=30s, instance=i)")
	p := api.AsProblem(portMismatchProblem(api.CharacterizationReport{ObservedPort: 8080}, 3000, refused))
	if p == nil || strings.Contains(p.Detail, "ECONNREFUSED") {
		t.Fatalf("portMismatchProblem = %#v, want a detail without the probe text", p)
	}
}
