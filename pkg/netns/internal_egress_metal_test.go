//go:build metal

// adr: 20261114100000 — billing review R8-2: the guest's own traffic to
// Gregale-internal destinations is counted in faas_internal_tx so meterd can
// leave it out of billed egress. This drives real packets through the
// rendered ruleset.
package netns

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const internalEgressClient = `
import socket, sys
ip, port, n, size = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(2)
got = 0
for _ in range(n):
    s.sendto(b"x" * size, (ip, port))
    try:
        s.recvfrom(4096); got += 1
    except OSError:
        pass
print(got)
`

// internalCounter reads faas_internal_tx (packets, bytes) in the instance
// namespace.
func (topo *egressTopology) internalCounter() (packets, bytes uint64) {
	topo.t.Helper()
	out := runIn(topo.t, "ip", "netns", "exec", topo.inst, "nft", "list", "counter", "ip", "faas", EgressInternalTxCounter)
	fields := strings.Fields(out)
	for i := 0; i+1 < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[i] {
		case "packets":
			packets = v
		case "bytes":
			bytes = v
		}
	}
	return packets, bytes
}

func TestMetalInternalEgressCounted(t *testing.T) {
	topo := newEgressTopology(t, "iet", nil)

	p0, b0 := topo.internalCounter()
	if reply, ok := topo.try("tcp", "198.51.100.10", 443); !ok || reply != "ok" {
		t.Fatalf("TCP 443 to a public host must pass: ok=%v reply=%q", ok, reply)
	}
	if p, b := topo.internalCounter(); p != p0 || b != b0 {
		t.Fatalf("public egress counted as internal: packets %d->%d bytes %d->%d", p0, p, b0, b)
	}

	// 20 DNS-sized datagrams to the bridge resolver (10.100.0.1, RFC1918):
	// each is 1000 payload + 8 UDP + 20 IP bytes on the wire.
	const n, size = 20, 1000
	out, err := exec.Command("ip", "netns", "exec", topo.guest, "python3", "-c", internalEgressClient,
		"10.100.0.1", "53", strconv.Itoa(n), strconv.Itoa(size)).CombinedOutput()
	if err != nil {
		t.Fatalf("internal client: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != strconv.Itoa(n) {
		t.Fatalf("bridge resolver answered %s of %d datagrams; the egress policy must allow DNS to the bridge", got, n)
	}
	p1, b1 := topo.internalCounter()
	dp, db := p1-p0, b1-b0
	if dp != n || db != n*(size+28) {
		t.Fatalf("internal egress counter: +%d packets +%d bytes, want +%d packets +%d bytes", dp, db, n, n*(size+28))
	}

	// The value meterd subtracts is in veth terms: IP bytes plus Ethernet
	// framing per packet.
	counters := map[string]uint64{EgressInternalTxCounter: dp, EgressInternalTxCounter + CounterBytesSuffix: db}
	if got, want := InternalEgressBytes(counters), uint64(n*(size+28+EthernetHeaderBytes)); got != want {
		t.Fatalf("InternalEgressBytes = %d, want %d", got, want)
	}
	t.Logf("internal egress: %d packets, %d IP bytes, %d veth bytes", dp, db, InternalEgressBytes(counters))
}
