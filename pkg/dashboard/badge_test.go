package dashboard

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestBadgeFor locks the 7-state → 3-glyph mapping (ux_spec §6.3).
// Table-driven so a new internal state only needs a row to land.
func TestBadgeFor(t *testing.T) {
	tests := []struct {
		state state.State
		wantC string
		wantG string
		wantL string
	}{
		{state.StateRunning, "running", "●", "running"},
		{state.StateParked, "sleeping", "◌", "sleeping"},
		{state.StateWaking, "waking", "⟳", "waking"},
		{state.StateColdBooting, "waking", "⟳", "waking"},
		{state.StateSnapshotting, "waking", "⟳", "waking"},
		{state.StateFailed, "dim", "·", "idle"},
		{state.StateStopped, "dim", "·", "idle"},
		// Unknown state — defensive fallback so a future spec
		// addition doesn't render as a literal string and break
		// the CSS.
		{state.State("teleporting"), "dim", "·", "idle"},
	}
	for _, tt := range tests {
		cls, glyph, label := BadgeFor(tt.state)
		if cls != tt.wantC || glyph != tt.wantG || label != tt.wantL {
			t.Errorf("BadgeFor(%q) = (%q,%q,%q), want (%q,%q,%q)",
				tt.state, cls, glyph, label, tt.wantC, tt.wantG, tt.wantL)
		}
	}
}

// TestBadgeForDefault pins the "no instance rows" case: an app
// that's never woken renders as ◌ sleeping. Matches user
// expectation (no traffic = asleep), not · idle (which is for
// crashed/cold-without-snapshot rows).
func TestBadgeForDefault(t *testing.T) {
	cls, glyph, label := BadgeForDefault()
	if cls != "sleeping" || glyph != "◌" || label != "sleeping" {
		t.Errorf("BadgeForDefault() = (%q,%q,%q), want (sleeping,◌,sleeping)", cls, glyph, label)
	}
}
