package runfilestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"sync"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/storage"
)

var ErrIntegrity = errors.New("run file length or checksum is invalid")

type Written struct {
	SizeBytes int64
	SHA256    string
}

// Put owns and closes plain. Both the encryption worker and backend operation
// are joined, including on early storage failure or request cancellation.
// The key is an immutable, newly reserved attempt, never an existing object.
func Put(ctx context.Context, backend storage.StorageBackend, accountID string, ref runfile.Ref, plain io.ReadCloser, size int64, expectedSHA string) (Written, error) {
	if plain == nil {
		return Written{}, ErrIntegrity
	}
	defer plain.Close() //nolint:errcheck // transfer completion controls publication
	if backend == nil || size < 0 || size > api.ExecutionFileHardMaxBytes || expectedSHA != "" && !runfile.ValidDigest(expectedSHA) {
		return Written{}, ErrIntegrity
	}
	if err := runfile.ValidateKey(accountID, ref.ID, ref.StorageKey); err != nil {
		return Written{}, err
	}
	identity, err := fileIdentity(accountID, ref)
	if err != nil {
		return Written{}, errors.New("run file encryption key is unavailable")
	}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	stop := context.AfterFunc(work, func() {
		_ = plain.Close()
		_ = reader.CloseWithError(work.Err())
		_ = writer.CloseWithError(work.Err())
	})
	defer stop()
	type completion struct {
		value Written
		err   error
	}
	done := make(chan completion, 1)
	go func() {
		h := sha256.New()
		encrypted, err := age.Encrypt(writer, identity.Recipient())
		var n int64
		if err == nil {
			n, err = io.CopyBuffer(encrypted, io.TeeReader(io.LimitReader(plain, size+1), h), make([]byte, api.ExecutionFileStreamChunkBytes))
		}
		sha := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if err == nil && (n != size || expectedSHA != "" && sha != expectedSHA) {
			err = ErrIntegrity
		}
		if err == nil {
			err = encrypted.Close()
		}
		_ = writer.CloseWithError(err)
		done <- completion{Written{n, sha}, err}
	}()
	putErr := backend.Put(work, ref.StorageKey, reader)
	_ = reader.CloseWithError(putErr)
	if putErr != nil {
		cancel()
	}
	result := <-done
	if err := errors.Join(putErr, result.err, work.Err()); err != nil {
		return Written{}, err
	}
	return result.value, nil
}

type verifiedReader struct {
	ctx         context.Context
	body        io.ReadCloser
	plain       io.Reader
	hash        hash.Hash
	expected    int64
	expectedSHA string
	read        int64
	stop        func() bool
	closeOnce   sync.Once
	closeErr    error
}

type onceReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *onceReadCloser) Close() error {
	r.once.Do(func() { r.err = r.ReadCloser.Close() })
	return r.err
}

func Open(ctx context.Context, backend storage.StorageBackend, accountID string, ref runfile.Ref) (io.ReadCloser, error) {
	if backend == nil || ref.SizeBytes < 0 || ref.SizeBytes > api.ExecutionFileHardMaxBytes || !runfile.ValidDigest(ref.SHA256) {
		return nil, ErrIntegrity
	}
	if err := runfile.ValidateKey(accountID, ref.ID, ref.StorageKey); err != nil {
		return nil, err
	}
	identity, err := fileIdentity(accountID, ref)
	if err != nil {
		return nil, errors.New("run file decryption key is unavailable")
	}
	rawBody, err := backend.Get(ctx, ref.StorageKey)
	if err != nil {
		return nil, err
	}
	body := &onceReadCloser{ReadCloser: rawBody}
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	plain, err := age.Decrypt(body, identity)
	if err != nil {
		stop()
		_ = body.Close()
		return nil, ErrIntegrity
	}
	return &verifiedReader{ctx: ctx, body: body, plain: plain, hash: sha256.New(), expected: ref.SizeBytes, expectedSHA: ref.SHA256, stop: stop}, nil
}

func (r *verifiedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.read > r.expected {
		return 0, ErrIntegrity
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > r.expected-r.read+1 {
		p = p[:int(r.expected-r.read+1)]
	}
	n, err := r.plain.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
		r.read += int64(n)
	}
	if r.read > r.expected {
		return n, ErrIntegrity
	}
	if errors.Is(err, io.EOF) && (r.read != r.expected || "sha256:"+hex.EncodeToString(r.hash.Sum(nil)) != r.expectedSHA) {
		return n, ErrIntegrity
	}
	return n, err
}

func (r *verifiedReader) Close() error {
	r.closeOnce.Do(func() { r.stop(); r.closeErr = r.body.Close() })
	return r.closeErr
}
