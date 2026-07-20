//go:build metal

// fixtures_meta_test.go — sanity checks on the fixture tarballs that don't
// need the full harness. The real validation lives in
// cmd/apid/deploy_inputs.go::validateTarballShape and runs when the e2e
// test POSTs these fixtures to /v1/apps/<slug>/deployments. Here we just
// assert the tarballs are well-formed gzip+tar and contain the expected
// top-level files (so a typo in a fixture path catches at go-test time,
// not deep inside the e2e harness).

package e2e_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"testing"
)

// assertFixtureContains reads tarBytes and asserts it has at least the
// names in wantNames. Helps the fixture builder's "did I typo this path"
// guard rail — without it, a wrong filename only surfaces when the in-VM
// build fails on missing files, which is expensive to debug.
func assertFixtureContains(t *testing.T, tarBytes []byte, wantNames ...string) {
	t.Helper()
	gz, err := gzip.NewReader(bytesReader(tarBytes))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	got := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		got[hdr.Name] = true
	}
	for _, n := range wantNames {
		if !got[n] {
			t.Errorf("fixture missing %q (got %v)", n, mapKeys(got))
		}
	}
}

func TestNodeFixture_Shape(t *testing.T) {
	assertFixtureContains(t, NodeFixture(t),
		"package.json", "index.js", "faas-build-token")
}

func TestPythonFixture_Shape(t *testing.T) {
	assertFixtureContains(t, PythonFixture(t),
		"requirements.txt", "app.py", "faas-build-token")
}

func TestDockerfileFixture_Shape(t *testing.T) {
	assertFixtureContains(t, DockerfileFixture(t),
		"Dockerfile", "faas-build-token")
}

// bytesReader + mapKeys are tiny helpers to keep the test above readable.
func bytesReader(b []byte) *byteReader { return &byteReader{b: b} }

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
