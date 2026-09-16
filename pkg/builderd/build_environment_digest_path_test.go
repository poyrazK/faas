package builderd

// Tests for the explicit digest-sidecar path (issue #2577).
//
// readBuildEnvironment used to derive the sidecar as builderBase + ".digest".
// That holds for the local backend, where the base is
// /srv/fc/base/runner-builder-<arch>.ext4 and the sidecar is genuinely its
// sibling. It does not hold for the OCI backend: builderd resolves the base
// through storage.LocalPathResolver into a read-through cache that is
// content-addressed, so the base lands at /var/lib/faas/cache/<aa>/<hash> and
// no sibling ".digest" exists there or ever will — the sidecar is a separate
// storage key. Every build on an OCI-backed node failed with
//
//	stat builder base digest sidecar:
//	  /var/lib/faas/cache/e6/572dbb....digest: no such file or directory
//
// reported as failure_class=user_error, though nothing about the user's source
// was at fault.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// validSidecar returns the canonical sidecar body and the identity a reader
// should derive from it.
func validSidecar() (body, identity, baseDigest string) {
	baseDigest = "sha256:" + strings.Repeat("a", sha256.Size*2)
	body = baseDigest + "\nfaas-base-layout-v3\nguest-init-sha256=" + strings.Repeat("b", sha256.Size*2)
	sum := sha256.Sum256([]byte(body))
	return body, "sha256:" + hex.EncodeToString(sum[:]), baseDigest
}

// cacheShapedBase mimics the OCI read-through cache: the base blob sits at a
// content-addressed path, and the sidecar is a DIFFERENT content-addressed
// blob — deliberately not a sibling.
func cacheShapedBase(t *testing.T) (basePath, digestPath, wantIdentity, wantBaseDigest string) {
	t.Helper()
	root := t.TempDir()
	body, identity, baseDigest := validSidecar()

	basePath = filepath.Join(root, "e6", "572dbb9f9583e55ea42860540900588f855592184da8a5631746f81198f0bf")
	digestPath = filepath.Join(root, "0f", "1ebe73854ff3a195f6cd858f53086fedcb8c671a4cd1729d28089c3013d231")
	for _, p := range []string{basePath, digestPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(basePath, []byte("ext4"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(digestPath, []byte(body+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return basePath, digestPath, identity, baseDigest
}

func TestReadBuildEnvironment_ExplicitDigestPathOutsideTheBaseDirectory(t *testing.T) {
	basePath, digestPath, wantIdentity, wantBaseDigest := cacheShapedBase(t)

	got, err := readBuildEnvironment(basePath, digestPath, "linux/amd64")
	if err != nil {
		t.Fatalf("readBuildEnvironment with an explicit sidecar path: %v", err)
	}
	if got.BuilderBaseIdentity != wantIdentity {
		t.Errorf("BuilderBaseIdentity = %q, want %q", got.BuilderBaseIdentity, wantIdentity)
	}
	if got.BaseDigest != wantBaseDigest {
		t.Errorf("BaseDigest = %q, want %q", got.BaseDigest, wantBaseDigest)
	}
	if got.TargetPlatform != "linux/amd64" {
		t.Errorf("TargetPlatform = %q, want linux/amd64", got.TargetPlatform)
	}
}

// The regression itself: with the same cache-shaped layout, deriving the
// sidecar from the base path must fail — proving the explicit path is doing
// the work, not an accidental sibling left in the fixture.
func TestReadBuildEnvironment_DerivedPathFailsOnACacheShapedBase(t *testing.T) {
	basePath, _, _, _ := cacheShapedBase(t)

	if _, err := readBuildEnvironment(basePath, "", "linux/amd64"); err == nil {
		t.Fatal("deriving the sidecar from a content-addressed base path succeeded; " +
			"the fixture is not reproducing the OCI cache layout")
	}
}

// The mtime ordering check exists to catch staging interrupted between
// publishing the base and publishing its sidecar. That inference is only valid
// when both files come from the same staging pass — i.e. siblings. In a
// read-through cache the mtimes record pull order, so an explicitly-located
// sidecar older than the base is normal and must be accepted.
func TestReadBuildEnvironment_ExplicitSidecarIgnoresMtimeOrdering(t *testing.T) {
	basePath, digestPath, _, _ := cacheShapedBase(t)

	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(digestPath, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := readBuildEnvironment(basePath, digestPath, "linux/amd64"); err != nil {
		t.Fatalf("explicit sidecar older than the base was rejected: %v; in a "+
			"read-through cache, blob mtimes reflect pull order, not publication order", err)
	}
}

// The sibling case must keep the ordering check: that is the local backend,
// where an older sidecar really does mean staging is in flight or was
// interrupted.
func TestReadBuildEnvironment_DerivedSidecarKeepsMtimeOrdering(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "runner-builder-amd64.ext4")
	body, _, _ := validSidecar()
	if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".digest", []byte(body+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(base+".digest", old, old); err != nil {
		t.Fatal(err)
	}

	_, err := readBuildEnvironment(base, "", "linux/amd64")
	if err == nil {
		t.Fatal("a sibling sidecar older than the base was accepted; that check " +
			"is what catches staging interrupted between the two writes")
	}
	if !strings.Contains(err.Error(), "predates") {
		t.Errorf("err = %v, want the sidecar-predates-base rejection", err)
	}
}

// ReadBuildEnvironment is the readiness path's entry point and must stay
// equivalent to passing an empty digest path.
func TestReadBuildEnvironment_ExportedWrappersAgree(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "runner-builder-amd64.ext4")
	body, _, _ := validSidecar()
	if err := os.WriteFile(base, []byte("ext4"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".digest", []byte(body+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	derived, err := ReadBuildEnvironment(base, "linux/amd64")
	if err != nil {
		t.Fatalf("ReadBuildEnvironment: %v", err)
	}
	explicit, err := ReadBuildEnvironmentAt(base, base+".digest", "linux/amd64")
	if err != nil {
		t.Fatalf("ReadBuildEnvironmentAt: %v", err)
	}
	if derived != explicit {
		t.Errorf("ReadBuildEnvironment = %+v, ReadBuildEnvironmentAt = %+v; "+
			"naming the sibling explicitly must not change the identity", derived, explicit)
	}
}
