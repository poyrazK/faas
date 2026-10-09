package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackExcludesSecretFileAcrossSymlinkSpellings reproduces production
// hunt #8: `gregale start` excluded the guided secrets file by comparing
// paths lexically. On macOS the working directory resolves to
// /private/var/... while a typed absolute path stays /var/..., so the
// credentials file was uploaded inside the source archive. Any symlinked
// project directory on Linux behaves the same way.
func TestPackExcludesSecretFileAcrossSymlinkSpellings(t *testing.T) {
	const secret = "never-upload-this"
	for _, tc := range []struct {
		name string
		// srcVia and excludeVia choose which spelling of the project
		// directory the walk root and the excluded path use.
		srcVia, excludeVia string
		// excludeName is the path the customer typed, relative to the
		// project directory; storedName is where the secret bytes live.
		excludeName, storedName string
	}{
		{name: "source via link, secret via real path", srcVia: "link", excludeVia: "real", excludeName: "credentials.txt", storedName: "credentials.txt"},
		{name: "source via real path, secret via link", srcVia: "real", excludeVia: "link", excludeName: "credentials.txt", storedName: "credentials.txt"},
		{name: "secret file is an in-tree symlink", srcVia: "real", excludeVia: "real", excludeName: ".env", storedName: "config/prod.env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			real := filepath.Join(base, "real-app")
			link := filepath.Join(base, "linked-app")
			if err := os.MkdirAll(filepath.Join(real, "config"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(real, "package.json"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(real, tc.storedName), []byte("TOKEN="+secret+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.excludeName != tc.storedName {
				if err := os.Symlink(tc.storedName, filepath.Join(real, tc.excludeName)); err != nil {
					t.Fatal(err)
				}
			}
			dirs := map[string]string{"real": real, "link": link}
			archivePath := filepath.Join(t.TempDir(), "source.tar.gz")
			if _, err := packDirToTarGz(dirs[tc.srcVia], archivePath, defaultZeroConfigSourceCapMB, nil, filepath.Join(dirs[tc.excludeVia], tc.excludeName)); err != nil {
				t.Fatal(err)
			}
			entries := readPackedArchive(t, archivePath)
			if !packedArchiveHas(entries, "package.json") {
				t.Fatalf("archive is missing the project files: %v", entries)
			}
			for name, content := range entries {
				if strings.HasSuffix(name, tc.excludeName) || strings.HasSuffix(name, tc.storedName) || strings.Contains(content, secret) {
					t.Fatalf("secret file %q was included in the source archive as %q", tc.excludeName, name)
				}
			}
		})
	}
}

func packedArchiveHas(entries map[string]string, suffix string) bool {
	for name := range entries {
		if strings.HasSuffix(name, "/"+suffix) || name == suffix {
			return true
		}
	}
	return false
}

func readPackedArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := openCustomerFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	entries := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		header, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			return entries
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		entries[header.Name] = readTarEntry(t, tr)
	}
}

// TestStartSessionScopeIsSymlinkCanonical keeps one guided launch per
// directory: resuming through another spelling of the same directory (the
// shell's $PWD through a symlink, or macOS /var vs /private/var) must find the
// session instead of reporting it belongs to another directory.
func TestStartSessionScopeIsSymlinkCanonical(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	base := t.TempDir()
	real := filepath.Join(base, "real-app")
	link := filepath.Join(base, "linked-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	viaReal, err := startSessionPath(real)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := startSessionPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if viaReal != viaLink {
		t.Fatalf("session path differs by spelling: %s vs %s", viaReal, viaLink)
	}
	canonicalReal, err := canonicalStartScope(real)
	if err != nil {
		t.Fatal(err)
	}
	canonicalLink, err := canonicalStartScope(link)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalReal != canonicalLink {
		t.Fatalf("scope differs by spelling: %s vs %s", canonicalReal, canonicalLink)
	}
}
