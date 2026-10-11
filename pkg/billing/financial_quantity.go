package billing

import (
	"fmt"
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

// FormatFinancialQuantity turns retained meter units into customer-readable
// units for human-facing views. JSON keeps the exact source quantity and unit.
func FormatFinancialQuantity(quantity int64, unit string) string {
	switch unit {
	case "mb_seconds":
		return fmt.Sprintf("%.4f GB-hours", float64(quantity)/float64(api.SecondsPerGBHour))
	case "interface_bytes":
		return formatInterfaceBytes(quantity)
	case "":
		return fmt.Sprintf("%d units", quantity)
	default:
		return fmt.Sprintf("%d %s", quantity, unit)
	}
}

func formatInterfaceBytes(quantity int64) string {
	units := [...]string{"bytes", "KiB", "MiB", "GiB", "TiB"}
	value := math.Abs(float64(quantity))
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	sign := ""
	if quantity < 0 {
		sign = "-"
	}
	if unit == 0 {
		return fmt.Sprintf("%s%d bytes", sign, int64(value))
	}
	return fmt.Sprintf("%s%.2f %s", sign, value, units[unit])
}
