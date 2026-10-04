package devbridgeacceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// adr: 379 — this gate refuses simulated/native claims on macOS or without
// an explicitly designated host. Ordinary CI exercises the harness contracts.
func TestNativeDevBridgeAcceptance(t *testing.T) {
	if os.Getenv("FAAS_DEV_BRIDGE_ACCEPTANCE") != "native-x86" {
		t.Skip("run make native-dev-bridge-acceptance on a designated native host")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("native gate requires x86_64 Linux")
	}
	designation, err := os.ReadFile("/etc/faas/dev-bridge-acceptance-host")
	if err != nil {
		t.Fatal("native host designation is missing")
	}
	var topology NativeTopology
	if json.Unmarshal(designation, &topology) != nil || topology.API != os.Getenv("FAAS_API") || topology.ControlPlane == "" || len(topology.ComputeNodes) == 0 {
		t.Fatal("host designation must name this API, its control-plane and native compute nodes")
	}
	for _, node := range topology.ComputeNodes {
		if node == "" || node == topology.ControlPlane {
			t.Fatal("native split-box gate requires separate control-plane and compute nodes")
		}
	}
	kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		t.Fatal("native acceptance runner requires accessible /dev/kvm")
	}
	_ = kvm.Close()
	config := Config{API: os.Getenv("FAAS_API"), Token: os.Getenv("FAAS_TOKEN"), Project: os.Getenv("FAAS_BRIDGE_PROJECT"), Environment: os.Getenv("FAAS_BRIDGE_ENVIRONMENT"), Payments: os.Getenv("FAAS_BRIDGE_PAYMENTS"), Frontend: os.Getenv("FAAS_BRIDGE_FRONTEND"), Inventory: os.Getenv("FAAS_BRIDGE_INVENTORY"), IdleFor: 2 * time.Minute}
	if config.Environment == "" {
		config.Environment = "development"
	}
	if value := os.Getenv("FAAS_BRIDGE_IDLE_FOR"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			t.Fatal("FAAS_BRIDGE_IDLE_FOR must be a duration")
		}
		config.IdleFor = duration
	}
	ctx, cancel := context.WithTimeout(t.Context(), config.IdleFor+5*time.Minute)
	defer cancel()
	report, err := Run(ctx, config)
	report.Topology = &topology
	report.SourceCommit = strings.TrimSpace(os.Getenv("FAAS_BRIDGE_SOURCE_COMMIT"))
	if output := os.Getenv("FAAS_BRIDGE_EVIDENCE"); output != "" {
		path, pathErr := filepath.Abs(output)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native bridge acceptance passed %d assertions; idle edge lifetime verified for %s", len(report.Passed), config.IdleFor)
}
