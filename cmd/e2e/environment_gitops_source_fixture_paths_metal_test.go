//go:build metal && managed_service_native

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Native service acceptance publishes manifests outside the default fixture
// path; ordinary builds use gitarchive.Create through add/addFiles.
func (f *gitOpsSourceFixture) addAtPath(t *testing.T, sha string, definition api.EnvironmentDefinition, manifestPath string) {
	t.Helper()
	f.addAtPathWithFiles(t, sha, definition, manifestPath, nil)
}

func (f *gitOpsSourceFixture) addAtPathWithFiles(t *testing.T, sha string, definition api.EnvironmentDefinition, manifestPath string, files map[string][]byte) {
	t.Helper()
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "shop-" + sha + "/" + manifestPath, Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		file := files[path]
		if err := tw.WriteHeader(&tar.Header{Name: "shop-" + sha + "/" + path, Mode: 0o600, Size: int64(len(file))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.storeArchive(t, sha, archive.Bytes())
}
