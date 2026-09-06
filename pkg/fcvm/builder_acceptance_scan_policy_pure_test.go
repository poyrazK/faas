package fcvm_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
)

func TestAcceptanceScanPolicyFromEnv(t *testing.T) {
	t.Run("defaults to fixed high findings", func(t *testing.T) {
		t.Setenv(acceptanceScanFailOnEnv, "")
		t.Setenv(acceptanceScanOnlyFixedEnv, "")
		policy, err := acceptanceScanPolicyFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if policy.FailOn != imaged.SeverityHigh || !policy.OnlyFixed {
			t.Fatalf("default policy = %+v", policy)
		}
	})

	t.Run("accepts explicit policy", func(t *testing.T) {
		t.Setenv(acceptanceScanFailOnEnv, "critical")
		t.Setenv(acceptanceScanOnlyFixedEnv, "false")
		policy, err := acceptanceScanPolicyFromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if policy.FailOn != imaged.SeverityCritical || policy.OnlyFixed {
			t.Fatalf("explicit policy = %+v", policy)
		}
	})

	for _, tc := range []struct {
		name, failOn, onlyFixed string
	}{
		{name: "invalid severity", failOn: "unknown", onlyFixed: "true"},
		{name: "invalid boolean", failOn: "high", onlyFixed: "sometimes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(acceptanceScanFailOnEnv, tc.failOn)
			t.Setenv(acceptanceScanOnlyFixedEnv, tc.onlyFixed)
			if _, err := acceptanceScanPolicyFromEnv(); err == nil {
				t.Fatal("expected invalid policy to fail")
			}
		})
	}
}

func TestAcceptanceScanPolicyViolations(t *testing.T) {
	result := imaged.ScanResult{Vulnerabilities: []imaged.Vulnerability{
		{ID: "CVE-fixed-critical", Severity: imaged.SeverityCritical, FixedIn: "2.0"},
		{ID: "CVE-unfixed-critical", Severity: imaged.SeverityCritical},
		{ID: "CVE-fixed-high", Severity: imaged.SeverityHigh, FixedIn: "1.1"},
		{ID: "CVE-unfixed-high", Severity: imaged.SeverityHigh},
		{ID: "CVE-fixed-medium", Severity: imaged.SeverityMedium, FixedIn: "1.2"},
	}}

	t.Run("fixed high matches image publishing policy", func(t *testing.T) {
		got := (acceptanceScanPolicy{FailOn: imaged.SeverityHigh, OnlyFixed: true}).violations(result)
		if len(got) != 2 || got[0].ID != "CVE-fixed-critical" || got[1].ID != "CVE-fixed-high" {
			t.Fatalf("violations = %+v", got)
		}
	})

	t.Run("critical threshold excludes high", func(t *testing.T) {
		got := (acceptanceScanPolicy{FailOn: imaged.SeverityCritical, OnlyFixed: true}).violations(result)
		if len(got) != 1 || got[0].ID != "CVE-fixed-critical" {
			t.Fatalf("violations = %+v", got)
		}
	})

	t.Run("all findings includes unfixed", func(t *testing.T) {
		got := (acceptanceScanPolicy{FailOn: imaged.SeverityHigh, OnlyFixed: false}).violations(result)
		if len(got) != 4 {
			t.Fatalf("violations = %+v", got)
		}
	})
}
