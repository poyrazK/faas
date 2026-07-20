package dashboard

import "github.com/onebox-faas/faas/pkg/state"

// BadgeFor maps an instance state.State onto the public dashboard badge
// (ux_spec §6.3). The state machine has 7 internal states; the
// spec collapses them onto three glyphs plus one dim fallback so
// customers see ●/◌/⟳ rather than the internal vocabulary.
//
// Inputs:
//   - state.StateRunning              → running
//   - state.StateParked               → sleeping
//   - state.StateWaking, ColdBooting  → waking
//   - state.StateSnapshotting         → waking (it's waking's mirror phase)
//   - state.StateFailed/Stopped/unknown → idle
//
// Pure function; no template imports so badge_test.go can drive it
// without html/template in the loop.
func BadgeFor(s state.State) (cls, glyph, label string) {
	switch s {
	case state.StateRunning:
		return "running", "●", "running"
	case state.StateParked:
		return badgeSleeping, "◌", badgeSleeping
	case state.StateWaking, state.StateColdBooting, state.StateSnapshotting:
		return "waking", "⟳", "waking"
	case state.StateFailed, state.StateStopped:
		return "dim", "·", "idle"
	default:
		// Unknown state string — treat like "no live instance" so a
		// brand-new app (ListInstancesForApp returns []Instance)
		// renders as ◌ sleeping. The dashboard caller fills in
		// "parked" via BadgeForDefault below when the slice is empty.
		return "dim", "·", "idle"
	}
}

// badgeSleeping is promoted to a const so goconst (CI lint) stops
// flagging the 4-occurrence literal ("class", "label", comment,
// godoc). The dashboard copy is the user-facing string, not an
// internal identifier — keep it in the dashboard package.
const badgeSleeping = "sleeping"

// BadgeForDefault is the badge rendered when the app has no
// instance rows yet (fresh deploy, never woken). Per ux_spec §6.3,
// that's the parked glyph — "no traffic = asleep."
func BadgeForDefault() (cls, glyph, label string) {
	return BadgeFor(state.StateParked)
}
