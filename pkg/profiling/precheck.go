package profiling

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// pprof Profile message fields counted before parsing.
const (
	pprofFieldSample      = 2
	pprofFieldMapping     = 3
	pprofFieldLocation    = 4
	pprofFieldFunction    = 5
	pprofFieldStringTable = 6
)

// profileStringsMax bounds the string table: every function contributes a
// name, a system name and a file, plus labels and units.
const profileStringsMax = 4*api.ProfileMaxNodes + 1024

// precheckProfile walks the top level of an uncompressed pprof message and
// refuses one with more samples, mappings, locations, functions or strings
// than Parse would accept, before profile.ParseUncompressed builds the whole
// tree. A 12 KB gzip of 1.37M tiny samples once allocated about 268 MiB in
// the parser before the node limits ran (profiling review round 4, R4-1 and
// R4-2): the limits must apply before the allocation, not after.
func precheckProfile(raw []byte) error {
	var samples, mappings, locations, functions, strs int
	for len(raw) > 0 {
		tag, n := binary.Uvarint(raw)
		if n <= 0 {
			return errors.New("profile: malformed field tag")
		}
		raw = raw[n:]
		field, wire := tag>>3, tag&7
		switch wire {
		case 0: // varint
			_, m := binary.Uvarint(raw)
			if m <= 0 {
				return errors.New("profile: malformed varint")
			}
			raw = raw[m:]
		case 1: // 64-bit
			if len(raw) < 8 {
				return errors.New("profile: truncated fixed64")
			}
			raw = raw[8:]
		case 2: // length-delimited
			size, m := binary.Uvarint(raw)
			if m <= 0 || size > uint64(len(raw)-m) {
				return errors.New("profile: malformed length")
			}
			raw = raw[m+int(size):]
		case 5: // 32-bit
			if len(raw) < 4 {
				return errors.New("profile: truncated fixed32")
			}
			raw = raw[4:]
		default:
			return fmt.Errorf("profile: unsupported wire type %d", wire)
		}
		switch field {
		case pprofFieldSample:
			samples++
		case pprofFieldMapping:
			mappings++
		case pprofFieldLocation:
			locations++
		case pprofFieldFunction:
			functions++
		case pprofFieldStringTable:
			strs++
		}
		if samples > api.ProfileMaxNodes || mappings > api.ProfileMaxNodes || locations > api.ProfileMaxNodes ||
			functions > api.ProfileMaxNodes || strs > profileStringsMax {
			return errors.New("profile has too many nodes")
		}
	}
	return nil
}
