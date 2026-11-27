package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestJailTmpfsMetrics(t *testing.T) {
	cases := []struct {
		name string
		stat jailFSStat
		want string
	}{
		{
			name: "read",
			stat: func(path string) (uint64, uint64, bool) {
				if path != "/srv/fc/jail" {
					t.Fatalf("stat path = %q", path)
				}
				return 12 << 30, 10 << 30, true
			},
			want: `
# HELP vmmd_jail_tmpfs_avail_bytes Free bytes in the jailer chroot tmpfs (/srv/fc/jail) on this compute host.
# TYPE vmmd_jail_tmpfs_avail_bytes gauge
vmmd_jail_tmpfs_avail_bytes 1.073741824e+10
# HELP vmmd_jail_tmpfs_size_bytes Size of the jailer chroot tmpfs (/srv/fc/jail) on this compute host. It is RAM, held until the files are removed or the host reboots.
# TYPE vmmd_jail_tmpfs_size_bytes gauge
vmmd_jail_tmpfs_size_bytes 1.2884901888e+10
`,
		},
		// An unreadable jail exports nothing, never zero.
		{name: "unreadable", stat: func(string) (uint64, uint64, bool) { return 0, 0, false }},
		{name: "zero size", stat: func(string) (uint64, uint64, bool) { return 0, 0, true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			registerJailTmpfsMetrics(reg, "/srv/fc/jail", tc.stat)
			err := testutil.GatherAndCompare(reg, strings.NewReader(tc.want), "vmmd_jail_tmpfs_size_bytes", "vmmd_jail_tmpfs_avail_bytes")
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
