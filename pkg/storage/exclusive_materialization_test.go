// adr: 568 — private copies require complete original generation verification.
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type exclusiveCopyReadFault struct {
	*GCSStorageBackend
	body     []byte
	closeErr error
	closed   int
}

type exclusiveCopyCloser struct {
	io.Reader
	backend *exclusiveCopyReadFault
}

func (r exclusiveCopyCloser) Close() error { r.backend.closed++; return r.backend.closeErr }
func (b *exclusiveCopyReadFault) GetExclusiveArtifact(context.Context, ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	return exclusiveCopyCloser{Reader: bytes.NewReader(b.body), backend: b}, nil
}

func TestExclusiveArtifactMaterializationVerifiesBeforeSuccess(t *testing.T) {
	for _, encoding := range []string{snapshotCompressionNone, snapshotCompressionZstd} {
		t.Run(encoding, func(t *testing.T) {
			b, store := receiptGCSFixture(t, encoding)
			body := make([]byte, 1<<20)
			copy(body, "original")
			copy(body[len(body)/2:], "another allocated page")
			r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"mem", bytes.NewReader(body), int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.CreateTemp(t.TempDir(), "private")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if file != nil {
					_ = file.Close()
				}
			}()
			count, err := CopyExclusiveArtifact(t.Context(), b, r, file)
			if err != nil || count != int64(len(body)) {
				t.Fatal(count, err)
			}
			actual := make([]byte, len(body))
			if _, err := file.ReadAt(actual, 0); err != nil || !bytes.Equal(actual, body) {
				t.Fatal("verified private copy differs", err)
			}
			if store.reads != 1 || store.retirements != 0 || store.ordinaryDeletes != 0 {
				t.Fatal("copy borrowed lookup or cleanup authority")
			}
		})
	}
}

func TestExclusiveArtifactMaterializationRefusesIncompleteProofAndOccupiedOutput(t *testing.T) {
	for _, failure := range []string{"digest", "short", "long", "close", "cancel", "occupied", "offset", "shared", "nil", "directory"} {
		t.Run(failure, func(t *testing.T) {
			b, _ := receiptGCSFixture(t, snapshotCompressionNone)
			body := []byte("original")
			r, err := PutExclusiveArtifact(t.Context(), b, exclusiveCapturePrefix+"mem", bytes.NewReader(body), int64(len(body)))
			if err != nil {
				t.Fatal(err)
			}
			fault := &exclusiveCopyReadFault{GCSStorageBackend: b, body: body}
			file, err := os.CreateTemp(t.TempDir(), "private")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if file != nil {
					_ = file.Close()
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch failure {
			case "digest":
				fault.body = []byte("replaced")
			case "short":
				fault.body = body[:len(body)-1]
			case "long":
				fault.body = append(append([]byte(nil), body...), 'x')
			case "close":
				fault.closeErr = errors.New("original source close failed")
			case "cancel":
				cancel()
			case "occupied":
				if _, err := file.Write(body); err != nil {
					t.Fatal(err)
				}
			case "offset":
				if _, err := file.Seek(1, io.SeekStart); err != nil {
					t.Fatal(err)
				}
			case "shared":
				if err := file.Chmod(0o644); err != nil {
					t.Fatal(err)
				}
			case "nil":
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				file = nil
			case "directory":
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				file, err = os.OpenFile(filepath.Dir(file.Name()), os.O_RDONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
			}
			count, err := CopyExclusiveArtifact(ctx, fault, r, file)
			if err == nil || count != 0 {
				t.Fatal("incomplete materialization acquired proof", count, err)
			}
			if failure == "digest" || failure == "short" || failure == "long" || failure == "close" {
				if fault.closed != 1 {
					t.Fatal("original source was not joined exactly once", fault.closed)
				}
			} else if fault.closed != 0 {
				t.Fatal("invalid output started a source", fault.closed)
			}
		})
	}
}
