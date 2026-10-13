// Package objectchecksum implements the bounded S3 multipart integrity contract.
// Checksums attest to bytes; they are not authentication or write receipts.
package objectchecksum

import (
	"crypto/sha1" // #nosec G505 -- S3 wire checksum compatibility only.
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const FullObject = "FULL_OBJECT"
const Composite = "COMPOSITE"
const NVMEPolynomial uint64 = 0x9a6c9329ac4bc9b5

var ErrInvalid = errors.New("invalid object checksum")

type Policy struct {
	Algorithm string `json:"algorithm,omitempty"`
	Type      string `json:"type,omitempty"`
}

type Digest struct {
	Policy
	Value string `json:"value,omitempty"`
}

type Part struct {
	Number int32  `json:"number"`
	Size   int64  `json:"size"`
	Value  string `json:"value"`
}

// Snapshot binds the checksum policy and selected native parts to completion.
// Value is calculated before dispatch and must match the native acknowledgment.
type Snapshot struct {
	Digest
	Parts []Part `json:"parts,omitempty"`
}

func NewPolicy(algorithm, kind string) (Policy, error) {
	p := Policy{Algorithm: strings.ToUpper(algorithm), Type: strings.ToUpper(kind)}
	if p.Algorithm != "" && p.Type == "" {
		p.Type = Composite
		if p.Algorithm == "CRC64NVME" {
			p.Type = FullObject
		}
	}
	if !p.Valid() {
		return Policy{}, ErrInvalid
	}
	return p, nil
}

func (p Policy) Empty() bool { return p == (Policy{}) }

func (p Policy) Valid() bool {
	if p.Empty() {
		return true
	}
	switch p.Algorithm {
	case "CRC32", "CRC32C":
		return p.Type == FullObject || p.Type == Composite
	case "CRC64NVME":
		return p.Type == FullObject
	case "SHA1", "SHA256":
		return p.Type == Composite
	}
	return false
}

func NewHash(algorithm string) hash.Hash {
	switch algorithm {
	case "CRC32":
		return crc32.NewIEEE()
	case "CRC32C":
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case "CRC64NVME":
		return crc64.New(crc64.MakeTable(NVMEPolynomial))
	case "SHA1":
		return sha1.New() // #nosec G401 -- S3 compatibility, not secret derivation.
	case "SHA256":
		return sha256.New()
	}
	return nil
}

func ValidPart(algorithm, value string) bool {
	if len(value) > api.MaxObjectChecksumValueBytes {
		return false
	}
	h := NewHash(algorithm)
	if h == nil {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(b) == h.Size() && base64.StdEncoding.EncodeToString(b) == value
}

func (d Digest) Valid(parts int) bool {
	if d.Empty() {
		return d.Value == ""
	}
	if !d.Policy.Valid() {
		return false
	}
	v := d.Value
	if d.Type == Composite {
		i := strings.LastIndexByte(v, '-')
		if i < 0 || parts < 1 || parts > api.MaxMultipartParts || v[i+1:] != strconv.Itoa(parts) {
			return false
		}
		v = v[:i]
	}
	return ValidPart(d.Algorithm, v)
}

func (s Snapshot) Clone() Snapshot { s.Parts = slices.Clone(s.Parts); return s }
func (s Snapshot) Equal(other Snapshot) bool {
	return s.Digest == other.Digest && slices.Equal(s.Parts, other.Parts)
}
func (s Snapshot) Initial() bool { return s.Policy.Valid() && s.Value == "" && len(s.Parts) == 0 }

func Build(p Policy, parts []Part) (Snapshot, error) {
	if p.Empty() || !p.Valid() || len(parts) < 1 || len(parts) > api.MaxMultipartParts {
		return Snapshot{}, ErrInvalid
	}
	h := NewHash(p.Algorithm)
	var crc uint64
	var total int64
	for i, part := range parts {
		if part.Number != int32(i+1) || part.Size < 1 || part.Size > api.MaxObjectSinglePutBytes || !ValidPart(p.Algorithm, part.Value) || total > api.MaxObjectUploadBytes-part.Size {
			return Snapshot{}, ErrInvalid
		}
		total += part.Size
		b, _ := base64.StdEncoding.DecodeString(part.Value)
		if p.Type == Composite {
			_, _ = h.Write(b)
		} else {
			v := uint64(0)
			if len(b) == 4 {
				v = uint64(binary.BigEndian.Uint32(b))
			} else {
				v = binary.BigEndian.Uint64(b)
			}
			if i == 0 {
				crc = v
			} else {
				crc = combine(crc, v, uint64(part.Size), polynomial(p.Algorithm), h.Size()*8)
			}
		}
	}
	var value string
	if p.Type == Composite {
		value = base64.StdEncoding.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(parts))
	} else {
		b := make([]byte, h.Size())
		if len(b) == 4 {
			binary.BigEndian.PutUint32(b, uint32(crc))
		} else {
			binary.BigEndian.PutUint64(b, crc)
		}
		value = base64.StdEncoding.EncodeToString(b)
	}
	return Snapshot{Digest: Digest{Policy: p, Value: value}, Parts: slices.Clone(parts)}, nil
}

func (s Snapshot) Complete() bool {
	if s.Empty() {
		return s.Initial()
	}
	want, err := Build(s.Policy, s.Parts)
	return err == nil && want.Equal(s)
}

func polynomial(algorithm string) uint64 {
	switch algorithm {
	case "CRC32":
		return uint64(crc32.IEEE)
	case "CRC32C":
		return uint64(crc32.Castagnoli)
	}
	return NVMEPolynomial
}

// Apply the reflected CRC zero-byte operator in GF(2), then add the next CRC.
// This combines already verified parts without reading the object into memory.
func combine(a, b, size, poly uint64, width int) uint64 {
	var odd, even [64]uint64
	odd[0] = poly
	for i := 1; i < width; i++ {
		odd[i] = uint64(1) << (i - 1)
	}
	square(&even, &odd, width)
	square(&odd, &even, width)
	for size != 0 {
		square(&even, &odd, width)
		if size&1 != 0 {
			a = times(&even, a)
		}
		size >>= 1
		if size == 0 {
			break
		}
		square(&odd, &even, width)
		if size&1 != 0 {
			a = times(&odd, a)
		}
		size >>= 1
	}
	return a ^ b
}
func times(matrix *[64]uint64, v uint64) uint64 {
	var sum uint64
	for i := 0; v != 0; i++ {
		if v&1 != 0 {
			sum ^= matrix[i]
		}
		v >>= 1
	}
	return sum
}
func square(out, in *[64]uint64, width int) {
	for i := 0; i < width; i++ {
		out[i] = times(in, in[i])
	}
}
