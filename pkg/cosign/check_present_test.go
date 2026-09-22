package cosign

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// probingStorage is memStorage plus the ExistenceChecker capability. It
// counts Gets per key so tests can pin that CheckPresent never transfers
// the layer body (issue #3356).
type probingStorage struct {
	*memStorage
	gets     map[string]int
	probeErr error
}

func (p *probingStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p.gets[key]++
	return p.memStorage.Get(ctx, key)
}

func (p *probingStorage) Exists(_ context.Context, key string) (bool, error) {
	if p.probeErr != nil {
		return false, p.probeErr
	}
	_, ok := p.blobs[key]
	return ok, nil
}

func TestLocalVerifier_CheckPresent(t *testing.T) {
	const layerKey = "apps/test/00000000-0000-0000-0000-000000000000.ext4"
	sigKey := SigKeyFor(layerKey)
	ioErr := errors.New("registry unreachable")

	cases := []struct {
		name         string
		layer        bool
		sig          []byte
		probeErr     error
		withoutProbe bool
		wantNotFound bool
		wantSigCode  bool
		wantErr      error
	}{
		{name: "both present", layer: true, sig: make([]byte, 64)},
		{name: "layer missing", sig: make([]byte, 64), wantNotFound: true},
		{name: "sig missing", layer: true, wantNotFound: true},
		{name: "sig malformed", layer: true, sig: make([]byte, 12), wantSigCode: true},
		{name: "probe i/o error", layer: true, sig: make([]byte, 64), probeErr: ioErr, wantErr: ioErr},
		{name: "backend cannot probe", sig: make([]byte, 64), withoutProbe: true},
		{name: "probe unsupported by delegate", sig: make([]byte, 64), probeErr: storage.ErrExistenceUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			_, pubPath := keyPairTempDir(t)
			mem := newMemStorage()
			if tc.layer {
				mem.blobs[layerKey] = bytes.Repeat([]byte{1}, 4096)
			}
			if tc.sig != nil {
				mem.blobs[sigKey] = tc.sig
			}
			probing := &probingStorage{memStorage: mem, gets: map[string]int{}, probeErr: tc.probeErr}
			var stor storage.StorageBackend = probing
			if tc.withoutProbe {
				stor = mem
			}
			verifier, err := NewLocalVerifier(pubPath, stor)
			if err != nil {
				t.Fatalf("NewLocalVerifier: %v", err)
			}

			err = verifier.CheckPresent(ctx, layerKey, sigKey)

			if probing.gets[layerKey] != 0 {
				t.Fatalf("CheckPresent read the layer body %d times; it must only probe", probing.gets[layerKey])
			}
			switch {
			case tc.wantNotFound:
				if !storage.IsNotFound(err) {
					t.Fatalf("err = %v, want storage.ErrNotFound", err)
				}
			case tc.wantSigCode:
				var p *api.Problem
				if !errors.As(err, &p) || p.Code != api.CodeSigInvalid {
					t.Fatalf("err = %v, want %s problem", err, api.CodeSigInvalid)
				}
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) || storage.IsNotFound(err) {
					t.Fatalf("err = %v, want wrapped %v", err, tc.wantErr)
				}
			default:
				if err != nil {
					t.Fatalf("CheckPresent: %v", err)
				}
			}
		})
	}
}
