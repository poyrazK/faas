package imaged

// ADR-733 encryption at rest for crash captures.
//
// A capture is a copy of production memory. imaged encrypts each ready
// capture's objects with a fresh age X25519 identity (zstd first: guest
// memory is mostly zero pages and ciphertext is never sparse), seals that
// identity to the fleet recipient in crash_captures.sealed_key, and deletes
// the plaintext. A plaintext copy comes back only while a fork pinned to the
// capture is active (queued, restoring or running), and is purged once no
// fork needs it. Expiry deletes every object and drops the sealed key.
//
// All of it runs on one goroutine, so encrypting, staging, purging and
// expiring a capture never race each other.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const (
	// crashCaptureCryptoEvery bounds how long a fork waits for its
	// capture to be staged and how long plaintext lingers after capture.
	crashCaptureCryptoEvery = 5 * time.Second
	// crashCaptureCryptoBatch bounds each step of one tick.
	crashCaptureCryptoBatch = 10
	// crashCaptureEncryptedSuffix names a plaintext object's encrypted twin.
	crashCaptureEncryptedSuffix = ".age"
	// crashCaptureSealedKeyMax caps the sealed identity (an age secret key
	// string is 74 bytes).
	crashCaptureSealedKeyMax = 256
)

// crashCaptureNamespace binds a sealed key to its capture, so a key copied
// onto another row does not open.
func crashCaptureNamespace(captureID string) string {
	return "crash_capture:" + captureID
}

// runCrashCaptureCrypto ticks until ctx ends.
func (l *Loop) runCrashCaptureCrypto(ctx context.Context) {
	every := l.crashCaptureCryptoEvery
	if every <= 0 {
		every = crashCaptureCryptoEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.tendCrashCaptures(ctx, l.now())
		}
	}
}

// tendCrashCaptures runs one pass: expire, encrypt, purge, stage.
func (l *Loop) tendCrashCaptures(ctx context.Context, now time.Time) {
	if l.store == nil || l.handler == nil {
		return
	}
	be, err := l.handler.storageFor()
	if err != nil {
		l.log.Warn("imaged: crash capture storage", "err", err)
		return
	}
	l.expireCrashCaptures(ctx, be, now)
	identities := l.handler.secretboxIdentities
	l.encryptCrashCaptures(ctx, be, identities, now)
	l.purgeCrashPlaintext(ctx, be, now)
	l.stageCrashCaptures(ctx, be, identities, now)
}

func (l *Loop) encryptCrashCaptures(ctx context.Context, be storage.StorageBackend, identities []*age.X25519Identity, now time.Time) {
	due, err := l.store.CrashCapturesToEncrypt(ctx, now, crashCaptureCryptoBatch)
	if err != nil || len(due) == 0 {
		if err != nil {
			l.log.Warn("imaged: crash capture encrypt list", "err", err)
		}
		return
	}
	recipient, err := secretbox.CurrentRecipient(identities)
	if err != nil {
		l.log.Error("imaged: crash captures stay unencrypted: no host age identity (FAAS_HOST_AGE_IDENTITY_PATH)", "pending", len(due))
		return
	}
	for _, capture := range due {
		sealed, err := encryptCrashCapture(ctx, be, recipient, capture)
		if err != nil {
			l.log.Warn("imaged: crash capture encrypt", "capture", capture.ID, "err", err)
			continue
		}
		marked, err := l.store.MarkCrashCaptureEncrypted(ctx, capture.ID, sealed, l.now())
		if err != nil {
			// Expired or already encrypted meanwhile; the next pass
			// (or expiry) deletes what this one wrote.
			l.log.Warn("imaged: crash capture mark encrypted", "capture", capture.ID, "err", err)
			continue
		}
		if marked.PlaintextState == state.CrashPlaintextPurging {
			l.purgeCrashCapture(ctx, be, marked)
		}
	}
}

func (l *Loop) purgeCrashPlaintext(ctx context.Context, be storage.StorageBackend, now time.Time) {
	due, err := l.store.CrashCapturesToPurge(ctx, crashCaptureCryptoBatch)
	if err != nil {
		l.log.Warn("imaged: crash capture purge list", "err", err)
		return
	}
	for _, capture := range due {
		if capture.PlaintextState != state.CrashPlaintextPurging {
			// Fenced on no active fork, so a fork that could still
			// restore the plaintext never loses it.
			if capture, err = l.store.BeginCrashCapturePurge(ctx, capture.ID, now); err != nil {
				continue
			}
		}
		l.purgeCrashCapture(ctx, be, capture)
	}
}

func (l *Loop) purgeCrashCapture(ctx context.Context, be storage.StorageBackend, capture state.CrashCapture) {
	if err := deleteCrashCaptureKeys(ctx, be, crashCaptureKeys(capture)); err != nil {
		l.log.Warn("imaged: crash capture purge", "capture", capture.ID, "err", err)
		return
	}
	if _, err := l.store.FinishCrashCapturePurge(ctx, capture.ID, l.now()); err != nil && !errors.Is(err, state.ErrNotFound) {
		l.log.Warn("imaged: crash capture purge finish", "capture", capture.ID, "err", err)
	}
}

func (l *Loop) stageCrashCaptures(ctx context.Context, be storage.StorageBackend, identities []*age.X25519Identity, now time.Time) {
	due, err := l.store.CrashCapturesToStage(ctx, now, crashCaptureCryptoBatch)
	if err != nil || len(due) == 0 {
		if err != nil {
			l.log.Warn("imaged: crash capture stage list", "err", err)
		}
		return
	}
	for _, capture := range due {
		if capture, err = l.store.BeginCrashCaptureStage(ctx, capture.ID, now); err != nil {
			continue
		}
		if err := decryptCrashCapture(ctx, be, identities, capture); err != nil {
			// Left in staging: retried while a fork wants it, purged
			// once none does.
			l.log.Warn("imaged: crash capture stage", "capture", capture.ID, "err", err)
			continue
		}
		if _, err := l.store.FinishCrashCaptureStage(ctx, capture.ID, l.now()); err != nil && !errors.Is(err, state.ErrNotFound) {
			l.log.Warn("imaged: crash capture stage finish", "capture", capture.ID, "err", err)
		}
	}
}

// encryptCrashCapture writes the encrypted twin of every object and returns
// the capture's identity sealed to recipient. Memory and vmstate must exist;
// the private drive and backing identity are optional.
func encryptCrashCapture(ctx context.Context, be storage.StorageBackend, recipient *age.X25519Recipient, capture state.CrashCapture) ([]byte, error) {
	keys := crashCaptureKeys(capture)
	if len(keys) < 2 {
		return nil, errors.New("crash capture has no storage keys")
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("generate capture key: %w", err)
	}
	for i, key := range keys {
		err := encryptObject(ctx, be, key, identity.Recipient())
		if errors.Is(err, storage.ErrNotFound) && i >= 2 {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("encrypt %s: %w", key, err)
		}
	}
	return secretbox.SealBytes(recipient, crashCaptureNamespace(capture.ID), []byte(identity.String()), crashCaptureSealedKeyMax)
}

// decryptCrashCapture restores every plaintext object from its encrypted
// twin. An optional object that was never there has no twin.
func decryptCrashCapture(ctx context.Context, be storage.StorageBackend, identities []*age.X25519Identity, capture state.CrashCapture) error {
	if len(identities) == 0 || len(capture.SealedKey) == 0 {
		return errors.New("no key to open the capture")
	}
	namespace, raw, err := secretbox.OpenBytesMulti(identities, capture.SealedKey)
	if err != nil {
		return fmt.Errorf("open sealed key: %w", err)
	}
	if namespace != crashCaptureNamespace(capture.ID) {
		return errors.New("sealed key belongs to another capture")
	}
	identity, err := age.ParseX25519Identity(string(raw))
	if err != nil {
		return fmt.Errorf("parse capture key: %w", err)
	}
	for i, key := range crashCaptureKeys(capture) {
		err := decryptObject(ctx, be, key, identity)
		if errors.Is(err, storage.ErrNotFound) && i >= 2 {
			continue
		}
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", key, err)
		}
	}
	return nil
}

// encryptObject streams key through zstd and age into its encrypted twin.
func encryptObject(ctx context.Context, be storage.StorageBackend, key string, recipient age.Recipient) error {
	src, err := be.Get(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return putThrough(ctx, be, key+crashCaptureEncryptedSuffix, func(w io.Writer) error {
		aw, err := age.Encrypt(w, recipient)
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
	})
}

// decryptObject restores key from its encrypted twin.
func decryptObject(ctx context.Context, be storage.StorageBackend, key string, identity age.Identity) error {
	src, err := be.Get(ctx, key+crashCaptureEncryptedSuffix)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return putThrough(ctx, be, key, func(w io.Writer) error {
		ar, err := age.Decrypt(src, identity)
		if err != nil {
			return err
		}
		zr, err := zstd.NewReader(ar, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		defer zr.Close()
		_, err = io.Copy(w, zr)
		return err
	})
}

// putThrough stores what produce writes under key, streaming through a
// pipe. A Put that stops reading unblocks produce, and produce has
// returned before putThrough does.
func putThrough(ctx context.Context, be storage.StorageBackend, key string, produce func(io.Writer) error) error {
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := produce(pw)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	putErr := be.Put(ctx, key, pr)
	_ = pr.CloseWithError(errors.New("put finished"))
	produceErr := <-done
	if putErr != nil {
		return putErr
	}
	return produceErr
}
