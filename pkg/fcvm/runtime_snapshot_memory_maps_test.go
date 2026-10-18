package fcvm

// adr: 595

import (
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotMemoryMapsRequireExactPrivateBacking(t *testing.T) {
	const first = "10000-12000 rw-p 00000000 08:01 42 /mem"
	const second = "20000-22000 rw-p 00002000 08:01 42 /mem"
	file := snapshotMemoryFileIdentity{major: 8, minor: 1, inode: 42, bytes: 0x4000}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"split memory hole", first + "\n" + second, true},
		{"out of file order", second + "\n" + first, true},
		{"unrelated private and anonymous mappings", "30000-31000 rw-p 0 08:01 43 /other\n40000-41000 rw-p 0 00:00 0\n" + first + "\n" + second, true},
		{"shared", strings.Replace(first, "rw-p", "rw-s", 1) + "\n" + second, false},
		{"read only", strings.Replace(first, "rw-p", "r--p", 1) + "\n" + second, false},
		{"executable", strings.Replace(first, "rw-p", "rwxp", 1) + "\n" + second, false},
		{"wrong inode", strings.ReplaceAll(first+"\n"+second, " 42 ", " 43 "), false},
		{"wrong device", strings.ReplaceAll(first+"\n"+second, "08:01", "08:02"), false},
		{"missing tail", first, false},
		{"missing head", second, false},
		{"duplicate alias", first + "\n" + second + "\n30000-34000 rw-p 0 08:01 42 /mem", false},
		{"overlapping file bytes", first + "\n20000-22000 rw-p 1000 08:01 42 /mem", false},
		{"overlapping virtual ranges", first + "\n11000-13000 rw-p 2000 08:01 42 /mem", false},
		{"beyond EOF", first + "\n20000-24000 rw-p 2000 08:01 42 /mem", false},
		{"unaligned range", "10001-12001 rw-p 0 08:01 42 /mem\n" + second, false},
		{"offset overflow", "10000-14000 rw-p ffffffffffffffff 08:01 42 /mem", false},
		{"address overflow", "ffffffffffff0000-ffffffffffff4000 rw-p 0 08:01 42 /mem", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranges, err := verifySnapshotMemoryMaps([]byte(tc.body), file, 4096)
			if (err == nil) != tc.valid {
				t.Fatalf("mapping admission: ranges=%+v error=%v, valid=%t", ranges, err, tc.valid)
			}
			if tc.valid && !reflect.DeepEqual(ranges, []RuntimeSnapshotMemoryRange{{Start: 0x10000, End: 0x12000}, {Start: 0x20000, End: 0x22000, Offset: 0x2000}}) {
				t.Fatalf("file coverage lost mapping identity: %+v", ranges)
			}
		})
	}
}
