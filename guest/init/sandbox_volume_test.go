package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// adr: 20261122132055 §4 — guest-init mounts drive 2 at /workspace only when
// it is an ext4 filesystem with the volume's label.
func TestExt4Label(t *testing.T) {
	image := func(magic uint16, label string) *bytes.Reader {
		b := make([]byte, 4096)
		binary.LittleEndian.PutUint16(b[1024+0x38:], magic)
		copy(b[1024+0x78:1024+0x88], label)
		return bytes.NewReader(b)
	}
	for _, tc := range []struct {
		name  string
		r     *bytes.Reader
		label string
		ok    bool
	}{
		{"volume", image(0xEF53, sandboxVolumeLabel), sandboxVolumeLabel, true},
		{"other ext4", image(0xEF53, "gregale-db"), "gregale-db", true},
		{"full-width label", image(0xEF53, "abcdefghijklmnop"), "abcdefghijklmnop", true},
		{"not ext4", image(0x1234, sandboxVolumeLabel), "", false},
		{"too short", bytes.NewReader(make([]byte, 1100)), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label, ok := ext4Label(tc.r)
			if label != tc.label || ok != tc.ok {
				t.Fatalf("ext4Label = %q %v, want %q %v", label, ok, tc.label, tc.ok)
			}
		})
	}
}
