package main

import (
	"bytes"
	"encoding/binary"
	"io"
)

// adr: 20261122132055 §4 — a persistent session's /workspace is a volume
// vmmd attaches as drive 2, an ext4 filesystem labelled sandboxVolumeLabel.
// Any other session keeps /workspace on its root.

// sandboxVolumeLabel is fcvm.SandboxVolumeLabel.
const sandboxVolumeLabel = "gregale-ws"

// sandboxVolumeDrive is the volume's drive: after the base and the layer.
const sandboxVolumeDrive = 2

// ext4Label reads an ext4 superblock's volume name, and reports false for
// anything that is not ext4.
func ext4Label(r io.ReaderAt) (string, bool) {
	// The superblock starts at byte 1024: s_magic at 0x38, s_volume_name
	// (16 bytes) at 0x78.
	sb := make([]byte, 0x88)
	if _, err := r.ReadAt(sb, 1024); err != nil {
		return "", false
	}
	if binary.LittleEndian.Uint16(sb[0x38:]) != 0xEF53 {
		return "", false
	}
	name := sb[0x78:0x88]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return string(name), true
}
