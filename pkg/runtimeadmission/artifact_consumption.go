package runtimeadmission

// adr: 431. Protocol 2 separates measured native consumption from source input.

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

const ArtifactProtocolVersion = 2

type ConsumedDrive struct {
	Source         ArtifactSource `json:"source"`
	DriveID        string         `json:"drive_id"`
	ReadOnly       bool           `json:"read_only"`
	RootDevice     bool           `json:"root_device"`
	ProducerDigest string         `json:"producer_digest"`
	ProducerBytes  int64          `json:"producer_bytes"`
	InjectedDigest string         `json:"injected_digest"`
	InjectedBytes  int64          `json:"injected_bytes"`
}

type ArtifactConsumption struct {
	ConfigHash   string          `json:"config_hash"`
	ProcessPID   uint32          `json:"process_pid"`
	ProcessStart string          `json:"process_start"`
	Drives       []ConsumedDrive `json:"drives"`
}

func (c ArtifactConsumption) IsZero() bool {
	return c.ConfigHash == "" && c.ProcessPID == 0 && c.ProcessStart == "" && len(c.Drives) == 0
}

func (c ArtifactConsumption) Equal(other ArtifactConsumption) bool {
	return c.ConfigHash == other.ConfigHash && c.ProcessPID == other.ProcessPID && c.ProcessStart == other.ProcessStart && slices.Equal(c.Drives, other.Drives)
}

func (c ArtifactConsumption) Clone() ArtifactConsumption {
	c.Drives = slices.Clone(c.Drives)
	return c
}

func (d ConsumedDrive) valid() bool {
	if !d.Source.Valid() || d.DriveID == "" || len(d.DriveID) > api.ApplicationStandardBaseMaxStorageKeyBytes || strings.ContainsAny(d.DriveID, "\x00\r\n") {
		return false
	}
	if d.ReadOnly != (d.Source.Role() != "main") || d.RootDevice != (d.Source.Role() == "base") || d.ProducerDigest != d.Source.Digest || d.ProducerBytes != d.Source.Bytes || d.InjectedBytes != d.Source.Bytes || ociref.ValidateDigest(d.InjectedDigest) != nil {
		return false
	}
	return !d.ReadOnly || d.InjectedDigest == d.ProducerDigest
}

func (c ArtifactConsumption) Check(sourceHash string) error {
	start, err := strconv.ParseUint(c.ProcessStart, 10, 64)
	if !ValidHash(c.ConfigHash) || c.ProcessPID == 0 || c.ProcessPID > math.MaxInt32 || err != nil || start == 0 || strconv.FormatUint(start, 10) != c.ProcessStart || len(c.Drives) < 2 || len(c.Drives) > api.SidecarCapMax+2 {
		return ErrInvalid
	}
	sources, ids := make([]ArtifactSource, 0, len(c.Drives)), map[string]bool{}
	for _, drive := range c.Drives {
		if !drive.valid() || ids[drive.DriveID] {
			return ErrInvalid
		}
		ids[drive.DriveID] = true
		sources = append(sources, drive.Source)
	}
	actual, err := HashArtifactSources(sources)
	if err != nil || actual != sourceHash {
		return ErrInvalid
	}
	return nil
}

// HashArtifactSources is ordered by role and uses length-framed UTF-8 bytes and
// unsigned big-endian counts. SQL uses the same format without JSON serialization.
func HashArtifactSources(sources []ArtifactSource) (string, error) {
	ordered, err := canonicalArtifactSources(sources)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("gregale.native-artifact-sources.v2\x00"))
	writeArtifactSourceNumber(h, uint64(len(ordered)))
	for _, source := range ordered {
		for _, field := range []string{source.Kind, source.WorkloadName, source.StorageKey, source.Digest} {
			writeArtifactSourceNumber(h, uint64(len(field)))
			_, _ = h.Write([]byte(field))
		}
		writeArtifactSourceNumber(h, uint64(source.Bytes))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func canonicalArtifactSources(sources []ArtifactSource) ([]ArtifactSource, error) {
	if len(sources) < 2 || len(sources) > api.SidecarCapMax+2 {
		return nil, ErrInvalid
	}
	roles, keys := map[string]bool{}, map[string]bool{}
	for _, source := range sources {
		if !source.Valid() || roles[source.Role()] || keys[source.StorageKey] {
			return nil, ErrInvalid
		}
		roles[source.Role()], keys[source.StorageKey] = true, true
	}
	if !roles["base"] || !roles["main"] {
		return nil, ErrInvalid
	}
	ordered := slices.Clone(sources)
	slices.SortFunc(ordered, func(a, b ArtifactSource) int { return cmp.Compare(a.Role(), b.Role()) })
	return ordered, nil
}

func writeArtifactSourceNumber(h hash.Hash, number uint64) {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], number)
	_, _ = h.Write(value[:])
}
