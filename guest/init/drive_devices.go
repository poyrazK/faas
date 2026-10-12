package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Firecracker attaches drive0 (the shared base), drive1 (the app layer) and
// the sidecar drives as virtio-blk devices /dev/vda, /dev/vdb, ... in that
// order. When vmmd attaches the base as a virtio-pmem device instead
// (ADR-908), the kernel mounts it from /dev/pmem0 and every virtio-blk
// drive moves down one letter: the app layer is then /dev/vda.

// rootOnPmem reports whether a kernel command line mounts the root
// filesystem from a pmem device.
func rootOnPmem(cmdline string) bool {
	for _, field := range strings.Fields(cmdline) {
		if strings.HasPrefix(field, "root=/dev/pmem") {
			return true
		}
	}
	return false
}

// blkDriveDevice returns the device of drive n: 1 is the app layer and
// 2 onwards are sidecars.
func blkDriveDevice(drive int, pmemRoot bool) string {
	if pmemRoot {
		drive--
	}
	return fmt.Sprintf("/dev/vd%c", 'a'+drive)
}

var bootRootOnPmem = sync.OnceValue(func() bool {
	cmdline, err := os.ReadFile("/proc/cmdline")
	return err == nil && rootOnPmem(string(cmdline))
})

// layerDevice is the app layer's device on this boot.
func layerDevice() string { return blkDriveDevice(1, bootRootOnPmem()) }
