// Command eviction-dryrun exercises the deterministic RAM-pressure eviction
// policy and writes an operator-readable evidence record.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

const syntheticResidentMB = 47_500

func main() {
	now := time.Now().UTC().Truncate(time.Second)
	instances := []sched.InstanceInfo{
		{Instance: "free-old", Plan: api.PlanFree, State: state.StateRunning, RAMMB: 2_500, LastRequest: now.Add(-4 * time.Hour), Started: now.Add(-10 * time.Minute)},
		{Instance: "hobby-old", Plan: api.PlanHobby, State: state.StateRunning, RAMMB: 2_500, LastRequest: now.Add(-3 * time.Hour), Started: now.Add(-10 * time.Minute)},
		{Instance: "scale-old", Plan: api.PlanScale, State: state.StateRunning, RAMMB: 2_500, LastRequest: now.Add(-2 * time.Hour), Started: now.Add(-10 * time.Minute)},
		{Instance: "reserved-old", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 2_500, EvictionPriority: string(api.EvictionPriorityReserved), LastRequest: now.Add(-1 * time.Hour), Started: now.Add(-10 * time.Minute)},
		{Instance: "young-old", Plan: api.PlanHobby, State: state.StateRunning, RAMMB: 2_500, LastRequest: now.Add(-5 * time.Hour), Started: now.Add(-10 * time.Second)},
		{Instance: "service-old", Plan: api.PlanPro, State: state.StateRunning, RAMMB: 2_500, Mode: string(state.InstanceModeService), LastRequest: now.Add(-6 * time.Hour), Started: now.Add(-10 * time.Minute)},
	}
	expected := []string{"free-old", "hobby-old", "scale-old", "reserved-old"}
	actual := sched.SelectEvictions(syntheticResidentMB, now, instances)
	finalResident := syntheticResidentMB
	for _, id := range actual {
		for _, instance := range instances {
			if instance.Instance == id {
				finalResident -= api.BillableRAMMBWithSidecars(instance.RAMMB, instance.SidecarMBs)
				break
			}
		}
	}

	checks := []string{
		check("selection order", reflect.DeepEqual(actual, expected)),
		check("young instance protected", !contains(actual, "young-old")),
		check("service instance protected", !contains(actual, "service-old")),
		check("resident below threshold", finalResident <= sched.EvictionThresholdMB),
	}
	pass := !contains(checks, "FAIL")

	recordDir := os.Getenv("FAAS_DRILL_RECORD_DIR")
	if recordDir == "" {
		recordDir = "docs/drills"
	}
	if err := os.MkdirAll(recordDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create evidence directory: %v\n", err)
		os.Exit(1)
	}
	path := filepath.Join(recordDir, now.Format("2006-01-02-150405")+"-eviction.md")
	record := fmt.Sprintf(`# Eviction dry run

- **UTC:** %s
- **Synthetic resident RAM:** %d MB
- **Eviction threshold:** %d MB
- **Expected order:** %s
- **Actual order:** %s
- **Final resident RAM:** %d MB
- **Protected fixtures:** young-old (age < 30s), service-old (service replica)

## Checks

%s

**Verdict: %s**
`, now.Format(time.RFC3339), syntheticResidentMB, sched.EvictionThresholdMB, strings.Join(expected, " → "), strings.Join(actual, " → "), finalResident, strings.Join(checks, "\n"), verdict(pass))
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write evidence record: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s\n", path)
	if !pass {
		os.Exit(1)
	}
}

func check(name string, ok bool) string {
	if ok {
		return "- PASS: " + name
	}
	return "- FAIL: " + name
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func verdict(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}
