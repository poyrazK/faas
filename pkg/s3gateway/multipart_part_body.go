package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
)

// Observe the verified body before releasing its final bytes to the transport.
// Partial reads and integrity failures never become complete body observations.
// The completion channel also prevents an early provider reply from settling a
// protected upload merely because its incoming spool has already been validated.
type multipartPartBodyReader struct {
	body      io.Reader
	remaining int64
	digest    hash.Hash
	observe   func(string) error
	completed chan struct{}
	err       error
}

func newMultipartPartBodyReader(body io.Reader, size int64, observe func(string) error) *multipartPartBodyReader {
	return &multipartPartBodyReader{body: body, remaining: size, digest: sha256.New(), observe: observe, completed: make(chan struct{})}
}
func (r *multipartPartBodyReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.body.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
		return 0, err
	}
	if n > 0 {
		_, _ = r.digest.Write(p[:n])
		r.remaining -= int64(n)
	}
	if r.remaining != 0 {
		if errors.Is(err, io.EOF) {
			r.err = io.ErrUnexpectedEOF
			return 0, r.err
		}
		return n, nil
	}
	if err = r.observe(hex.EncodeToString(r.digest.Sum(nil))); err != nil {
		r.err = err
		return 0, err
	}
	close(r.completed)
	return n, nil
}
func (r *multipartPartBodyReader) Completed() bool {
	select {
	case <-r.completed:
		return true
	default:
		return false
	}
}
