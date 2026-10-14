package devpatch

import (
	"os"
	"slices"
	"testing"
)

// The install inputs of Railpack's real plans are the manifests and
// lockfiles their install steps copy, whether or not the build step runs a
// command over the source.
func TestRailpackInstallInputs(t *testing.T) {
	for fixture, want := range map[string][]string{
		"railpack-0.38.0-node-build.json": {".gregaleignore", "gregale.yaml", "package-lock.json", "package.json", "railpack.json"},
		"railpack-0.38.0-py-uv.json":      {".gregaleignore", "gregale.yaml", "pyproject.toml", "railpack.json", "uv.lock"},
		"railpack-0.38.0-py-req.json":     {".gregaleignore", "gregale.yaml", "railpack.json", "requirements.txt"},
	} {
		data, err := os.ReadFile("testdata/" + fixture)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RailpackInstallInputs(data)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("%s: inputs = %v, %v; want %v", fixture, got, err, want)
		}
	}
	if _, err := RailpackInstallInputs([]byte("{")); err == nil {
		t.Fatal("an unreadable plan had install inputs")
	}
}
