package gitfetch

// adr: 20261029103512 — repository links become copies of their in-tree
// targets; nothing outside the tree is ever read.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type linkEntry struct {
	name, body, link string
	hard, dir        bool
}

func buildLinkArchive(t *testing.T, entries []linkEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			hdr = &tar.Header{Name: e.name, Mode: 0o755, Typeflag: tar.TypeDir}
		case e.hard:
			hdr = &tar.Header{Name: e.name, Linkname: e.link, Mode: 0o644, Typeflag: tar.TypeLink}
		case e.link != "":
			hdr = &tar.Header{Name: e.name, Linkname: e.link, Mode: 0o777, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatalf("tar write: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func extractLinks(t *testing.T, entries []linkEntry, maxTotal int64, lim extractLimit) (string, error) {
	t.Helper()
	dst := t.TempDir()
	return dst, extractStream(t.Context(), dst, bytes.NewReader(buildLinkArchive(t, entries)), maxTotal, lim)
}

func TestExtractStreamCopiesInTreeLinks(t *testing.T) {
	dst, err := extractLinks(t, []linkEntry{
		{name: "o-sha/", dir: true},
		{name: "o-sha/LICENSE", body: "MIT"},
		{name: "o-sha/AGENTS.md", body: "rules"},
		{name: "o-sha/CLAUDE.md", link: "AGENTS.md"},
		{name: "o-sha/shared/util.js", body: "util"},
		{name: "o-sha/shared/nested", link: "../LICENSE"},
		{name: "o-sha/packages/api/LICENSE", link: "../../LICENSE"},
		{name: "o-sha/packages/api/shared", link: "../../shared"},
		{name: "o-sha/packages/api/via", link: "shared/util.js"},
		{name: "o-sha/hard.txt", link: "o-sha/LICENSE", hard: true},
		{name: "o-sha/dangling", link: "missing"},
		{name: "o-sha/loop-a", link: "loop-b"},
		{name: "o-sha/loop-b", link: "loop-a"},
		{name: "o-sha/self", link: "."},
	}, 0, defaultExtractLimits())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := map[string]string{
		"CLAUDE.md":                   "rules",
		"packages/api/LICENSE":        "MIT",
		"packages/api/shared/util.js": "util",
		"packages/api/shared/nested":  "MIT",
		"packages/api/via":            "util",
		"hard.txt":                    "MIT",
	}
	for name, body := range want {
		info, err := os.Lstat(filepath.Join(dst, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s: info=%v err=%v, want a regular file", name, info, err)
		}
		got, _ := os.ReadFile(filepath.Join(dst, name))
		if string(got) != body {
			t.Errorf("%s = %q, want %q", name, got, body)
		}
	}
	for _, name := range []string{"dangling", "loop-a", "loop-b", "self"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: err = %v, want left out", name, err)
		}
	}
	_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("%s is a symlink; extraction must never create one", p)
		}
		return err
	})
}

func TestExtractStreamLinkCopiesCountAgainstTheCaps(t *testing.T) {
	big := strings.Repeat("x", 600)
	_, err := extractLinks(t, []linkEntry{
		{name: "o-sha/blob", body: big},
		{name: "o-sha/copy", link: "blob"},
	}, 1000, defaultExtractLimits())
	if !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("byte cap: err = %v, want ErrArchiveTooLarge", err)
	}
	_, err = extractLinks(t, []linkEntry{
		{name: "o-sha/dir/a", body: "a"},
		{name: "o-sha/dir/b", body: "b"},
		{name: "o-sha/copy", link: "dir"},
	}, 0, extractLimit{MaxEntries: 4, MaxFileBytes: 1 << 20})
	if !errors.Is(err, ErrBadArchive) {
		t.Fatalf("entry cap: err = %v, want ErrBadArchive", err)
	}
}

// Two links per level to the level below doubled the copied tree at each
// level, and only files counted: 44 archive entries made 65,520 directories,
// and a few levels more would exhaust the control plane's disk (bug hunt
// 2026-10-28 B39). Directories now count against the entry cap, and a
// cancelled fetch stops the copy.
func TestExtractStreamLinkFanOutIsBounded(t *testing.T) {
	fanOut := []linkEntry{{name: "o-sha/l0/f", body: "x"}}
	for level := 1; level <= 14; level++ {
		below := "../l" + strconv.Itoa(level-1)
		dir := "o-sha/l" + strconv.Itoa(level)
		fanOut = append(fanOut, linkEntry{name: dir + "/a", link: below}, linkEntry{name: dir + "/b", link: below})
	}
	started := time.Now()
	dst, err := extractLinks(t, fanOut, 0, defaultExtractLimits())
	if !errors.Is(err, ErrBadArchive) {
		t.Fatalf("fan-out archive: err = %v, want ErrBadArchive", err)
	}
	created := 0
	_ = filepath.WalkDir(dst, func(string, fs.DirEntry, error) error { created++; return nil })
	if limit := defaultExtractLimits().MaxEntries + 64; created > limit {
		t.Fatalf("fan-out archive created %d entries, want at most %d", created, limit)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("fan-out archive took %s", elapsed)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = extractStream(ctx, t.TempDir(), bytes.NewReader(buildLinkArchive(t, fanOut[:5])), 0, defaultExtractLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fetch: err = %v, want context.Canceled", err)
	}
}
