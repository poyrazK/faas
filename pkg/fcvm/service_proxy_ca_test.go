package fcvm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 244
func TestPreBootStagesServiceProxyCAOnlyForNetworkedGuests(t *testing.T) {
	ca := []byte("test-public-ca")
	writers, firstDigest, err := preBootFileWriters(nil, nil, nil, "10.100.0.1", false, ca)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, writer := range writers {
		if writer.path == serviceProxyCAPath {
			found = true
			if !bytes.Equal(writer.want, ca) || writer.mode != 0o444 {
				t.Fatalf("CA writer = %+v", writer)
			}
		}
	}
	if !found {
		t.Fatal("networked guest lacks service CA writer")
	}
	_, rotatedDigest, err := preBootFileWriters(nil, nil, nil, "10.100.0.1", false, []byte("rotated-public-ca"))
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == rotatedDigest {
		t.Fatal("CA rotation did not invalidate restore pre-boot digest")
	}
	writers, _, err = preBootFileWriters(nil, nil, nil, "", false, ca)
	if err != nil {
		t.Fatal(err)
	}
	for _, writer := range writers {
		if writer.path == serviceProxyCAPath {
			t.Fatal("networkless execution guest received service CA")
		}
	}
}

func TestWriteServiceProxyCAConfinedAndGuestReadable(t *testing.T) {
	for _, fullRootfs := range []bool{false, true} {
		root := t.TempDir()
		if fullRootfs {
			marker := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte(api.FullRootfsMarkerValue), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeServiceProxyCA(root, []byte("public-root")); err != nil {
			t.Fatal(err)
		}
		path, err := stagedDrivePath(root, serviceProxyCAPath)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o444 {
			t.Fatalf("guest CA mode = %#o", info.Mode().Perm())
		}
	}
	outside := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "upper/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "upper/etc/faas")); err != nil {
		t.Fatal(err)
	}
	if err := writeServiceProxyCA(root, []byte("public-root")); err == nil {
		t.Fatal("followed guest symlink outside drive")
	}
	if _, err := os.Stat(filepath.Join(outside, "service-proxy-ca.crt")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed: %v", err)
	}
}
