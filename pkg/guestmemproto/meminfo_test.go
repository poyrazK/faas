package guestmemproto

import (
	"strings"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	const in = `MemTotal:        1009204 kB
MemFree:          812340 kB
MemAvailable:     900000 kB
Buffers:            2048 kB
Cached:           120000 kB
Shmem:              1024 kB
AnonPages:         60000 kB
Slab:              12000 kB
SReclaimable:       5000 kB
KernelStack:        1200 kB
PageTables:          800 kB
Broken line
Cached2:          bogus kB
HugePages_Total:       0
`
	got, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{
		MemTotal: 1009204 << 10, MemFree: 812340 << 10, Buffers: 2048 << 10,
		Cached: 120000 << 10, Shmem: 1024 << 10, AnonPages: 60000 << 10,
		Slab: 12000 << 10, SReclaimable: 5000 << 10, KernelStack: 1200 << 10,
		PageTables: 800 << 10,
	}
	if got != want {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
}
