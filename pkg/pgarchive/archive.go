// Package pgarchive validates and transfers native PostgreSQL export files.
package pgarchive

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/onebox-faas/faas/pkg/pgexport"
)

const ManifestLimit = 32 << 10

var ErrArchive = errors.New("archive size, format or checksum does not match the manifest")

type Manifest struct {
	pgexport.Source
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func (m Manifest) Valid() bool {
	digest, err := hex.DecodeString(m.SHA256)
	return m.Source.Valid() && m.DatabaseBytes > 0 && m.DatabaseBytes <= 1<<50 &&
		m.SizeBytes >= 5 && m.SizeBytes <= pgexport.MaxBytes &&
		err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == m.SHA256
}

// Receipt authenticates the archive's digest and restore metadata. The
// account sealing identities are kept on the API servers, never in guests.
func (m Manifest) Receipt(secret string) string {
	if secret == "" || !m.Valid() {
		return ""
	}
	m.CapturedAt = m.CapturedAt.UTC()
	raw, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, "gregale/native-postgres-restore/v1\x00")
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m Manifest) VerifyReceipt(receipt, secret string) bool {
	expected := m.Receipt(secret)
	return expected != "" && hmac.Equal([]byte(receipt), []byte(expected))
}

// Copy verifies a bounded PGDMP payload. The caller supplies an input
// limited to the archive bytes when the enclosing protocol has a trailer.
func (m Manifest) Copy(dst io.Writer, r io.Reader) error {
	if !m.Valid() {
		return ErrArchive
	}
	var magic [5]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil || !bytes.Equal(magic[:], []byte("PGDMP")) {
		return ErrArchive
	}
	digest := sha256.New()
	out := io.MultiWriter(dst, digest)
	if _, err := out.Write(magic[:]); err != nil {
		return err
	}
	if _, err := io.CopyN(out, r, m.SizeBytes-int64(len(magic))); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrArchive
		}
		return err
	}
	// Read to EOF: neither an overlong body nor a truncated stream can
	// authorize the restore. Limited readers stop at their own boundary.
	var extra [1]byte
	if _, err := io.ReadFull(r, extra[:]); err != io.EOF || hex.EncodeToString(digest.Sum(nil)) != m.SHA256 {
		return ErrArchive
	}
	return nil
}
