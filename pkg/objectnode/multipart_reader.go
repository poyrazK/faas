package objectnode

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// One open part and the stageObject copy buffer bound assembly memory and file
// descriptors independently of the number or total size of selected parts.
type multipartReader struct {
	store  *Store
	upload string
	parts  []MultipartPart
	index  int
	file   *os.File
	digest hash.Hash
	read   int64
}

func (r *multipartReader) Read(buffer []byte) (int, error) {
	for r.index < len(r.parts) {
		part := r.parts[r.index]
		if r.file == nil {
			path := filepath.Join(r.store.partPath(r.upload, part.Number), part.Object.ID+".data")
			f, err := os.Open(path) //nolint:forbidigo // Pinned, validated immutable part in the private node directory.
			if err != nil {
				return 0, ErrCorrupt
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() != part.Object.Size {
				_ = f.Close()
				return 0, ErrCorrupt
			}
			r.file, r.digest, r.read = f, sha256.New(), 0
		}
		n, err := r.file.Read(buffer)
		if n > 0 {
			_, _ = r.digest.Write(buffer[:n])
			r.read += int64(n)
		}
		if errors.Is(err, io.EOF) {
			if r.read != part.Object.Size || hex.EncodeToString(r.digest.Sum(nil)) != part.Object.SHA256 {
				return n, ErrCorrupt
			}
			if err = r.file.Close(); err != nil {
				return n, err
			}
			r.file = nil
			r.index++
			if n == 0 {
				continue
			}
			return n, nil
		}
		return n, err
	}
	return 0, io.EOF
}

func (r *multipartReader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}
