package fcvm

// Addressed ingress for the idle ladder's activity guard
// (ADR-20261107122928 amendment 4).
//
// A paused guarded app resumes on traffic that reaches it outside the forward
// path. The guard used to read that from the host veth's tx bytes, which also
// count frames the bridge sends to every port: the host's ARP broadcasts and
// unicast it floods to an instance whose MAC aged out of the bridge while it
// was paused. On production those woke paused apps for nothing, about six
// resumes in seven.
//
// The instance's network namespace sees the same frames but counts only what
// was addressed to it: the kernel drops ARP and frames for another MAC
// (PACKET_OTHERHOST) before its IPv4 statistics, so Ip InReceives moves only
// on IPv4 packets for the namespace or the guest behind it. The guest network
// is IPv4 (ADR-009), so IPv6 is not counted: the IPv6 router and multicast
// listener chatter the bridge carries would count there.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// IdleLadderAddressedIngress is an optional IdleLadderNet extension: how many
// IPv4 packets reached an instance's network namespace addressed to it. ok is
// false when there is no fresh reading; the guard then falls back to the
// veth's ingress bytes.
type IdleLadderAddressedIngress interface {
	AddressedIngress(instance string) (packets uint64, ok bool)
}

// netnsIngressMaxAge bounds how old a reading may be when the guard uses it.
const netnsIngressMaxAge = 2 * time.Second

// netnsIngressRescanEvery bounds how often a poll scans the process table for
// instances whose Firecracker process it has not found (a VM still booting).
const netnsIngressRescanEvery = 5 * time.Second

// NetnsIngressReader reads each live instance's network namespace Ip
// InReceives through its Firecracker process (/proc/<pid>/net/snmp), which
// the jailer started inside the namespace. Poll reads; AddressedIngress
// serves the latest reading without I/O, for the guard's pass.
type NetnsIngressReader struct {
	procRoot string
	now      func() time.Time

	mu       sync.Mutex
	procs    map[string]netnsIngressProc
	readings map[string]netnsIngressReading
	lastScan time.Time
}

type netnsIngressProc struct {
	pid int
	// netns is the process's /proc/<pid>/ns/net link when it was found; a
	// reading through a pid whose link changed is refused.
	netns string
}

type netnsIngressReading struct {
	packets uint64
	at      time.Time
}

// NewNetnsIngressReader reads under procRoot (/proc in production).
func NewNetnsIngressReader(procRoot string) *NetnsIngressReader {
	return &NetnsIngressReader{
		procRoot: procRoot,
		now:      time.Now,
		procs:    map[string]netnsIngressProc{},
		readings: map[string]netnsIngressReading{},
	}
}

// Poll reads every instance in live (instance id → anything, such as
// Manager.SnapshotLive's veth map) and forgets the rest.
func (r *NetnsIngressReader) Poll(live map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	scanned := false
	for id := range live {
		proc, ok := r.procs[id]
		if !ok && !scanned && now.Sub(r.lastScan) >= netnsIngressRescanEvery {
			r.scan(now)
			scanned = true
			proc, ok = r.procs[id]
		}
		if !ok {
			delete(r.readings, id)
			continue
		}
		packets, err := r.read(proc)
		if err != nil {
			// The VM exited or was replaced: find it again on the next scan.
			delete(r.procs, id)
			delete(r.readings, id)
			continue
		}
		r.readings[id] = netnsIngressReading{packets: packets, at: now}
	}
	for id := range r.procs {
		if _, ok := live[id]; !ok {
			delete(r.procs, id)
		}
	}
	for id := range r.readings {
		if _, ok := live[id]; !ok {
			delete(r.readings, id)
		}
	}
}

// AddressedIngress serves the latest reading.
func (r *NetnsIngressReader) AddressedIngress(instance string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reading, ok := r.readings[instance]
	if !ok || r.now().Sub(reading.at) > netnsIngressMaxAge {
		return 0, false
	}
	return reading.packets, true
}

// scan finds the Firecracker process of every instance it does not know yet.
func (r *NetnsIngressReader) scan(now time.Time) {
	r.lastScan = now
	processes, err := discoverFirecrackerProcesses(r.procRoot)
	if err != nil {
		return
	}
	for id, p := range processes {
		if _, ok := r.procs[id]; ok {
			continue
		}
		link, err := os.Readlink(filepath.Join(r.procRoot, strconv.Itoa(p.pid), "ns", "net"))
		if err != nil {
			continue
		}
		r.procs[id] = netnsIngressProc{pid: p.pid, netns: link}
	}
}

func (r *NetnsIngressReader) read(p netnsIngressProc) (uint64, error) {
	dir := filepath.Join(r.procRoot, strconv.Itoa(p.pid))
	link, err := os.Readlink(filepath.Join(dir, "ns", "net"))
	if err != nil {
		return 0, err
	}
	if link != p.netns {
		return 0, fmt.Errorf("pid %d moved from %s to %s", p.pid, p.netns, link)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "net", "snmp"))
	if err != nil {
		return 0, err
	}
	return parseIPInReceives(raw)
}

// parseIPInReceives reads InReceives from /proc/net/snmp, whose "Ip:" lines
// are a header of field names followed by their values.
func parseIPInReceives(raw []byte) (uint64, error) {
	var header []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := bytes.Fields(sc.Bytes())
		if len(fields) == 0 || string(fields[0]) != "Ip:" {
			continue
		}
		if header == nil {
			header = make([]string, len(fields))
			for i, f := range fields {
				header[i] = string(f)
			}
			continue
		}
		for i, name := range header {
			if name == "InReceives" && i < len(fields) {
				return strconv.ParseUint(string(fields[i]), 10, 64)
			}
		}
		return 0, errors.New("snmp: no Ip InReceives field")
	}
	return 0, errors.New("snmp: no Ip lines")
}
