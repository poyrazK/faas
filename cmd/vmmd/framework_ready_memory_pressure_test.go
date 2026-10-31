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
