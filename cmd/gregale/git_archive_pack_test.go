package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPackGitArchive_RedactsAndExcludes(t *testing.T) {
	secret := "sk" + "_live_" + "XXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	inPath := writeGitArchiveFixture(t, map[string]string{
		".env":                    "STRIPE_SECRET_KEY=" + secret + "\n",
		".gregaleignore":          "ignored.txt\n",
		"config.txt":              "STRIPE_SECRET_KEY=" + secret + "\n",
		"ignored.txt":             "STRIPE_SECRET_KEY=" + secret + "\n",
		"node_modules/hidden.txt": "STRIPE_SECRET_KEY=" + secret + "\n",
		"tail.txt":                "tail survives\n",
	})

	outPath, count, findings, err := packGitArchive(inPath, defaultZeroConfigSourceCapMB, modeSourceTree)
	if err != nil {
		t.Fatalf("packGitArchive: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outPath) })
	if count != 4 {
		t.Fatalf("regular file count = %d, want 4 (ignored file and node_modules subtree omitted)", count)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(findings), findings)
	}

	entries := readGitArchiveFixture(t, outPath)
	for _, omitted := range []string{"ignored.txt", "node_modules/hidden.txt"} {
		if _, ok := entries[omitted]; ok {
			t.Errorf("filtered archive contains omitted entry %q", omitted)
		}
	}
	for name, body := range entries {
		if strings.Contains(string(body), secret) {
			t.Errorf("filtered archive contains raw secret in %q", name)
		}
	}
	if got := string(entries["tail.txt"]); got != "tail survives\n" {
		t.Errorf("tail.txt = %q, want original bytes", got)
	}
	if got := string(entries[".env"]); !strings.Contains(got, "<REDACTED") {
		t.Errorf(".env was not redacted: %q", got)
	}
	if got := string(entries["config.txt"]); !strings.Contains(got, "REDACTED secret detected") {
		t.Errorf("config.txt was not redacted: %q", got)
	}
}

func writeGitArchiveFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.tar.gz")
	f, err := os.Create(path) //nolint:forbidigo // test fixture creates its own archive
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("write header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	return path
}

func readGitArchiveFixture(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test reads the archive it just created
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()

	entries := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir || hdr.Typeflag == tar.TypeXHeader || hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %q: %v", hdr.Name, err)
		}
		entries[hdr.Name] = body
	}
}
