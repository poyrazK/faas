package main

import (
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// resolvePmemBaseDir applies ADR-908's gates to the configured pmem base
// directory. It returns the directory to hand to the VMM, or "" with the
// reason pmem is off. A host whose physical address width cannot be read
// keeps pmem, as before the width gate existed.
func resolvePmemBaseDir(configured, fcVersion string, nativeRecovery bool, readPhysBits func() (int, error), log *slog.Logger) (dir, reason string) {
	switch {
	case configured == "" || configured == "off":
		return "", ""
	case !fcvm.PmemBaseSupported(fcVersion):
		return "", fmt.Sprintf("firecracker %q has no virtio-pmem (needs 1.16+)", fcVersion)
	case nativeRecovery:
		return "", "native process recovery stages images through a journal with no pmem slot"
	}
	if readPhysBits == nil {
		readPhysBits = fcvm.HostPhysAddrBits
	}
	bits, err := readPhysBits()
	if err != nil {
		log.Warn("vmmd: host physical address width unknown; keeping pmem", "err", err)
		return configured, ""
	}
	if !fcvm.PmemPhysAddrSupported(bits) {
		return "", fmt.Sprintf("host has %d physical address bits; firecracker places pmem past its 512 GiB MMIO window and KVM needs at least %d",
			bits, fcvm.PmemMinPhysAddrBits)
	}
	return configured, ""
}
