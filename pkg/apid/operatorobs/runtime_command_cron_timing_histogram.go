package operatorobs

import "time"

// Histogram counters form one observation. Partial or incoherent snapshots and
// impossible interval distributions cannot establish a usable timing delta.
// The final unknown-timestamp counter remains independent of this group.
func runtimeCommandCronTimingHistogramValid(signals []RuntimeCommandCronTimingSignal, field string) bool {
	if len(signals) != len(runtimeCommandCronTimingDimensions()) {
		return false
	}
	var prior int64
	var clock time.Time
	for i, s := range signals[:len(signals)-1] {
		var value int64
		switch field {
		case "current", "previous":
			p := s.Current
			if field == "previous" {
				p = s.Previous
			}
			if p == nil {
				return false
			}
			value = p.Value
			at := p.ObservedAt
			if i > 0 && !at.Equal(clock) {
				return false
			}
			clock = at
		case "delta":
			if s.Delta == nil {
				return false
			}
			value = *s.Delta
		}
		if value < prior || i == len(signals)-2 && value != prior {
			return false
		}
		prior = value
	}
	return true
}

func normalizeRuntimeCommandCronTimingHistogram(item *RuntimeCommandCronTimingItem, compare bool) {
	h := item.Signals[:len(item.Signals)-1]
	for _, field := range []string{"current", "previous", "delta"} {
		if field != "current" && !compare {
			break
		}
		if field == "delta" {
			all := true
			for _, s := range h {
				all = all && s.Delta != nil
			}
			if !all {
				// A reset in one bucket invalidates every bucket's interval; process
				// discontinuities already suppress all deltas through shared proof.
				any := false
				for _, s := range h {
					any = any || s.Delta != nil
				}
				if !any {
					return
				}
			} else if runtimeCommandCronTimingHistogramValid(item.Signals, field) {
				continue
			}
		} else if runtimeCommandCronTimingHistogramValid(item.Signals, field) {
			continue
		}
		state, reason := "unavailable", "invalid_value"
		for _, s := range h {
			if field == "current" && s.Current == nil || field == "previous" && s.Previous == nil {
				state, reason = s.State, s.Reason
				break
			}
		}
		for i := range h {
			s := &item.Signals[i]
			s.Delta, s.Previous = nil, nil
			if field == "current" {
				s.Current = nil
			} else if field == "delta" {
				reason = "previous_invalid_evidence"
			} else if reason == "invalid_value" {
				reason = "previous_invalid_value"
			}
			s.State, s.Reason = state, reason
		}
		return
	}
}

func runtimeCommandCronTimingHistogramContract(signals []RuntimeCommandCronTimingSignal) bool {
	for _, field := range []string{"current", "previous", "delta"} {
		any := false
		for _, s := range signals[:len(signals)-1] {
			any = any || field == "current" && s.Current != nil || field == "previous" && s.Previous != nil || field == "delta" && s.Delta != nil
		}
		if any && !runtimeCommandCronTimingHistogramValid(signals, field) {
			return false
		}
	}
	return true
}
