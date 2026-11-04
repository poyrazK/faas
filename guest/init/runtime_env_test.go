package main

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

// An image without a hosts file, or one without localhost, left `localhost`
// and the hostname unresolvable, so a HEALTHCHECK running
// `curl localhost:$PORT` failed (ADR-20261025111339).
func TestHostsWithGuestEntries(t *testing.T) {
	for _, tc := range []struct {
		name, hosts, want string
		changed           bool
	}{
		{"no hosts file", "", "127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n", true},
		{"complete file kept", "127.0.0.1 localhost\n10.0.0.2 gregale\n", "127.0.0.1 localhost\n10.0.0.2 gregale\n", false},
		{"baked entries kept", "10.1.2.3 db.internal\n", "10.1.2.3 db.internal\n127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n", true},
		{"missing final newline", "127.0.0.1 localhost", "127.0.0.1 localhost\n10.0.0.2\tgregale\n", true},
		{"alias on loopback counts", "127.0.1.1 gregale\n127.0.0.1 localhost.localdomain localhost\n", "127.0.1.1 gregale\n127.0.0.1 localhost.localdomain localhost\n", false},
		{"commented localhost ignored", "# 127.0.0.1 localhost\n", "# 127.0.0.1 localhost\n127.0.0.1\tlocalhost\n10.0.0.2\tgregale\n", true},
		{"ipv6 localhost alone adds ipv4", "::1 localhost\n10.0.0.2 gregale\n", "::1 localhost\n10.0.0.2 gregale\n127.0.0.1\tlocalhost\n", true},
	} {
		got, changed := hostsWithGuestEntries(tc.hosts, "gregale")
		if got != tc.want || changed != tc.changed {
			t.Errorf("%s: got %q changed=%v, want %q changed=%v", tc.name, got, changed, tc.want, tc.changed)
		}
	}
}

func TestHomeWritable(t *testing.T) {
	dir := func(perm fs.FileMode) fs.FileInfo {
		fsys := fstest.MapFS{"home": {Mode: fs.ModeDir | perm}}
		info, err := fs.Stat(fsys, "home")
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	file, err := fs.Stat(fstest.MapFS{"home": {Mode: 0o777}}, "home")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                         string
		info                         fs.FileInfo
		ownerUID, ownerGID, uid, gid uint32
		want                         bool
	}{
		{"owner", dir(0o755), 1000, 1000, 1000, 1000, true},
		{"owner without write", dir(0o555), 1000, 1000, 1000, 1000, false},
		{"root-owned home", dir(0o755), 0, 0, 1000, 1000, false},
		{"group writable", dir(0o775), 0, 1000, 1000, 1000, true},
		{"world writable", dir(0o1777), 0, 0, 1000, 1000, true},
		{"root writes anywhere", dir(0o555), 1000, 1000, 0, 0, true},
		{"not a directory", file, 1000, 1000, 1000, 1000, false},
	} {
		if got := homeWritable(tc.info, tc.ownerUID, tc.ownerGID, tc.uid, tc.gid); got != tc.want {
			t.Errorf("%s: homeWritable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWithDefaultPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"image PATH kept", []string{"PATH=/opt/bin"}, "/opt/bin"},
		{"unset gets Docker's default", []string{"HOME=/"}, defaultImagePath},
		{"empty gets Docker's default", []string{"PATH="}, defaultImagePath},
	} {
		got, count := "", 0
		for _, kv := range withDefaultPath(tc.env) {
			if k, v, ok := cut(kv); ok && k == "PATH" {
				got, count = v, count+1
			}
		}
		if got != tc.want || count != 1 {
			t.Errorf("%s: PATH = %q (%d entries), want %q once", tc.name, got, count, tc.want)
		}
	}
}
