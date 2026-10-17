package api

import "testing"

func TestFormatBillingCents(t *testing.T) {
	tests := []struct {
		cents int64
		want  string
	}{
		{0, "$0.00"},
		{5, "$0.05"},
		{1000, "$10.00"},
		{9999, "$99.99"},
		{-250, "-$2.50"},
	}
	for _, tt := range tests {
		if got := FormatBillingCents(tt.cents); got != tt.want {
			t.Errorf("FormatBillingCents(%d) = %q, want %q", tt.cents, got, tt.want)
		}
	}
}

func TestFormatBillingMillicents(t *testing.T) {
	tests := []struct {
		millicents int64
		want       string
	}{
		{0, "$0.00"},
		{1_000, "$0.01"},
		{2_000_000, "$20.00"},
		{2_050_000, "$20.50"},
		{125, "$0.00125"},
		{1_234_560, "$12.3456"},
		{-1_000, "-$0.01"},
	}
	for _, tt := range tests {
		if got := FormatBillingMillicents(tt.millicents); got != tt.want {
			t.Errorf("FormatBillingMillicents(%d) = %q, want %q", tt.millicents, got, tt.want)
		}
	}
}
