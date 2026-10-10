//go:build linux

// adr: 568 — local observations require original bytes and never authorize unlink.
package storage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLocalExclusiveReceiptObservesOriginalAllocatedBytes(t *testing.T) {
	b := exclusiveLocalFixture(t)
	body := make([]byte, 4<<20)
	copy(body, "original-sparse-capture")
	body[len(body)-1] = 42
	r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"mem", bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(filepath.Join(b.root, r.ObjectKey), &stat); err != nil {
		t.Fatal(err)
	}
	if r.Local == nil || r.Local.Inode != stat.Ino || r.Local.Device != uint64(stat.Dev) || r.StoredBytes != stat.Blocks*512 || r.StoredBytes >= r.LogicalBytes/10 {
		t.Fatal("receipt did not describe original sparse output", r, stat)
	}
	reader, err := GetExclusiveArtifact(t.Context(), b, r)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(reader)
	if err := errors.Join(err, reader.Close()); err != nil || !bytes.Equal(actual, body) {
		t.Fatal("receipt did not verify original descriptor", err)
	}
	if err := RetireExclusiveArtifact(t.Context(), b, r); !errors.Is(err, ErrExclusiveRetireUnsupported) {
		t.Fatal("inode observation acquired unsafe unlink permission", err)
	}
	if actual, err := os.ReadFile(filepath.Join(b.root, r.ObjectKey)); err != nil || !bytes.Equal(actual, body) {
		t.Fatal("unsupported retirement changed retained object", err)
	}
}

func TestLocalExclusiveReceiptRefusesReplacementMutationAndAliases(t *testing.T) {
	for _, change := range []string{"replacement", "same_inode_mutation", "symlink", "alias", "parent_replaced", "root_replaced", "foreign_root"} {
		t.Run(change, func(t *testing.T) {
			b := exclusiveLocalFixture(t)
			r, err := PutExclusiveArtifact(t.Context(), b, "capture/vmstate", strings.NewReader("original"), 8)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(b.root, r.ObjectKey)
			switch change {
			case "same_inode_mutation":
				err = os.WriteFile(path, []byte("replaced"), 0o644)
			case "alias":
				err = os.Link(path, filepath.Join(b.root, "alias"))
			case "replacement", "symlink":
				err = os.Rename(path, filepath.Join(b.root, "retained"))
				if err == nil {
					if change == "replacement" {
						err = os.WriteFile(path, []byte("replaced"), 0o644)
					} else {
						err = os.Symlink(filepath.Join(b.root, "retained"), path)
					}
				}
			case "parent_replaced":
				err = os.Rename(filepath.Dir(path), filepath.Join(b.root, "retained-parent"))
				if err == nil {
					err = os.Mkdir(filepath.Dir(path), 0o700)
				}
			case "root_replaced":
				err = os.Rename(b.root, b.root+"-retained")
				if err == nil {
					t.Cleanup(func() { _ = os.RemoveAll(b.root + "-retained") })
					err = os.Mkdir(b.root, 0o700)
				}
			case "foreign_root":
				b = exclusiveLocalFixture(t)
			}
			if err != nil {
				t.Fatal(err)
			}
			reader, err := GetExclusiveArtifact(t.Context(), b, r)
			if change == "same_inode_mutation" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.ReadAll(reader)
				err = errors.Join(err, reader.Close())
			} else if reader != nil {
				err = errors.Join(err, reader.Close())
			}
			if err == nil {
				t.Fatal("changed artifact supplied original content proof")
			}
		})
	}
}

func TestLocalExclusiveReceiptFailsWithoutAdoptingNamedObject(t *testing.T) {
	b := exclusiveLocalFixture(t)
	key := "capture/vmstate"
	r, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("short"), 8)
	if err == nil || r.Version != 0 {
		t.Fatal("incomplete source supplied receipt", r, err)
	}
	assertNoExclusiveArtifacts(t, b.root)
	r, err = PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("original"), 8)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := PutExclusiveArtifact(t.Context(), b, key, strings.NewReader("replaced"), 8)
	if !errors.Is(err, ErrArtifactExists) || replayed.Version != 0 {
		t.Fatal("repeat publication adopted original receipt", replayed, err)
	}
}
