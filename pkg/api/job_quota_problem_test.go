package api

import (
	"strings"
	"testing"
)

// A job --ram over the plan cap was told to cancel a task or delete a job
// (prod hunt r80).
func TestErrJobQuotaNamesTheSettingForPerJobCaps(t *testing.T) {
	t.Parallel()
	ram := ErrJobQuota(PlanHobby, "ram_mb", 1024, 4096)
	if strings.Contains(ram.Detail, "Cancel a task") || !strings.Contains(ram.Detail, "--ram") || ram.Limit == nil {
		t.Fatalf("ram_mb problem = %+v, want it to name --ram and keep the limit", ram)
	}
	count := ErrJobQuota(PlanHobby, "per_account", 5, 5)
	if !strings.Contains(count.Detail, "Cancel a task") {
		t.Fatalf("per_account problem = %+v, want the free-capacity advice", count)
	}
}
