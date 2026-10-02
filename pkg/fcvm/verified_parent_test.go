package fcvm

// adr: 431

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
)

type failedParentReader struct{ *bytes.Reader }

func (r failedParentReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, errors.New("storage read failed")
	}
	return n, err
}
func TestVerifiedParentCopiesExactSourceBytes(t *testing.T) {
	source := append([]byte("filesystem metadata"), make([]byte, 4096)...)
	expected := imagechain.BaseArtifact{StorageKey: "base/parent.ext4", Digest: imagechain.Digest(source), Bytes: int64(len(source))}
	for _, mode := range []string{"complete", "changed unused capacity", "truncated", "suffix", "read failure", "canceled", "closed output"} {
		t.Run(mode, func(t *testing.T) {
			input := bytes.Clone(source)
			ctx := t.Context()
			switch mode {
			case "changed unused capacity":
				input[len(input)-1] ^= 1
			case "truncated":
				input = input[:len(input)-1]
			case "suffix":
				input = append(input, 1)
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			path := filepath.Join(t.TempDir(), "protected-source")
			dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
			var reader io.Reader = bytes.NewReader(input)
			if mode == "read failure" {
				reader = failedParentReader{bytes.NewReader(input)}
			}
			if mode == "closed output" {
				if err := dst.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = writeVerifiedParentSource(ctx, dst, reader, expected)
			if mode != "complete" {
				if err == nil {
					t.Fatal("unverified source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(copied, source) {
				t.Fatalf("copied different mount source: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("source not protected: %v", err)
			}
		})
	}
}
