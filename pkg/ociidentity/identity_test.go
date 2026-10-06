package ociidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResolveOCIUserAndGroup(t *testing.T) {
	image := fstest.MapFS{
		"etc/passwd": {Data: []byte("root:x:0:0::/:/bin/sh\nserver:x:1001:2001::/app:/bin/false\n")},
		"etc/group":  {Data: []byte("root:x:0:\nreaders:x:3001:server\n")},
	}
	fallback := Identity{UID: 1000, GID: 1000}
	for _, tc := range []struct {
		spec string
		want Identity
		fail bool
	}{
		{"server", Identity{1001, 2001}, false},
		{"1001", Identity{1001, 2001}, false},
		{"4001", Identity{4001, 4001}, false},
		{"server:readers", Identity{1001, 3001}, false},
		{"1001:readers", Identity{1001, 3001}, false},
		{"server:4001", Identity{1001, 4001}, false},
		{"1001:4001", Identity{1001, 4001}, false},
		{"0:4001", Identity{0, 4001}, false},
		{"root:root", Identity{0, 0}, false},
		{"unknown", fallback, false},
		{"app:4001", Identity{1000, 4001}, false},
		{"unknown:readers", Identity{}, true},
		{"server:unknown", Identity{}, true},
		{"server:", Identity{}, true},
		{":readers", Identity{}, true},
		{"server:readers:extra", Identity{}, true},
		{"65535", Identity{}, true},
		{"-1", Identity{}, true},
		{"1001:+1", Identity{}, true},
		{"1001:65535", Identity{}, true},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			got, err := Resolve(image, tc.spec, fallback)
			if (err != nil) != tc.fail {
				t.Fatalf("identity=%+v error=%v", got, err)
			}
			if !tc.fail && got != tc.want {
				t.Fatalf("identity=%+v want=%+v", got, tc.want)
			}
		})
	}
}

func TestResolveScratchNumericIdentity(t *testing.T) {
	got, err := Resolve(fstest.MapFS{}, "1001:2001", Identity{1000, 1000})
	if err != nil || got != (Identity{1001, 2001}) {
		t.Fatalf("identity=%+v error=%v", got, err)
	}
}

func TestResolveRejectsCorruptIdentityFiles(t *testing.T) {
	for _, tc := range []struct{ name, passwd, group, spec string }{
		{"bad UID", "server:x:invalid:2001:", "", "server"},
		{"bad GID", "server:x:1001:70000:", "", "server"},
		{"bad explicit group", "server:x:1001:2001:", "readers:x:invalid:", "server:readers"},
		{"oversized passwd", strings.Repeat("x", api.OCIIdentityFileMaxBytes+1), "", "1001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := fstest.MapFS{"etc/passwd": {Data: []byte(tc.passwd)}, "etc/group": {Data: []byte(tc.group)}}
			if _, err := Resolve(image, tc.spec, Identity{1000, 1000}); err == nil {
				t.Fatal("corrupt identity accepted")
			}
		})
	}
}

func TestResolveImageRootRejectsEscapingGroupSymlink(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "group"), []byte("readers:x:0:\n"), 0600); err != nil {
		t.Fatal(err)
	}
	image := t.TempDir()
	if err := os.Mkdir(filepath.Join(image, "etc"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "group"), filepath.Join(image, "etc", "group")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(image)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := Resolve(root.FS(), "1001:readers", Identity{1000, 1000}); err == nil {
		t.Fatal("group escaped companion image root")
	}
}
