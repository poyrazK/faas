// adr: 568 — only the original acknowledged writer supplies artifact receipts.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"path/filepath"
)

// ExclusiveArtifactReceipt describes one original successful publication. Key
// follows logical prefix routing; ObjectKey and Location identify its canonical
// destination. StoredBytes is the writer's committed encoded/allocated-byte
// observation, not a promise that future filesystem allocation cannot change.
// A receipt is evidence, not authorization to delete: the owner must separately
// fence retirement. Local inode observations do not permit conditional unlink.
type ExclusiveArtifactReceipt struct {
	Version      int                    `json:"version"`
	Key          string                 `json:"key"`
	ObjectKey    string                 `json:"object_key"`
	Backend      string                 `json:"backend"`
	Location     string                 `json:"location"`
	LogicalBytes int64                  `json:"logical_bytes"`
	StoredBytes  int64                  `json:"stored_bytes"`
	SHA256       string                 `json:"sha256"`
	Encoding     string                 `json:"encoding"`
	Generation   int64                  `json:"generation"`
	Local        *ExclusiveLocalReceipt `json:"local"`
}

type ExclusiveLocalReceipt struct {
	Device       uint64 `json:"device"`
	Inode        uint64 `json:"inode"`
	RootDevice   uint64 `json:"root_device"`
	RootInode    uint64 `json:"root_inode"`
	ParentDevice uint64 `json:"parent_device"`
	ParentInode  uint64 `json:"parent_inode"`
}

func (r *ExclusiveArtifactReceipt) UnmarshalJSON(data []byte) error {
	fields, err := exclusiveReceiptJSONFields(data, []string{"version", "key", "object_key", "backend", "location", "logical_bytes", "stored_bytes", "sha256", "encoding", "generation", "local"})
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(fields["local"]), []byte("null")) {
		if _, err := exclusiveReceiptJSONFields(fields["local"], []string{"device", "inode", "root_device", "root_inode", "parent_device", "parent_inode"}); err != nil {
			return err
		}
	}
	type plain ExclusiveArtifactReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*plain)(r)); err != nil {
		return err
	}
	return r.Validate()
}

func exclusiveReceiptJSONFields(data []byte, required []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, ErrArtifactReceiptMismatch
	}
	fields := make(map[string]json.RawMessage, len(required))
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := name.(string)
		if !ok || fields[key] != nil {
			return nil, ErrArtifactReceiptMismatch
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if key != "local" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrArtifactReceiptMismatch
		}
		fields[key] = value
	}
	if len(fields) != len(required) {
		return nil, ErrArtifactReceiptMismatch
	}
	for _, name := range required {
		if fields[name] == nil {
			return nil, ErrArtifactReceiptMismatch
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, ErrArtifactReceiptMismatch
	}
	return fields, nil
}

var (
	ErrExclusiveReceiptUnsupported = errors.New("storage: original artifact receipts unsupported")
	ErrExclusiveRetireUnsupported  = errors.New("storage: conditional artifact retirement unsupported")
	ErrArtifactReceiptMismatch     = errors.New("storage: artifact differs from original receipt")
	ErrArtifactReadIncomplete      = errors.New("storage: original artifact read was not verified to EOF")
)

type ExclusiveArtifactBackend interface {
	CheckExclusiveArtifact(context.Context, string) error
	PutExclusiveArtifact(context.Context, string, io.Reader, int64) (ExclusiveArtifactReceipt, error)
	GetExclusiveArtifact(context.Context, ExclusiveArtifactReceipt) (io.ReadCloser, error)
}

// ExclusiveArtifactRetirer must condition the effect on the receipt identity
// inside the backend operation. A stat followed by ordinary Delete is forbidden.
type ExclusiveArtifactRetirer interface {
	RetireExclusiveArtifact(context.Context, ExclusiveArtifactReceipt) error
}

func (r ExclusiveArtifactReceipt) Validate() error {
	if err := errors.Join(validateKey(r.Key), validateKey(r.ObjectKey)); err != nil {
		return err
	}
	digest, err := hex.DecodeString(r.SHA256)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != r.SHA256 || r.Version != 1 || r.LogicalBytes <= 0 || r.StoredBytes < 0 {
		return ErrArtifactReceiptMismatch
	}
	switch r.Backend {
	case "gcs":
		if validateGCSBucket(r.Location) != nil || r.Generation <= 0 || r.Local != nil || r.StoredBytes <= 0 || (r.Encoding != "" && r.Encoding != snapshotCompressionZstd) || (r.Encoding == "" && r.StoredBytes != r.LogicalBytes) {
			return ErrArtifactReceiptMismatch
		}
	case "local":
		l := r.Local
		if !filepath.IsAbs(r.Location) || filepath.Clean(r.Location) != r.Location || r.Location == "/" || r.Generation != 0 || r.Encoding != "" || l == nil || l.Device == 0 || l.Inode == 0 || l.RootDevice != l.Device || l.RootInode == 0 || l.ParentDevice != l.Device || l.ParentInode == 0 {
			return ErrArtifactReceiptMismatch
		}
	default:
		return ErrArtifactReceiptMismatch
	}
	return nil
}

func CheckExclusiveArtifact(ctx context.Context, backend StorageBackend, key string) error {
	if err := errors.Join(validateKey(key), ctx.Err()); err != nil {
		return err
	}
	b, ok := backend.(ExclusiveArtifactBackend)
	if !ok {
		return ErrExclusiveReceiptUnsupported
	}
	return b.CheckExclusiveArtifact(ctx, key)
}

func PutExclusiveArtifact(ctx context.Context, backend StorageBackend, key string, source io.Reader, size int64) (ExclusiveArtifactReceipt, error) {
	if err := CheckExclusiveArtifact(ctx, backend, key); err != nil {
		return ExclusiveArtifactReceipt{}, err
	}
	if source == nil || size <= 0 {
		return ExclusiveArtifactReceipt{}, errors.New("storage: receipt publication requires a nonempty original reader")
	}
	r, err := backend.(ExclusiveArtifactBackend).PutExclusiveArtifact(ctx, key, source, size)
	if err == nil {
		err = errors.Join(r.Validate(), ctx.Err())
		if r.Key != key || r.LogicalBytes != size {
			err = errors.Join(err, ErrArtifactReceiptMismatch)
		}
	}
	if err != nil {
		return ExclusiveArtifactReceipt{}, err // Never supply a receipt for an uncertain effect.
	}
	return r, nil
}

func GetExclusiveArtifact(ctx context.Context, backend StorageBackend, receipt ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	if err := errors.Join(receipt.Validate(), ctx.Err()); err != nil {
		return nil, err
	}
	b, ok := backend.(ExclusiveArtifactBackend)
	if !ok {
		return nil, ErrExclusiveReceiptUnsupported
	}
	reader, err := b.GetExclusiveArtifact(ctx, receipt)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, ErrArtifactReceiptMismatch
	}
	return &verifiedArtifactReader{ctx: ctx, source: reader, receipt: receipt, digest: sha256.New()}, nil
}

func RetireExclusiveArtifact(ctx context.Context, backend StorageBackend, receipt ExclusiveArtifactReceipt) error {
	if err := errors.Join(receipt.Validate(), ctx.Err()); err != nil {
		return err
	}
	b, ok := backend.(ExclusiveArtifactRetirer)
	if !ok {
		return ErrExclusiveRetireUnsupported
	}
	return b.RetireExclusiveArtifact(ctx, receipt)
}

// A receipt-bound read supplies content proof only after exact length, digest
// and EOF verification. Closing early cannot be mistaken for complete evidence.
type verifiedArtifactReader struct {
	ctx      context.Context
	source   io.ReadCloser
	receipt  ExclusiveArtifactReceipt
	digest   hash.Hash
	read     int64
	complete bool
	failed   error
}

func (r *verifiedArtifactReader) Read(p []byte) (int, error) {
	if err := errors.Join(r.failed, r.ctx.Err()); err != nil {
		return 0, err
	}
	// Read at most one byte beyond the receipt's size, so malformed/compressed
	// content cannot make an unbounded read or overflow the byte counter.
	remaining := r.receipt.LogicalBytes - r.read
	if remaining < int64(len(p)) {
		p = p[:remaining+1]
	}
	n, err := r.source.Read(p)
	_, _ = r.digest.Write(p[:n])
	if int64(n) > remaining {
		err = errors.Join(err, ErrArtifactReceiptMismatch)
	} else {
		r.read += int64(n)
	}
	if err == io.EOF { //nolint:errorlint // Only the original EOF acknowledges all source bytes.
		if r.read != r.receipt.LogicalBytes || hex.EncodeToString(r.digest.Sum(nil)) != r.receipt.SHA256 {
			err = ErrArtifactReceiptMismatch
		} else {
			r.complete = true
		}
	}
	if err != nil && err != io.EOF { //nolint:errorlint // Wrapped EOF with a source failure is not proof.
		r.failed = err
	}
	return n, err
}

func (r *verifiedArtifactReader) Close() error {
	var incomplete error
	if !r.complete {
		incomplete = ErrArtifactReadIncomplete
	}
	return errors.Join(r.failed, incomplete, r.source.Close(), r.ctx.Err())
}
