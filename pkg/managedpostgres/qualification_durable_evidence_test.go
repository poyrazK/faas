// adr: 731 — memory-only evidence cannot authorize version-8 provisioning.
package managedpostgres

import "testing"

func TestQualificationRequiresDurableRestartEvidence(t *testing.T) {
	valid := passingLifecycleQualificationReport()
	if err := ValidateLifecycleQualificationReport(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*LifecycleQualificationReport){
		"legacy memory smoke":            func(r *LifecycleQualificationReport) { r.Mode = ""; r.Checks = r.Checks[:11] },
		"mode missing":                   func(r *LifecycleQualificationReport) { r.Mode = "" },
		"mode unknown":                   func(r *LifecycleQualificationReport) { r.Mode = "postgres" },
		"restart missing":                func(r *LifecycleQualificationReport) { r.Checks = r.Checks[:len(r.Checks)-1] },
		"duplicate hides required check": func(r *LifecycleQualificationReport) { r.Checks[len(r.Checks)-1] = r.Checks[0] },
		"cleanup failed":                 func(r *LifecycleQualificationReport) { r.Checks[len(r.Checks)-1].Passed = false },
		"passed with error":              func(r *LifecycleQualificationReport) { r.Checks[len(r.Checks)-1].Error = "unavailable" },
	} {
		t.Run(name, func(t *testing.T) {
			report := passingLifecycleQualificationReport()
			mutate(&report)
			if err := ValidateLifecycleQualificationReport(report); err == nil {
				t.Fatal("incomplete durable evidence authorized provisioning")
			}
		})
	}
}
