//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestParseDiskTelemetryDatagram(t *testing.T) {
	body := []byte{VsockFrameworkReadyHostTypeDisk}
	body = append(body, `{"used_bytes":80,"capacity_bytes":100}`...)
	msg, err := parseFrameworkReadyDatagram(body)
	if err != nil {
		t.Fatalf("parse disk telemetry: %v", err)
	}
	if msg.Kind != parseFWReadyKindDisk || msg.Disk.UsedBytes != 80 || msg.Disk.CapacityBytes != 100 {
		t.Fatalf("disk message = %#v, want used=80 capacity=100", msg)
	}
	if !strings.Contains(msg.TypeLabel(), "disk_telemetry") {
		t.Fatalf("TypeLabel = %q, want disk_telemetry", msg.TypeLabel())
	}
}

func TestParseDiskTelemetryDatagramRejectsInvalidSample(t *testing.T) {
	for _, body := range [][]byte{
		append([]byte{VsockFrameworkReadyHostTypeDisk}, []byte(`{"used_bytes":101,"capacity_bytes":100}`)...),
		append([]byte{VsockFrameworkReadyHostTypeDisk}, []byte(`{"used_bytes":0,"capacity_bytes":0}`)...),
		append([]byte{VsockFrameworkReadyHostTypeDisk}, []byte(`{"used_bytes":-1,"capacity_bytes":100}`)...),
	} {
		if _, err := parseFrameworkReadyDatagram(body); err == nil || !strings.Contains(err.Error(), "disk_telemetry") {
			t.Fatalf("parse %#v err = %v, want disk_telemetry error", body, err)
		}
	}
}
