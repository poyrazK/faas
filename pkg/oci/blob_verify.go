package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// verifyingBlob checks a streamed registry body against the digest used in
// the blob URL. The check happens when the reader reaches EOF, so a mismatch
// is returned through the normal io.Reader path that rootfs builders and
// config parsers already handle. Close also returns a mismatch observed by a
// previous Read; callers that stop early cannot claim verification because
// the unread bytes were never hashed.
type verifyingBlob struct {
	body     io.ReadCloser
	hash     hash.Hash
	expected string
	readErr  error
}

func newVerifyingBlob(body io.ReadCloser, expected string) io.ReadCloser {
	return &verifyingBlob{
		body:     body,
		hash:     sha256.New(),
		expected: expected,
	}
}

func (v *verifyingBlob) Read(p []byte) (int, error) {
	if v.readErr != nil {
		return 0, v.readErr
	}
	n, err := v.body.Read(p)
	if n > 0 {
		_, _ = v.hash.Write(p[:n])
	}
	if err == io.EOF {
		if got := digestAlgo + hex.EncodeToString(v.hash.Sum(nil)); got != v.expected {
			v.readErr = fmt.Errorf("%w: blob digest mismatch: requested %s, got %s", ErrImageManifestInvalid, v.expected, got)
			return n, v.readErr
		}
	}
	if err != nil {
		v.readErr = err
	}
	return n, err
}

func (v *verifyingBlob) Close() error {
	closeErr := v.body.Close()
	if v.readErr != nil && !errors.Is(v.readErr, io.EOF) {
		if closeErr != nil {
			return errors.Join(closeErr, v.readErr)
		}
		return v.readErr
	}
	return closeErr
}
