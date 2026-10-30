package reposcan

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// k8sCronJobSpec is the part of a CronJob spec beyond its schedule and
// suspend that the importer maps or reports (ADR-20261029093621 §2).
type k8sCronJobSpec struct {
	Spec struct {
		TimeZone                   string `yaml:"timeZone"`
		ConcurrencyPolicy          string `yaml:"concurrencyPolicy"`
		StartingDeadlineSeconds    *int64 `yaml:"startingDeadlineSeconds"`
		SuccessfulJobsHistoryLimit *int64 `yaml:"successfulJobsHistoryLimit"`
		FailedJobsHistoryLimit     *int64 `yaml:"failedJobsHistoryLimit"`
	} `yaml:"spec"`
}

// applyK8sCronJobSpec runs the CronJob's schedules in its timeZone when the
// kubernetes-manifests mapping is on, and reports the fields a cron sets
// after its first deploy instead. Without the mapping a timeZone is
// reported, because the schedule would otherwise run in UTC unnoticed.
func applyK8sCronJobSpec(s *workloadSeed, node *yaml.Node, mapping bool) ([]string, error) {
	var c k8sCronJobSpec
	if err := node.Decode(&c); err != nil {
		return nil, err
	}
	var warnings []string
	if zone := strings.TrimSpace(c.Spec.TimeZone); zone != "" {
		if !mapping {
			warnings = append(warnings, fmt.Sprintf("timeZone %s ignored: the schedule runs in UTC", zone))
		} else {
			if _, err := time.LoadLocation(zone); err != nil || zone == "Local" {
				return nil, fmt.Errorf("timeZone %q is not an IANA time zone", zone)
			}
			for i := range s.schedules {
				s.schedules[i].Timezone = zone
			}
		}
	}
	if !mapping {
		return warnings, nil
	}
	switch policy := c.Spec.ConcurrencyPolicy; policy {
	case "", "Allow":
	case "Forbid":
		warnings = append(warnings, "concurrencyPolicy Forbid not applied: runs may overlap; to skip a run while the previous one is active, use `gregale cron update CRON_ID --skip-if-running` after the first deploy")
	case "Replace":
		warnings = append(warnings, "concurrencyPolicy Replace not applied: runs may overlap; to replace an active run, set overlap replace with `gregale cron update CRON_ID --schedule-policy` after the first deploy")
	default:
		warnings = append(warnings, fmt.Sprintf("concurrencyPolicy %s ignored: runs may overlap", policy))
	}
	if d := c.Spec.StartingDeadlineSeconds; d != nil {
		warnings = append(warnings, fmt.Sprintf("startingDeadlineSeconds %d not applied: a run that starts late still runs; set start_deadline_seconds with `gregale cron update CRON_ID --schedule-policy` after the first deploy", *d))
	}
	if c.Spec.SuccessfulJobsHistoryLimit != nil || c.Spec.FailedJobsHistoryLimit != nil {
		warnings = append(warnings, "successfulJobsHistoryLimit and failedJobsHistoryLimit ignored: `gregale cron runs CRON_ID` lists every run for the invocation history retention period")
	}
	return warnings, nil
}
