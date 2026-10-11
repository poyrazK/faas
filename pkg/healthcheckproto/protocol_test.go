// adr:683
package healthcheckproto

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestHealthcheckProtocolBoundsAndVersion(t *testing.T) {
	request := Request{Nonce: strings.Repeat("a", 64), BudgetMS: 1000}
	if err := request.Validate(300000); err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	if err := Write(&frame, Probe, request); err != nil {
		t.Fatal(err)
	}
	var decoded Request
	if err := Read(bytes.NewReader(frame.Bytes()), Probe, &decoded); err != nil || decoded != request {
		t.Fatalf("roundtrip %+v %v", decoded, err)
	}
	for _, kind := range []uint32{Ack, 999} {
		if err := Read(bytes.NewReader(frame.Bytes()), kind, &decoded); err == nil {
			t.Fatal("accepted a wrong wire version")
		}
	}
	for _, size := range []uint32{0, MaxBody + 1} {
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], Probe)
		binary.BigEndian.PutUint32(header[4:], size)
		if err := Read(bytes.NewReader(header[:]), Probe, &decoded); err == nil {
			t.Fatal("accepted invalid frame size")
		}
	}
	if err := Read(bytes.NewReader(frame.Bytes()[:9]), Probe, &decoded); err == nil {
		t.Fatal("accepted truncated frame")
	}
	for _, bad := range []Request{{Nonce: "old-pass", BudgetMS: 1000}, {Nonce: request.Nonce, BudgetMS: 0}, {Nonce: request.Nonce, BudgetMS: 300001}} {
		if err := bad.Validate(300000); err == nil {
			t.Fatalf("accepted invalid request %+v", bad)
		}
	}
}

func TestHealthcheckProtocolRuntimeConfiguration(t *testing.T) {
	valid := Config{RuntimeID: "current-main", IntervalNS: 1250000, TimeoutNS: 2750000, Retries: 3}
	for _, change := range []func(*Config){func(c *Config) { c.RuntimeID = "" }, func(c *Config) { c.IntervalNS = 1 }, func(c *Config) { c.TimeoutNS = -1 }, func(c *Config) { c.Retries = 0 }, func(c *Config) { c.Error = "runtime_unavailable" }} {
		bad := valid
		change(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal("invalid runtime contract accepted")
		}
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	request := Request{Nonce: strings.Repeat("b", 64), BudgetMS: MaxProbeBudgetMS + 1}
	if err := request.Validate(MaxProbeBudgetMS); err == nil {
		t.Fatal("overflowing budget accepted")
	}
}
