package mail

import (
	"strings"
	"testing"
	"time"
)

func TestBudgetThresholdBody(t *testing.T) {
	period := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		in          BudgetThreshold
		wantSubject string
		wantBody    []string
		notBody     []string
	}{
		{
			name:        "complete coverage",
			in:          BudgetThreshold{BudgetName: "Production", Basis: "net_usage", ThresholdMillicents: 2_000_000, KnownMillicents: 2_150_000, LimitMillicents: 5_000_000, PeriodStart: period, CoverageComplete: true},
			wantSubject: `Gregale budget "Production" passed $20.00 this month`,
			wantBody:    []string{"Known net usage", "Known spending so far: $21.50", "Budget limit:          $50.00", "2026-10-01", "keep running"},
			notBody:     []string{"may be higher"},
		},
		{
			name:        "incomplete gross coverage",
			in:          BudgetThreshold{BudgetName: "Previews", Basis: "gross_usage", ThresholdMillicents: 1_000, KnownMillicents: 1_000, LimitMillicents: 1_000, PeriodStart: period},
			wantSubject: `Gregale budget "Previews" passed $0.01 this month`,
			wantBody:    []string{"Known gross usage", "may be higher"},
		},
		{
			name:        "control characters cannot inject headers",
			in:          BudgetThreshold{BudgetName: "a\r\nBcc: x@example.com", Basis: "net_usage", PeriodStart: period, CoverageComplete: true},
			wantSubject: `Gregale budget "aBcc: x@example.com" passed $0.00 this month`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body := BudgetThresholdBody(tt.in)
			if subject != tt.wantSubject {
				t.Fatalf("subject = %q, want %q", subject, tt.wantSubject)
			}
			if strings.ContainsAny(subject, "\r\n") {
				t.Fatalf("subject contains CR/LF: %q", subject)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q:\n%s", want, body)
				}
			}
			for _, unwanted := range tt.notBody {
				if strings.Contains(body, unwanted) {
					t.Errorf("body contains %q:\n%s", unwanted, body)
				}
			}
		})
	}
}
