package templates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIRPCSharedCatalog(t *testing.T) {
	for _, name := range []string{"data-api-starter", "data-api-teams"} {
		t.Run(name, func(t *testing.T) { testDataAPISharedCatalog(t, name) })
	}
}

func testDataAPISharedCatalog(t *testing.T, name string) {
	dir := t.TempDir()
	if err := Materialize(name, dir); err != nil {
		t.Fatal(err)
	}
	for target, source := range sharedTemplateCopies[name] {
		want, err := FS.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, target))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("shared source mismatch: %s", target)
		}
	}
	archive := filepath.Join(t.TempDir(), "starter.tar.gz")
	if err := TarGz(name, archive); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		for target, source := range sharedTemplateCopies[name] {
			if header.Name != name+"/"+target {
				continue
			}
			got, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			want, err := FS.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("archive source mismatch: %s", target)
			}
			seen[target] = true
		}
	}
	if len(seen) != len(sharedTemplateCopies[name]) {
		t.Fatal("archive missing shared catalog")
	}
}
