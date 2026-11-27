//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestParseResourceSlotFrame(t *testing.T) {
	body := `{"samples":10,"mem_start_mb":100,"mem_peak_mb":300,"mem_end_mb":200,"cpu_usec":5000,"cpu_peak_millicores":800,"peak_age_ms":1200}`
	msg, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeResourceSlot}, body...))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Kind != parseFWReadyKindResourceSlot || msg.ResourceSlot.MemPeakMB != 300 || msg.ResourceSlot.PeakAgeMS != 1200 {
		t.Fatalf("msg = %+v", msg)
	}
	for name, bad := range map[string]string{
		"invalid values": `{"samples":10,"mem_start_mb":300,"mem_peak_mb":100,"mem_end_mb":100}`,
		"not json":       `{`,
		"too large":      `{"samples":10,"pad":"` + strings.Repeat("x", 300) + `"}`,
	} {
		if _, err := parseFrameworkReadyDatagram(append([]byte{VsockFrameworkReadyHostTypeResourceSlot}, bad...)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
