package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 908 — the width comes from /proc/cpuinfo's "address sizes" lines, and
// the smallest line wins when CPUs disagree.
func TestHostPhysAddrBitsAt(t *testing.T) {
	const xeonE2388G = "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: 11th Gen Intel(R) Xeon(R) E-2388G\n" +
		"address sizes\t: 39 bits physical, 48 bits virtual\npower management:\n\n"
	for _, tc := range []struct {
		name    string
		cpuinfo string
		want    int
		wantErr string
	}{
		{name: "OVH Xeon E-2388G", cpuinfo: xeonE2388G, want: 39},
		{name: "wide server CPU", cpuinfo: "address sizes\t: 46 bits physical, 57 bits virtual\n", want: 46},
		{name: "one line per CPU", cpuinfo: xeonE2388G + strings.ReplaceAll(xeonE2388G, "processor\t: 0", "processor\t: 1"), want: 39},
		{name: "smallest of mixed CPUs", cpuinfo: "address sizes : 48 bits physical, 48 bits virtual\naddress sizes : 40 bits physical, 48 bits virtual\n", want: 40},
		{name: "virtual width listed first", cpuinfo: "address sizes\t: 48 bits virtual, 36 bits physical\n", want: 36},
		{name: "no address sizes line", cpuinfo: "processor\t: 0\nflags\t\t: fpu\n", wantErr: "no address sizes line"},
		{name: "no physical width", cpuinfo: "address sizes\t: 48 bits virtual\n", wantErr: "no physical address width"},
		{name: "unparsable width", cpuinfo: "address sizes\t: x bits physical\n", wantErr: "bad physical address width"},
		{name: "empty file", cpuinfo: "", wantErr: "no address sizes line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cpuinfo")
			if err := os.WriteFile(path, []byte(tc.cpuinfo), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := hostPhysAddrBitsAt(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("hostPhysAddrBitsAt = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	if _, err := hostPhysAddrBitsAt(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("an unreadable cpuinfo must be an error, not a guess")
	}
}

// adr: 908 — pmem sits past 2^39, so it needs at least 40 physical bits.
func TestPmemPhysAddrSupported(t *testing.T) {
	for bits, want := range map[int]bool{0: false, 36: false, 39: false, 40: true, 46: true, 52: true} {
		if got := PmemPhysAddrSupported(bits); got != want {
			t.Errorf("PmemPhysAddrSupported(%d) = %v, want %v", bits, got, want)
		}
	}
}
