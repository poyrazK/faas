package gitarchive_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/gitarchive"
)

func TestProviderArchiveFreezesDefinitionAndBuildFiles(t *testing.T) {
	sha := strings.Repeat("a", 40)
	definition := api.EnvironmentDefinition{Project: "shop", Environment: "production"}
	files := map[string][]byte{"jobs/report/commit.txt": []byte(sha), "jobs/report/Dockerfile": []byte("FROM scratch\n")}
	archive, err := gitarchive.Create(sha, definition, files)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := gitarchive.Create(sha, definition, files)
	if err != nil || !bytes.Equal(archive, repeated) {
		t.Fatal("provider archive bytes are not deterministic")
	}
	files["jobs/report/commit.txt"][0] = 'b'
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	got := map[string][]byte{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name, ok := strings.CutPrefix(header.Name, "shop-"+sha+"/")
		if !ok || header.Typeflag != tar.TypeReg || header.Mode != 0o644 {
			t.Fatalf("unsafe archive header: %+v", header)
		}
		got[name], err = io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	var reviewed api.EnvironmentDefinition
	if err := json.Unmarshal(got[gitarchive.ManifestPath], &reviewed); err != nil || reviewed.Project != "shop" || len(got) != 3 || string(got["jobs/report/commit.txt"]) != sha {
		t.Fatalf("definition and frozen build files lost: %v", err)
	}
}

func TestProviderArchiveRejectsEscapingAndManifestReplacementPaths(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "/absolute", "jobs/../../escape", "jobs\\escape", "jobs/\x00", gitarchive.ManifestPath} {
		if _, err := gitarchive.Create(strings.Repeat("a", 40), api.EnvironmentDefinition{}, map[string][]byte{name: []byte("untrusted")}); err == nil {
			t.Errorf("unsafe archive path accepted: %q", name)
		}
	}
	for _, sha := range []string{"main", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 39)} {
		if _, err := gitarchive.Create(sha, api.EnvironmentDefinition{}, nil); err == nil {
			t.Errorf("unresolved commit accepted: %q", sha)
		}
	}
}
