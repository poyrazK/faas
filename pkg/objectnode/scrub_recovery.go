package objectnode

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Progress and findings are independent of object publication. Old worklist
// targets are revalidated after ordinary startup garbage collection.
func (s *Store) recoverScrub() error {
	if err := os.Mkdir(s.scrubPath("findings"), 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := syncDirectory(s.scrubPath("")); err != nil {
		return err
	}
	p, err := s.readScrubState()
	if err != nil {
		return err
	}
	if p.State == "running" {
		info, err := os.Lstat(s.scrubPath(p.RunID + ".jsonl"))
		if err != nil || !info.Mode().IsRegular() || info.Size() != p.ManifestBytes {
			return ErrCorrupt
		}
		f, err := os.Open(s.scrubPath(p.RunID + ".jsonl")) //nolint:forbidigo // Validated private worklist UUID, bounded before hashing.
		if err != nil {
			return ErrCorrupt
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(f, api.ObjectNodeScrubManifestMaxBytes+1))
		_ = f.Close()
		if copyErr != nil || n != p.ManifestBytes || hex.EncodeToString(h.Sum(nil)) != p.ManifestSHA256 {
			return ErrCorrupt
		}
	}
	if err = visitDirectory(s.scrubPath("findings"), func(e os.DirEntry) error {
		if !e.Type().IsRegular() {
			return ErrCorrupt
		}
		if strings.HasPrefix(e.Name(), ".tmp-") && validReceiptID(strings.TrimPrefix(e.Name(), ".tmp-")) {
			return os.Remove(s.scrubPath("findings/" + e.Name()))
		}
		_, err := s.readScrubFinding(strings.TrimSuffix(e.Name(), ".json"))
		return err
	}); err != nil {
		return err
	}
	if err = visitDirectory(s.scrubPath(""), func(e os.DirEntry) error {
		name := e.Name()
		if name == "findings" || name == "progress.json" || p.State == "running" && name == p.RunID+".jsonl" {
			return nil
		}
		if strings.HasPrefix(name, ".tmp-") && validReceiptID(strings.TrimPrefix(name, ".tmp-")) || strings.HasSuffix(name, ".jsonl") && validReceiptID(strings.TrimSuffix(name, ".jsonl")) {
			return os.Remove(s.scrubPath(name))
		}
		return ErrCorrupt
	}); err != nil {
		return err
	}
	return syncDirectory(s.scrubPath(""))
}
