package daemonunitspec

// adr: 2026101801

import "testing"

// The daemons that hand off on reload are the four HTTP daemons; their units
// accept sd_notify only from the main process, so an old generation cannot
// speak for the unit after its successor took over.
func TestHandoffDaemonsReloadByHandoff(t *testing.T) {
	want := map[string]bool{"gatewayd-public": true, "gatewayd-internal": true, "apid": true, "realtimed": true}
	for _, e := range append(append([]Entry(nil), Registry...), OptionalRegistry...) {
		u := e.Unit()
		got := HandsOffOnReload(e.Name)
		if e.Name == "gatewayd-public-edge" {
			if !got {
				t.Errorf("gatewayd-public-edge runs gatewayd-public and should hand off too")
			}
		} else if got != want[e.Name] {
			t.Errorf("%s hands off on reload = %v, want %v", e.Name, got, want[e.Name])
		}
		if got && (u.Type != "notify" || u.NotifyAccess != "main" || u.OOMPolicy != "continue") {
			t.Errorf("%s: Type=%s NotifyAccess=%s OOMPolicy=%s, want notify/main/continue for a hand-off", e.Name, u.Type, u.NotifyAccess, u.OOMPolicy)
		}
	}
}
