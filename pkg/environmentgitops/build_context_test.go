package environmentgitops_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/tarball"
)

func contextArchive(t *testing.T, wrapper, code, shared, configuration string, mode int64, reverse bool) []byte {
	t.Helper()
	headers := []*tar.Header{
		{Name: wrapper + "/workspace/api/package.json", Typeflag: tar.TypeReg, Mode: mode},
		{Name: wrapper + "/workspace/lock.json", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: wrapper + "/workspace/shared/lib.js", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: wrapper + "/environments/production.yaml", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	bodies := []string{code, "dependency integrity", shared, configuration}
	if reverse {
		for i, j := 0, len(headers)-1; i < j; i, j = i+1, j-1 {
			headers[i], headers[j] = headers[j], headers[i]
			bodies[i], bodies[j] = bodies[j], bodies[i]
		}
		for _, h := range headers {
			h.ModTime = time.Unix(99999, 0)
			h.Uid, h.Gid = 123, 456
		}
	}
	return gitArchive(t, headers, bodies)
}

func canonicalContext(t *testing.T, archive []byte, context string) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := environmentgitops.CanonicalBuildContext(bytes.NewReader(archive), &out, context, 1<<20); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestCanonicalBuildContextEquivalenceAndIsolation(t *testing.T) {
	a := contextArchive(t, "github-commit-a", "app", "shared", "old-config", 0o644, false)
	b := contextArchive(t, "gitlab-commit-b", "app", "shared", "new-config", 0o644, true)
	canonical := canonicalContext(t, a, "workspace")
	if !bytes.Equal(canonical, canonicalContext(t, b, "workspace")) {
		t.Fatal("transport order/ownership/time/wrapper or excluded configuration changed delivered bytes")
	}
	if bytes.Equal(canonicalContext(t, a, "."), canonicalContext(t, b, ".")) {
		t.Fatal("whole-repository context omitted configuration")
	}
	zr, err := gzip.NewReader(bytes.NewReader(canonical))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = string(data)
	}
	if len(files) != 3 || files["gregale-source/workspace/api/package.json"] != "app" || files["gregale-source/workspace/shared/lib.js"] != "shared" || files["gregale-source/workspace/lock.json"] != "dependency integrity" {
		t.Fatalf("delivered context changed or leaked excluded files: %+v", files)
	}
	for _, test := range []struct {
		name, code, shared string
		mode               int64
	}{
		{"code", "changed", "shared", 0o644}, {"shared package", "app", "changed", 0o644}, {"execute mode", "app", "shared", 0o755},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := contextArchive(t, "commit-c", test.code, test.shared, "old-config", test.mode, false)
			if bytes.Equal(canonical, canonicalContext(t, changed, "workspace")) {
				t.Fatal("build input change reused fingerprint")
			}
		})
	}
}

func TestCanonicalBuildContextRejectsAmbiguityAndCorruption(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []*tar.Header
	}{
		{"traversal", []*tar.Header{{Name: "repo/workspace/../escape", Typeflag: tar.TypeReg}}},
		{"duplicate", []*tar.Header{{Name: "repo/workspace/a", Typeflag: tar.TypeReg}, {Name: "repo/workspace/a", Typeflag: tar.TypeReg}}},
		{"different roots", []*tar.Header{{Name: "repo/workspace/a", Typeflag: tar.TypeReg}, {Name: "other/workspace/b", Typeflag: tar.TypeReg}}},
		{"symlink", []*tar.Header{{Name: "repo/workspace/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}}},
		{"hardlink", []*tar.Header{{Name: "repo/workspace/link", Typeflag: tar.TypeLink, Linkname: "repo/outside"}}},
		{"device", []*tar.Header{{Name: "repo/workspace/device", Typeflag: tar.TypeChar}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make([]string, len(tc.headers))
			archive := gitArchive(t, tc.headers, bodies)
			if err := environmentgitops.CanonicalBuildContext(bytes.NewReader(archive), io.Discard, "workspace", 1<<20); err == nil {
				t.Fatal("unsafe context accepted")
			}
		})
	}
	good := contextArchive(t, "repo", "app", "shared", "config", 0o644, false)
	corrupt := bytes.Clone(good)
	corrupt[len(corrupt)-5] ^= 0xff
	for _, archive := range [][]byte{good[:len(good)-3], corrupt} {
		if err := environmentgitops.CanonicalBuildContext(bytes.NewReader(archive), io.Discard, "workspace", 1<<20); err == nil {
			t.Fatal("gzip checksum/truncation accepted")
		}
	}
	for _, context := range []string{"../escape", "workspace/missing", "workspace/api/../shared", strings.Repeat("x", 257)} {
		if err := environmentgitops.CanonicalBuildContext(bytes.NewReader(good), io.Discard, context, 1<<20); err == nil {
			t.Fatalf("invalid context %q accepted", context)
		}
	}
	if err := environmentgitops.CanonicalBuildContext(bytes.NewReader(good), io.Discard, "workspace", int64(len(good)-1)); err == nil {
		t.Fatal("compressed limit ignored")
	}
}

func TestCanonicalBuildContextWrapperCannotSelectAnotherMember(t *testing.T) {
	input := gitArchive(t, []*tar.Header{
		{Name: "repo/gregale-source/api/package.json", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "repo/api/package.json", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "repo/gregale-source-1/other", Typeflag: tar.TypeReg, Mode: 0o644},
	}, []string{"reviewed member", "wrong member", "other"})
	canonical := canonicalContext(t, input, ".")
	name := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(name, canonical, 0o640); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"gregale-source", "gregale-source/api", "api"} {
		resolved, err := tarball.ResolveSourceRoot(name, root)
		if err != nil || resolved != "gregale-source-2/"+root {
			t.Fatalf("root %s selected %s: %v", root, resolved, err)
		}
	}
}
