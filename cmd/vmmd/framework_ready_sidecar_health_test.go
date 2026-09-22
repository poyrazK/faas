//go:build linux

package main

import (
	"encoding/json"
	"testing"
)

func TestParseFrameworkReadySidecarHealth(t *testing.T) {
	body, err := json.Marshal(sidecarHealthWire{
		Sidecar: "metrics", Status: sidecarHealthUnhealthy, Reason: "probe failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeSidecarHealth}, body...))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != parseFWReadyKindSidecarHealth {
		t.Fatalf("kind = %v, want sidecar health", msg.Kind)
	}
	if msg.SidecarHealth.Status != sidecarHealthUnhealthy || msg.SidecarHealth.Reason != "probe failed" {
		t.Fatalf("health = %+v", msg.SidecarHealth)
	}
}

func TestParseFrameworkReadySidecarHealthRejectsOversize(t *testing.T) {
	tooLarge := make([]byte, 513)
	if _, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeSidecarHealth}, tooLarge...)); err == nil {
		t.Fatal("expected oversized sidecar health body to be rejected")
	}
}
