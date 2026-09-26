//go:build linux

package internal

import (
	"os/exec"
	"testing"
)

func TestProcessResourceUsageReadsChildWaitStatus(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf ok")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	usage := ProcessResourceUsage(cmd.ProcessState)
	if !usage.Available || usage.CPUTimeMS < 0 || usage.PeakRSSMB <= 0 {
		t.Fatalf("ProcessResourceUsage() = %+v, want available non-negative CPU and positive RSS", usage)
	}
}
