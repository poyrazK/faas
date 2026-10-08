package templates

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestDataAPIRPCSharedCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize("data-api-starter", dir); err != nil {
		t.Fatal(err)
	}
	for target, source := range sharedTemplateCopies["data-api-starter"] {
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
	if err := TarGz("data-api-starter", archive); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
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
		for target, source := range sharedTemplateCopies["data-api-starter"] {
			if header.Name != "data-api-starter/"+target {
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
	if len(seen) != len(sharedTemplateCopies["data-api-starter"]) {
		t.Fatal("archive missing shared catalog")
	}
}
