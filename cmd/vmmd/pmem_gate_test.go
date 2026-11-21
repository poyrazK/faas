package main

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

// adr: 908 — pmem needs Firecracker 1.16+, no native recovery, and a host
// with at least 40 physical address bits; the reason says which gate closed.
func TestResolvePmemBaseDir(t *testing.T) {
	bits := func(n int) func() (int, error) { return func() (int, error) { return n, nil } }
	for _, tc := range []struct {
		name       string
		configured string
		fcVersion  string
		native     bool
		physBits   func() (int, error)
		wantDir    string
		wantReason string
	}{
		{name: "auto on a wide host", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.17.0", physBits: bits(46), wantDir: fcvm.PmemBaseDirAuto},
		{name: "fixed dir kept", configured: "/srv/fc/pmem", fcVersion: "1.16.0", physBits: bits(40), wantDir: "/srv/fc/pmem"},
		{name: "39-bit host (OVH Xeon E-2388G)", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.17.0", physBits: bits(39), wantReason: "39 physical address bits"},
		{name: "36-bit host", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.17.0", physBits: bits(36), wantReason: "36 physical address bits"},
		{name: "width unreadable keeps pmem", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.17.0",
			physBits: func() (int, error) { return 0, errors.New("no cpuinfo") }, wantDir: fcvm.PmemBaseDirAuto},
		{name: "off", configured: "off", fcVersion: "1.17.0", physBits: bits(39)},
		{name: "empty", configured: "", fcVersion: "1.17.0", physBits: bits(39)},
		{name: "old firecracker", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.15.2", physBits: bits(46), wantReason: "no virtio-pmem"},
		{name: "undetected firecracker", configured: fcvm.PmemBaseDirAuto, fcVersion: "", physBits: bits(46), wantReason: "no virtio-pmem"},
		{name: "native recovery", configured: fcvm.PmemBaseDirAuto, fcVersion: "1.17.0", native: true, physBits: bits(46), wantReason: "native process recovery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, reason := resolvePmemBaseDir(tc.configured, tc.fcVersion, tc.native, tc.physBits, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if dir != tc.wantDir {
				t.Errorf("dir = %q, want %q", dir, tc.wantDir)
			}
			if tc.wantReason == "" && reason != "" || !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", reason, tc.wantReason)
			}
		})
	}
}
