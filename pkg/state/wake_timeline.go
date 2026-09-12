package state

import "sort"

const wakeBootStartedKind = "wake.boot_started"

// deduplicateWakeBootStarted keeps one logical boot marker in a
// customer-facing wake timeline. schedd is the canonical producer;
// vmmd may append a corroborating row with the same wake_id and kind.
// When reading legacy rows whose actor is not one of those producers,
// the earliest row remains the deterministic fallback.
//
// The raw events table is intentionally unchanged. Operator event reads
// can still inspect both observations when that distinction matters.
func deduplicateWakeBootStarted(events []Event) []Event {
	canonical := -1
	for i, event := range events {
		if event.Kind != wakeBootStartedKind {
			continue
		}
		if canonical < 0 || preferWakeBoot(event, events[canonical]) {
			canonical = i
		}
	}
	if canonical < 0 {
		return events
	}
	out := make([]Event, 0, len(events)-1)
	for i, event := range events {
		if event.Kind == wakeBootStartedKind && i != canonical {
			continue
		}
		out = append(out, event)
	}
	return out
}

func preferWakeBoot(candidate, current Event) bool {
	if candidate.Actor != current.Actor {
		return candidate.Actor == "schedd"
	}
	if candidate.At.Equal(current.At) {
		return candidate.ID < current.ID
	}
	return candidate.At.Before(current.At)
}

func sortWakeTimelineEvents(events []Event) {
	// Kept here with the canonical selection helper so every in-memory
	// implementation uses the same total ordering as Postgres.
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].ID < events[j].ID
		}
		return events[i].At.Before(events[j].At)
	})
}
