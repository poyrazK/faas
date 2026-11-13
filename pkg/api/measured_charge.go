package api

import "math"

// MeasuredChargeMB is ADR-909's charge for a running instance with plan RAM
// ramMB whose cgroup holds rssMB: its use, at least MeasuredChargeFloorMB,
// plus MeasuredChargeHeadroomPercent, and never more than the reservation
// BillableRAMMB(ramMB). A missing sample (NaN or negative) books the full
// reservation.
func MeasuredChargeMB(ramMB int, rssMB float64) int {
	full := BillableRAMMB(ramMB)
	if math.IsNaN(rssMB) || rssMB < 0 {
		return full
	}
	used := math.Max(rssMB, MeasuredChargeFloorMB)
	return min(int(math.Ceil(used*(100+MeasuredChargeHeadroomPercent)/100)), full)
}

// FrozenChargeMB is the charge for a frozen instance (ADR 20261031143521):
// MeasuredChargeMB with FrozenChargeFloorMB as its floor. A frozen VM is
// paused and cannot grow, and a thaw re-admits its full reservation like a
// wake, so it needs no room for a next request. A compressed one holds a few
// MB. A missing sample books the full reservation.
func FrozenChargeMB(ramMB int, rssMB float64) int {
	full := BillableRAMMB(ramMB)
	if math.IsNaN(rssMB) || rssMB < 0 {
		return full
	}
	used := math.Max(rssMB, FrozenChargeFloorMB)
	return min(int(math.Ceil(used*(100+MeasuredChargeHeadroomPercent)/100)), full)
}

// AgentReservationMB is the RAM an agent run VM with plan RAM ramMB books
// against the measured admission overcommit bound (ADR-20261027090015 §3):
// its version's p95 private peak with AgentReservationPeakFactorPercent of
// headroom, at least AgentReservationFloorMB and at most ramMB. Without a
// peak (peakMB <= 0) it books ramMB. PerVMOverheadMB is added by the caller.
func AgentReservationMB(ramMB, peakMB int) int {
	if peakMB <= 0 {
		return ramMB
	}
	headroom := (peakMB*AgentReservationPeakFactorPercent + 99) / 100
	return min(ramMB, max(AgentReservationFloorMB, headroom))
}

// AgentChargeMB is the measured charge of an agent run VM with plan RAM
// ramMB (ADR-20261027090015 §3): the larger of its memory (rssMB) and its
// version's p95 private peak, at least AgentChargeFloorMB, plus
// PerVMOverheadMB, and never more than BillableRAMMB(ramMB). A run is
// charged from its first second, so a missing sample (NaN or negative)
// charges the peak. Without a peak (peakMB <= 0) it is charged in full.
func AgentChargeMB(ramMB, peakMB int, rssMB float64) int {
	full := BillableRAMMB(ramMB)
	if peakMB <= 0 {
		return full
	}
	used := max(peakMB, AgentChargeFloorMB)
	if !math.IsNaN(rssMB) && rssMB >= 0 {
		used = max(used, int(math.Ceil(rssMB)))
	}
	return min(used+PerVMOverheadMB, full)
}
