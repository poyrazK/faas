package billing

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFormatFinancialQuantityUsesReadableMeterUnits(t *testing.T) {
	for _, tc := range []struct {
		quantity int64
		unit     string
		want     string
	}{
		{api.SecondsPerGBHour, "mb_seconds", "1.0000 GB-hours"},
		{1 << 30, "interface_bytes", "1.00 GiB"},
		{3, "requests", "3 requests"},
		{0, "", "0 units"},
	} {
		if got := FormatFinancialQuantity(tc.quantity, tc.unit); got != tc.want {
			t.Errorf("FormatFinancialQuantity(%d, %q) = %q, want %q", tc.quantity, tc.unit, got, tc.want)
		}
	}
}
