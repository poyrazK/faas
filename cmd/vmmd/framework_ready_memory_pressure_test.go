//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestParseFrameworkReadyMemoryPressure(t *testing.T) {
	msg, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeMemoryPressure}, []byte(`{"available_mb":30,"total_mb":246,"anon_mb":190}`)...))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != parseFWReadyKindMemoryPressure || msg.MemoryPressure != (memoryPressureWire{AvailableMB: 30, TotalMB: 246, AnonMB: 190}) {
		t.Fatalf("parsed %+v", msg)
	}
	// A guest-init that predates the flag reported only while profiling.
	if !msg.MemoryPressure.profilingActive() {
		t.Fatal("a report without the profiling flag was not treated as profiling")
	}
	for body, want := range map[string]bool{`{"available_mb":30,"profiling":false}`: false, `{"available_mb":30,"profiling":true}`: true} {
		flagged, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeMemoryPressure}, []byte(body)...))
		if err != nil || flagged.MemoryPressure.profilingActive() != want {
			t.Fatalf("%s: profiling=%v err=%v", body, flagged.MemoryPressure.profilingActive(), err)
		}
	}
	if !strings.HasPrefix(msg.TypeLabel(), "memory_pressure") {
		t.Fatalf("label = %q", msg.TypeLabel())
	}
	if _, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeMemoryPressure}, []byte(strings.Repeat(" ", 300))...)); err == nil {
		t.Fatal("oversized memory pressure body accepted")
	}
	if _, err := parseFrameworkReadyDatagram([]byte{VsockFrameworkReadyHostTypeMemoryPressure, '{'}); err == nil {
		t.Fatal("malformed memory pressure body accepted")
	}
}
