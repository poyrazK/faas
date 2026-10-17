package objectnode

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Receipts recover first, so a completion's assembled blob becomes current
// before any abandoned assembly, part or object bytes can be reclaimed.
func (s *Store) recoverMultipart() error {
	directory := filepath.Join(s.root, "multipart")
	err := visitDirectory(directory, func(e os.DirEntry) error {
		if !validReceiptID(e.Name()) || !e.IsDir() {
			return ErrCorrupt
		}
		u, err := s.readMultipart(e.Name())
		if errors.Is(err, ErrNoUpload) {
			// Initiation did not publish a session. Never erase a directory with
			// parts that could have been acknowledged under a missing catalog.
			parts, readErr := os.ReadDir(filepath.Join(s.multipartPath(e.Name()), "parts"))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) || len(parts) != 0 {
				return ErrCorrupt
			}
			return os.RemoveAll(s.multipartPath(e.Name()))
		}
		if err != nil {
			return err
		}
		if terminal, err := s.settleMultipart(&u); err != nil {
			return err
		} else if terminal {
			return nil
		}
		if u.Status == multipartAborted || u.Status == multipartExpired {
			return s.cleanMultipartParts(u.ID)
		}
		b, err := s.bucket(u.Bucket)
		if err != nil || b.Generation != u.Generation {
			return ErrCorrupt
		}
		if err = s.recoverParts(u); err != nil {
			return err
		}
		if !time.Now().Before(u.ExpiresAt) {
			u.Status = multipartExpired
			if err = s.writeMultipart(u); err != nil {
				return err
			}
			return s.cleanMultipartParts(u.ID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return syncDirectory(directory)
}

func (s *Store) recoverParts(u MultipartUpload) error {
	if u.Completion != nil {
		m, _, err := s.completionManifest(u, MultipartCompletion{Parts: u.Completion.Parts, IfMatch: u.Completion.IfMatch, IfNoneMatch: u.Completion.IfNoneMatch})
		if err != nil || m.Hash() != u.Completion.Hash() {
			return ErrCorrupt
		}
	}
	directory := s.multipartPath(u.ID)
	if err := visitDirectory(directory, func(e os.DirEntry) error {
		if strings.HasPrefix(e.Name(), ".tmp-") && validReceiptID(strings.TrimPrefix(e.Name(), ".tmp-")) {
			return os.Remove(filepath.Join(directory, e.Name()))
		}
		if e.Name() != "session.json" && e.Name() != "parts" {
			return ErrCorrupt
		}
		return nil
	}); err != nil {
		return err
	}
	parts := filepath.Join(directory, "parts")
	err := visitDirectory(parts, func(e os.DirEntry) error {
		number, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || strconv.FormatInt(number, 10) != e.Name() || number < 1 || number > api.MaxMultipartParts {
			return ErrCorrupt
		}
		part, err := s.readPart(u.ID, int32(number))
		dir := filepath.Join(parts, e.Name())
		if errors.Is(err, ErrInvalidPart) {
			return os.RemoveAll(dir)
		}
		if err != nil {
			return err
		}
		if part.Object.Key != u.Key {
			return ErrCorrupt
		}
		if err = verifyReceiptBlob(filepath.Join(dir, part.Object.ID+".data"), part.Object, false); err != nil {
			return err
		}
		if err = visitDirectory(dir, func(file os.DirEntry) error {
			if file.Name() == "current.json" || file.Name() == part.Object.ID+".data" {
				return nil
			}
			return os.RemoveAll(filepath.Join(dir, file.Name()))
		}); err != nil {
			return err
		}
		return syncDirectory(dir)
	})
	if err != nil {
		return err
	}
	return syncDirectory(parts)
}
