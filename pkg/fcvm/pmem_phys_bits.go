package fcvm

// Physical address width gate for virtio-pmem (ADR-908).
//
// Firecracker maps a pmem backing file into guest physical memory past its
// 64-bit MMIO window, which ends at 512 GiB (2^39). KVM refuses a memory
// region above the host CPU's MAXPHYADDR, so on a host with 39 or fewer
// physical address bits every boot that carries a pmem device fails with
// "Cannot create pmem device: Cannot set the memory regions: Invalid argument
// (os error 22)". The OVH compute nodes (Intel Xeon E-2388G, 39 bits) hit it
// on every cold boot of an app with a pmem base. Any other device Firecracker
// places past the 64-bit MMIO window needs the same width.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PmemMinPhysAddrBits is the physical address width pmem needs: the pmem
// window starts at 2^39 and must still fit under 2^PmemMinPhysAddrBits.
const PmemMinPhysAddrBits = 40

// hostCPUInfoPath lists the host CPUs, one "address sizes" line each on x86.
const hostCPUInfoPath = "/proc/cpuinfo"

// HostPhysAddrBits returns the host's physical address width in bits, as
// /proc/cpuinfo reports it.
func HostPhysAddrBits() (int, error) {
	return hostPhysAddrBitsAt(hostCPUInfoPath)
}

// hostPhysAddrBitsAt reads the width from a cpuinfo file. When CPUs disagree
// it returns the smallest, the one a guest may land on.
func hostPhysAddrBitsAt(path string) (int, error) {
	f, err := os.Open(path) //nolint:forbidigo // kernel CPU table; read only.
	if err != nil {
		return 0, fmt.Errorf("read host physical address bits: %w", err)
	}
	defer func() { _ = f.Close() }()
	bits := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(key) != "address sizes" {
			continue
		}
		n, err := parsePhysBits(val)
		if err != nil {
			return 0, fmt.Errorf("read host physical address bits: %w", err)
		}
		if bits == 0 || n < bits {
			bits = n
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read host physical address bits: %w", err)
	}
	if bits == 0 {
		return 0, fmt.Errorf("read host physical address bits: no address sizes line in %s", path)
	}
	return bits, nil
}

// parsePhysBits reads "39 bits physical, 48 bits virtual".
func parsePhysBits(val string) (int, error) {
	for _, part := range strings.Split(val, ",") {
		f := strings.Fields(part)
		if len(f) == 3 && f[1] == "bits" && f[2] == "physical" {
			n, err := strconv.Atoi(f[0])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad physical address width %q", strings.TrimSpace(part))
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("no physical address width in %q", strings.TrimSpace(val))
}

// PmemPhysAddrSupported reports whether a host with the given physical
// address width can map a pmem device.
func PmemPhysAddrSupported(bits int) bool {
	return bits >= PmemMinPhysAddrBits
}
