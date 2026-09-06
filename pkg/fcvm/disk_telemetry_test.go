package fcvm

import (
	"testing"
)

func TestDiskPressureThresholds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		used, capacity int64
		want           DiskPressure
	}{
		{"normal", 79, 100, DiskPressureNormal},
		{"near full", 80, 100, DiskPressureNearFull},
		{"full", 95, 100, DiskPressureFull},
		{"invalid", 101, 100, DiskPressureUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diskPressure(tc.used, tc.capacity); got != tc.want {
				t.Fatalf("diskPressure(%d,%d) = %s, want %s", tc.used, tc.capacity, got, tc.want)
			}
		})
	}
}

func TestManagerReportDiskUsageTransitions(t *testing.T) {
	m, _, _ := newPureManager()
	seedLive(m, "disk-1", "app-1", "dep-1", 1)
	if pressure, changed := m.ReportDiskUsage("disk-1", 79, 100); pressure != DiskPressureNormal || changed {
		t.Fatalf("first sample = (%s,%v), want normal,false", pressure, changed)
	}
	if pressure, changed := m.ReportDiskUsage("disk-1", 79, 100); pressure != DiskPressureNormal || changed {
		t.Fatalf("same pressure = (%s,%v), want normal,false", pressure, changed)
	}
	if pressure, changed := m.ReportDiskUsage("disk-1", 96, 100); pressure != DiskPressureFull || !changed {
		t.Fatalf("full transition = (%s,%v), want full,true", pressure, changed)
	}
	usage, ok := m.DiskUsage("disk-1")
	if !ok || usage.UsedBytes != 96 || usage.CapacityBytes != 100 || usage.Pressure != DiskPressureFull {
		t.Fatalf("DiskUsage = (%#v,%v), want latest full sample", usage, ok)
	}
	if pressure, changed := m.ReportDiskUsage("missing", 10, 100); pressure != DiskPressureUnknown || changed {
		t.Fatalf("missing instance = (%s,%v), want unknown,false", pressure, changed)
	}
}
