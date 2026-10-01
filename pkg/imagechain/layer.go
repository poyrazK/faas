package imagechain

// adr: 393

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

type LayerConsumption struct {
	Index             int    `json:"index"`
	Digest            string `json:"digest"`
	CompressedBytes   int64  `json:"compressed_bytes"`
	DiffID            string `json:"diff_id"`
	UncompressedBytes int64  `json:"uncompressed_bytes"`
}

// LayerStream records only actual complete reads; constructing/closing a reader
// never attests conversion. It is consumed by one build goroutine at a time.
type LayerStream struct {
	ctx               context.Context
	body              io.ReadCloser
	descriptor        Descriptor
	index             int
	diffID            string
	compressed        hash.Hash
	compressedBytes   int64
	readErr           error
	compressedDone    bool
	uncompressedBytes int64
	uncompressedDone  bool
}

func NewLayerStream(ctx context.Context, body io.ReadCloser, d Descriptor, diffID string, index int) (*LayerStream, error) {
	if body == nil || !ValidLayerDescriptor(d) || ociref.ValidateDigest(diffID) != nil || index < 0 || index >= api.OCIImageMaxLayers {
		return nil, ErrInvalid
	}
	return &LayerStream{ctx: ctx, body: body, descriptor: d, index: index, diffID: diffID, compressed: sha256.New()}, nil
}

func (s *LayerStream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.readErr != nil {
		return 0, s.readErr
	}
	n, err := s.body.Read(p)
	if n > 0 {
		_, _ = s.compressed.Write(p[:n])
		s.compressedBytes += int64(n)
	}
	if s.compressedBytes > s.descriptor.Size {
		err = fmt.Errorf("%w: compressed layer size", ErrInvalid)
	}
	if err == io.EOF {
		if s.compressedBytes != s.descriptor.Size || fmt.Sprintf("sha256:%x", s.compressed.Sum(nil)) != s.descriptor.Digest {
			err = fmt.Errorf("%w: compressed layer digest/size", ErrInvalid)
		} else {
			s.compressedDone = true
		}
	}
	if err != nil {
		s.readErr = err
	}
	return n, err
}

func (s *LayerStream) Close() error {
	err := s.body.Close()
	if s.readErr != nil && !errors.Is(s.readErr, io.EOF) {
		return errors.Join(err, s.readErr)
	}
	return err
}

type uncompressedReader struct {
	owner    *LayerStream
	body     io.Reader
	digest   hash.Hash
	bytes    int64
	terminal error
}

// VerifyingUncompressedReader is the rootfs consumer hook. Tar end-of-archive
// is insufficient: callers must drain this reader to gzip EOF before publishing.
func (s *LayerStream) VerifyingUncompressedReader(body io.Reader) io.Reader {
	s.uncompressedDone = false
	return &uncompressedReader{owner: s, body: body, digest: sha256.New()}
}
func (r *uncompressedReader) Read(p []byte) (int, error) {
	if err := r.owner.ctx.Err(); err != nil {
		return 0, err
	}
	if r.terminal != nil {
		return 0, r.terminal
	}
	n, err := r.body.Read(p)
	if n > 0 {
		_, _ = r.digest.Write(p[:n])
		r.bytes += int64(n)
	}
	if r.bytes > api.OCIImageMaxUncompressedLayerBytes {
		err = fmt.Errorf("%w: uncompressed layer limit", ErrInvalid)
	}
	if err == io.EOF {
		if fmt.Sprintf("sha256:%x", r.digest.Sum(nil)) != r.owner.diffID {
			err = fmt.Errorf("%w: uncompressed DiffID", ErrInvalid)
		} else {
			r.owner.uncompressedBytes = r.bytes
			r.owner.uncompressedDone = true
		}
	}
	if err != nil {
		r.terminal = err
	}
	return n, err
}

func (s *LayerStream) Consumption() (LayerConsumption, error) {
	if err := s.ctx.Err(); err != nil {
		return LayerConsumption{}, err
	}
	if !s.compressedDone || !s.uncompressedDone {
		return LayerConsumption{}, fmt.Errorf("%w: incomplete layer consumption", ErrInvalid)
	}
	return LayerConsumption{Index: s.index, Digest: s.descriptor.Digest, CompressedBytes: s.compressedBytes, DiffID: s.diffID, UncompressedBytes: s.uncompressedBytes}, nil
}

func ValidateConsumption(image Image, start int, layers []LayerConsumption) error {
	if start < 0 || start > len(image.Layers) || len(layers) != len(image.Layers)-start {
		return ErrInvalid
	}
	for i, got := range layers {
		index := start + i
		want := image.Layers[index]
		if got.Index != index || got.Digest != want.Digest || got.CompressedBytes != want.Size || got.DiffID != image.DiffIDs[index] || got.UncompressedBytes < 0 || got.UncompressedBytes > api.OCIImageMaxUncompressedLayerBytes {
			return ErrInvalid
		}
	}
	return nil
}
