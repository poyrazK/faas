package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResidentGBHoursPerMonth(t *testing.T) {
	// Tolerance: 0.05 GB-h (well below display precision of 0.1).
	const tol = 0.05
	cases := []struct {
		name  string
		plan  api.Plan
		ramMB int
		min   int
		want  float64
	}{
		// Free / Hobby: min must be 0 (api rejects); helper returns 0.
		{"Free min=0", api.PlanFree, 128, 0, 0},
		{"Free min=1 clamps to 0", api.PlanFree, 128, 1, 0},
		{"Hobby min=1 clamps to 0", api.PlanHobby, 256, 1, 0},

		// Pro 512 MB: (512+8) × 1 × 30 / 1024 = 15600/1024 = 15.234375
		{"Pro 1x512", api.PlanPro, 512, 1, 15.234375},

		// Pro 256 MB: (256+8) × 1 × 30 / 1024 = 7920/1024 = 7.734375
		{"Pro 1x256", api.PlanPro, 256, 1, 7.734375},

		// Scale 1024 MB × 2: (1024+8) × 2 × 30 / 1024 = 61920/1024 = 60.46875
		{"Scale 2x1024", api.PlanScale, 1024, 2, 60.46875},

		// Scale 1024 MB × 1: (1024+8) × 1 × 30 / 1024 = 30960/1024 = 30.234375
		{"Scale 1x1024", api.PlanScale, 1024, 1, 30.234375},

		// Pro min=0 → 0 even if plan allows.
		{"Pro min=0", api.PlanPro, 512, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResidentGBHoursPerMonth(tc.plan, tc.ramMB, tc.min)
			if !floatNear(got, tc.want, tol) {
				t.Errorf("got %.4f, want %.4f (±%.2f)", got, tc.want, tol)
			}
		})
	}
}

func TestResidentGBHoursPerMonth_UnknownPlanReturnsZero(t *testing.T) {
	// An empty/unknown plan must NOT crash the CLI; production callers
	// can race a Whoami that returns "" or a stale plan. Returns 0.
	if got := ResidentGBHoursPerMonth("", 1024, 1); got != 0 {
		t.Errorf(`ResidentGBHoursPerMonth("", 1024, 1) = %v, want 0`, got)
	}
	if got := ResidentGBHoursPerMonth("enterprise", 1024, 1); got != 0 {
		t.Errorf(`ResidentGBHoursPerMonth("enterprise", ...) = %v, want 0`, got)
	}
}

func TestFormatGBHours(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "~0.0 GB-h/mo"},
		{15.234375, "~15.2 GB-h/mo"},
		{60.46875, "~60.5 GB-h/mo"},
		{7.734375, "~7.7 GB-h/mo"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := formatGBHours(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrintResidentCostEcho_Pro(t *testing.T) {
	var buf bytes.Buffer
	old := osStdout
	osStdout = &buf
	defer func() { osStdout = old }()

	printResidentCostEcho(api.PlanPro, 512, 1)
	out := buf.String()

	// Pins UX §6.5 copy.
	for _, want := range []string{
		"1 instance of 512 MB kept warm",
		"~15.2 GB-h/mo",
		"billed against your included quota",
		"1000 millicent/GB-h overage",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\nfull: %s", want, out)
		}
	}
	// Singular: no trailing 's' on "instance".
	if strings.Contains(out, "instances ") {
		t.Errorf("expected singular for min=1; got: %s", out)
	}
}

func TestPrintResidentCostEcho_ScalePlural(t *testing.T) {
	var buf bytes.Buffer
	old := osStdout
	osStdout = &buf
	defer func() { osStdout = old }()

	printResidentCostEcho(api.PlanScale, 1024, 2)
	out := buf.String()

	if !strings.Contains(out, "2 instances of 1024 MB kept warm") {
		t.Errorf("stdout missing plural form\nfull: %s", out)
	}
	if !strings.Contains(out, "~60.5 GB-h/mo") {
		t.Errorf("stdout missing Scale 2x1024 estimate\nfull: %s", out)
	}
}

func floatNear(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
