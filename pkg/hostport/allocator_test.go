package hostport

// adr: 177

import (
	"errors"
	"testing"
)

func TestAllocatorIsIdempotentAndProtocolAware(t *testing.T) {
	a := NewAllocator(31000, 31001)
	first, err := a.Acquire("node-a", "instance-a", []Request{
		{Name: "dns", Protocol: UDP, GuestPort: 53},
		{Name: "http", Protocol: TCP, GuestPort: 8080},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	second, err := a.Acquire("node-a", "instance-a", []Request{
		{Name: "http", Protocol: TCP, GuestPort: 8080},
		{Name: "dns", Protocol: UDP, GuestPort: 53},
	})
	if err != nil {
		t.Fatalf("idempotent Acquire: %v", err)
	}
	if len(first) != 2 || len(second) != 2 || first[0].HostPort != second[0].HostPort || first[1].HostPort != second[1].HostPort {
		t.Fatalf("idempotent leases differ: first=%+v second=%+v", first, second)
	}
	if first[0].HostPort != first[1].HostPort {
		t.Fatalf("TCP and UDP should be allowed to share a number: %+v", first)
	}
}

func TestAllocatorSpreadsNodesAndReusesReleasedPort(t *testing.T) {
	a := NewAllocator(32000, 32000)
	one, err := a.Acquire("node-a", "instance-a", []Request{{Protocol: TCP, GuestPort: 80}})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	two, err := a.Acquire("node-b", "instance-b", []Request{{Protocol: TCP, GuestPort: 80}})
	if err != nil {
		t.Fatalf("second node Acquire: %v", err)
	}
	if one[0].HostPort != two[0].HostPort {
		t.Fatalf("same port should be reusable on another node: one=%+v two=%+v", one, two)
	}
	a.Release("node-a", "instance-a")
	three, err := a.Acquire("node-a", "instance-c", []Request{{Protocol: TCP, GuestPort: 443}})
	if err != nil {
		t.Fatalf("reusing released port: %v", err)
	}
	if three[0].HostPort != one[0].HostPort {
		t.Fatalf("released port was not reused: got=%+v want=%+v", three, one)
	}
}

func TestAllocatorAcquireRollsBackOnExhaustion(t *testing.T) {
	a := NewAllocator(33000, 33000)
	_, err := a.Acquire("node-a", "instance-a", []Request{
		{Name: "first", Protocol: TCP, GuestPort: 80},
		{Name: "second", Protocol: TCP, GuestPort: 81},
	})
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("Acquire error=%v, want ErrExhausted", err)
	}
	if got := a.List("node-a", "instance-a"); len(got) != 0 {
		t.Fatalf("failed acquire leaked leases: %+v", got)
	}
}
