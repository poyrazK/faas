// adr: 1027 — a moved database's volume arrives byte for byte, or not at all.
package dbvm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

const otherID = "1b4e28ba-2fa1-4d2e-883f-0016d3cca427"

// writeSparseVolume makes a volume of size with data at the given offsets.
func writeSparseVolume(t *testing.T, path string, size int64, extents map[int64][]byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	for off, data := range extents {
		if _, err := f.WriteAt(data, off); err != nil {
			t.Fatal(err)
		}
	}
}

// transfer copies id's volume from src to dst as the RPCs do, letting
// tamper change each extent in flight.
func transfer(t *testing.T, src, dst *Runtime, id string, tamper func(int64, []byte) []byte) error {
	t.Helper()
	ex, err := src.ExportVolume(id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ex.Close() }()
	im, err := dst.ImportVolume(id, ex.Size())
	if err != nil {
		t.Fatal(err)
	}
	for {
		off, data, err := ex.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			im.Abort()
			t.Fatal(err)
		}
		if tamper != nil {
			data = tamper(off, append([]byte(nil), data...))
		}
		if err := im.Write(off, data); err != nil {
			im.Abort()
			return err
		}
	}
	return im.Commit(ex.Sum())
}

func TestVolumeTransferCopiesDataAndSkipsZeros(t *testing.T) {
	src, dst := testRuntime(t, &fakeLeaser{}), testRuntime(t, &fakeLeaser{})
	if err := os.MkdirAll(src.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	const size = MinVolumeBytes
	head := bytes.Repeat([]byte("superblock"), 1000)
	tail := []byte("last page")
	writeSparseVolume(t, src.VolumePath(testID), size, map[int64][]byte{
		0:                       head,
		20 << 20:                make([]byte, 3<<20), // written zeros: skipped
		size - int64(len(tail)): tail,
	})
	if err := transfer(t, src, dst, testID, nil); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	want, _ := os.ReadFile(src.VolumePath(testID))
	got, err := os.ReadFile(dst.VolumePath(testID))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("imported volume differs (read err %v)", err)
	}
	if _, err := os.Stat(dst.VolumePath(testID) + ".import"); !os.IsNotExist(err) {
		t.Fatalf("import file left behind: %v", err)
	}
	if vols, _ := dst.ListVolumes(); len(vols) != 1 || vols[0].ID != testID {
		t.Fatalf("destination volumes = %+v", vols)
	}
}

func TestVolumeTransferRejectsCorruption(t *testing.T) {
	src, dst := testRuntime(t, &fakeLeaser{}), testRuntime(t, &fakeLeaser{})
	if err := os.MkdirAll(src.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSparseVolume(t, src.VolumePath(testID), MinVolumeBytes, map[int64][]byte{4096: []byte("row data")})
	err := transfer(t, src, dst, testID, func(_ int64, data []byte) []byte { data[0] ^= 1; return data })
	if !errors.Is(err, ErrTransferDigest) {
		t.Fatalf("corrupted transfer = %v, want ErrTransferDigest", err)
	}
	if _, err := os.Stat(dst.VolumePath(testID)); !os.IsNotExist(err) {
		t.Fatalf("a corrupted volume was installed: %v", err)
	}
	if err := transfer(t, src, dst, testID, nil); err != nil {
		t.Fatalf("retry after a failed transfer: %v", err)
	}
}

func TestVolumeTransferGuards(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	if err := os.MkdirAll(rt.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSparseVolume(t, rt.VolumePath(testID), MinVolumeBytes, nil)
	if _, err := rt.ExportVolume(otherID); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("export of a missing volume = %v", err)
	}
	if _, err := rt.ImportVolume(testID, MinVolumeBytes); !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("import over an existing volume = %v", err)
	}
	if _, err := rt.ImportVolume(otherID, 1<<10); err == nil {
		t.Fatal("import of a tiny volume accepted")
	}
	ex, err := rt.ExportVolume(testID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.reserve(testID); !errors.Is(err, ErrTransferring) {
		t.Fatalf("start during an export = %v, want ErrTransferring", err)
	}
	if _, err := rt.ExportVolume(testID); !errors.Is(err, ErrTransferring) {
		t.Fatalf("second export = %v, want ErrTransferring", err)
	}
	_ = ex.Close()
	if _, err := rt.reserve(testID); err != nil {
		t.Fatalf("start after the export = %v", err)
	}
	if _, err := rt.ExportVolume(testID); !errors.Is(err, ErrRunning) {
		t.Fatalf("export of a running database = %v, want ErrRunning", err)
	}
	im, err := rt.ImportVolume(otherID, MinVolumeBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := im.Write(8192, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := im.Write(4096, []byte("y")); err == nil {
		t.Fatal("out-of-order extent accepted")
	}
	im.Abort()
	if _, err := os.Stat(rt.VolumePath(otherID) + ".import"); !os.IsNotExist(err) {
		t.Fatalf("aborted import left a file: %v", err)
	}
}
