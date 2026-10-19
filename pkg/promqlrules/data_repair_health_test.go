package promqlrules_test

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDataRepairHealthRules(t *testing.T) {
	// Prometheus expressions cannot import Go constants. Keep the shipped
	// freshness window in sync with the central operator-health limit.
	for _, r := range loadShippedRules(t) {
		if r.Labels["family"] != "data_repair" {
			continue
		}
		window := "time() - " + strconv.Itoa(api.DataRepairHealthStaleSeconds) + ")"
		if r.Alert == "FaasDataRepairHealthStale" {
			window = "> " + strconv.Itoa(api.DataRepairHealthStaleSeconds) + ")"
		}
		if !strings.Contains(r.Expr, window) {
			t.Errorf("%s freshness window differs from api.DataRepairHealthStaleSeconds", r.Alert)
		}
	}
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool is required by CI and the strict data repair gate")
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "data_repair_health.test.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), promtool, "test", "rules", fixture).CombinedOutput(); err != nil {
		t.Fatalf("repair health alert evaluation: %v\n%s", err, out)
	}
}
