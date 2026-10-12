package main

import "testing"

// adr: 908 — a pmem base moves every virtio-blk drive down one letter.
func TestBlkDriveDevice(t *testing.T) {
	for _, tc := range []struct {
		cmdline string
		drive   int
		want    string
	}{
		{"console=ttyS0 root=/dev/vda ro init=/sbin/init", 1, "/dev/vdb"},
		{"console=ttyS0 root=/dev/vda ro init=/sbin/init", 2, "/dev/vdc"},
		{"console=ttyS0 root=/dev/pmem0 rootflags=dax ro", 1, "/dev/vda"},
		{"console=ttyS0 root=/dev/pmem0 rootflags=dax ro", 6, "/dev/vdf"},
		{"console=ttyS0 pmem=1 root=/dev/vda", 1, "/dev/vdb"},
	} {
		if got := blkDriveDevice(tc.drive, rootOnPmem(tc.cmdline)); got != tc.want {
			t.Errorf("%q drive %d = %s, want %s", tc.cmdline, tc.drive, got, tc.want)
		}
	}
}
