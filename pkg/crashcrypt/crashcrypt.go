// Package crashcrypt encrypts ADR-733 crash captures. A capture's objects
// (memory, vmstate, private drive, backing identity) are compressed and
// age-encrypted to a fresh per-capture identity; that identity is sealed to
// the fleet recipient and stored on the capture row. The encrypted twin of
// object key is key + Suffix.
//
// imaged encrypts captures already on local storage (plaintext_state
// present → absent). On a remote backend vmmd encrypts at capture time
// through Backend, so plaintext never reaches the shared store or a node's
// read-through cache, and decrypts at fork restore into its own staging.
package crashcrypt

import (
	"context"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Suffix names an object's encrypted twin.
const Suffix = ".age"

// sealedKeyMax bounds the sealed identity (an age X25519 identity string).
const sealedKeyMax = 256

// Namespace binds a sealed key to its capture.
func Namespace(captureID string) string { return "crash_capture:" + captureID }

// NewKey generates a capture identity and seals it to recipient.
func NewKey(recipient *age.X25519Recipient, captureID string) (*age.X25519Identity, []byte, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, nil, fmt.Errorf("crashcrypt: generate capture key: %w", err)
	}
	sealed, err := secretbox.SealBytes(recipient, Namespace(captureID), []byte(identity.String()), sealedKeyMax)
	if err != nil {
		return nil, nil, fmt.Errorf("crashcrypt: seal capture key: %w", err)
	}
	return identity, sealed, nil
}

// OpenKey opens a sealed capture identity with any of identities and checks
// it belongs to captureID.
func OpenKey(identities []*age.X25519Identity, captureID string, sealed []byte) (*age.X25519Identity, error) {
	if len(identities) == 0 || len(sealed) == 0 {
		return nil, errors.New("crashcrypt: no key to open the capture")
	}
	namespace, raw, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("crashcrypt: open sealed key: %w", err)
	}
	if namespace != Namespace(captureID) {
		return nil, errors.New("crashcrypt: sealed key belongs to another capture")
	}
	identity, err := age.ParseX25519Identity(string(raw))
	if err != nil {
		return nil, fmt.Errorf("crashcrypt: parse capture key: %w", err)
	}
	return identity, nil
}

// Encrypt writes src to dst compressed and encrypted to recipient.
func Encrypt(dst io.Writer, src io.Reader, recipient age.Recipient) error {
	aw, err := age.Encrypt(dst, recipient)
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(aw, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return err
	}
	if _, err := io.Copy(zw, src); err != nil {
		_ = zw.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return aw.Close()
}

// Decrypt returns a reader of src's plaintext.
func Decrypt(src io.Reader, identity age.Identity) (io.ReadCloser, error) {
	ar, err := age.Decrypt(src, identity)
	if err != nil {
		return nil, err
	}
	zr, err := zstd.NewReader(ar, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return zr.IOReadCloser(), nil
}

// Backend stores one capture's objects encrypted in Inner: Put writes only
// the encrypted twin, Get decrypts it, Delete removes both forms. It does not
// implement storage.LocalPathResolver, so no caller can publish or read a
// capture object through a local path that bypasses encryption.
type Backend struct {
	Inner    storage.StorageBackend
	Identity *age.X25519Identity
}

var _ storage.StorageBackend = Backend{}

func (b Backend) Put(ctx context.Context, key string, r io.Reader) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := Encrypt(pw, r, b.Identity.Recipient())
		_ = pw.CloseWithError(err)
		done <- err
	}()
	putErr := b.Inner.Put(ctx, key+Suffix, pr)
	_ = pr.CloseWithError(errors.New("crashcrypt: put finished"))
	encErr := <-done
	if putErr != nil {
		return putErr
	}
	return encErr
}

func (b Backend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	src, err := b.Inner.Get(ctx, key+Suffix)
	if err != nil {
		return nil, err
	}
	plain, err := Decrypt(src, b.Identity)
	if err != nil {
		_ = src.Close()
		return nil, fmt.Errorf("crashcrypt: decrypt %s: %w", key, err)
	}
	return readClosers{Reader: plain, closers: []io.Closer{plain, src}}, nil
}

func (b Backend) Delete(ctx context.Context, key string) error {
	return errors.Join(ignoreNotFound(b.Inner.Delete(ctx, key+Suffix)), ignoreNotFound(b.Inner.Delete(ctx, key)))
}

func ignoreNotFound(err error) error {
	if storage.IsNotFound(err) {
		return nil
	}
	return err
}

type readClosers struct {
	io.Reader
	closers []io.Closer
}

func (r readClosers) Close() error {
	var errs []error
	for _, c := range r.closers {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}
