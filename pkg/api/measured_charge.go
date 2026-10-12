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
