package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"
)

// Source identifies the exact frozen bytes, not just a migration's ledger ID.
type Source struct {
	Version  int64  `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

func Sources() ([]Source, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, err
	}
	out := make([]Source, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		if err != nil {
			return nil, err
		}
		raw, err := FS.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(raw)
		out = append(out, Source{Version: version, Filename: entry.Name(), SHA256: hex.EncodeToString(digest[:])})
	}
	return out, nil
}

func SourceDigest() (string, error) {
	sources, err := Sources()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
