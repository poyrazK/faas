// spec: §6.4
package fcvm

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #9: an app listening on 127.0.0.1 failed readiness as
// app_not_listening; the characterization report already named the loopback
// listener that spec §6.4 amendment 1 maps to app_loopback_bound.
func TestLoopbackBindProblemReclassifiesRefusedReadiness(t *testing.T) {
	refused := api.NewProblem(422, api.CodeAppNotListening, "no process listening on $PORT", "ECONNREFUSED")
	for _, tc := range []struct {
		name   string
		report api.CharacterizationReport
		err    error
		want   bool
	}{
		{name: "ipv4 loopback", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"127.0.0.1:3000"}}, err: refused, want: true},
		{name: "ipv6 loopback", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"[::1]:3000"}}, err: refused, want: true},
		{name: "wildcard", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"0.0.0.0:3000"}}, err: refused},
		{name: "no listener", report: api.CharacterizationReport{ListeningAddrs: []string{"127.0.0.1:0"}}, err: refused},
		{name: "other readiness failure", report: api.CharacterizationReport{ObservedPort: 3000, ListeningAddrs: []string{"127.0.0.1:3000"}}, err: errors.New("guest exited")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := loopbackBindProblem(tc.report, tc.err)
			p := api.AsProblem(got)
			if (p != nil && p.Code == api.CodeAppLoopbackBound) != tc.want || (!tc.want && got != nil) {
				t.Fatalf("loopbackBindProblem = %#v, want loopback=%v", got, tc.want)
			}
		})
	}
}
