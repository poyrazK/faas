package logarchive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/state"
)

type archiveOpener interface {
	OpenObject(context.Context, string) (io.ReadCloser, error)
}

func archiveMatches(path string, ref state.RuntimeLogArchive) (bool, error) {
	//nolint:forbidigo // path is a validated spool-owned daily gzip.
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() != ref.Bytes {
		return false, nil
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(digest.Sum(nil)) == ref.SHA256, nil
}

// Receipts outlive local commit and later writers. An accepted sealed fragment
// is consumed before newer partials merge, even when another node has published
// a newer cumulative object in the meantime.
func (s *Shipper) recoverPublishedArchive(ctx context.Context, f FileInfo) error {
	if s.catalog == nil {
		return nil
	}
	base := filepath.Join(s.spool.root, f.Instance, f.Day[:4], f.Day[5:7], spoolFilePrefix+f.Day)
	upload := base + spoolUploadSuffix
	digest, err := archiveFileSHA256(upload)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	accepted, err := s.catalog.RuntimeLogArchiveFragmentPublished(ctx, f.Instance, f.Day, digest)
	if err != nil {
		return err
	}
	if !accepted {
		return nil
	}
	if err := s.spool.CompleteUpload(upload); err != nil {
		return err
	}
	if err := os.Remove(base + archiveGzipSuffix + ".pending"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncArchiveDir(base)
}

func archiveFileSHA256(path string) (string, error) {
	//nolint:forbidigo // path is a sealed fragment under the spool root.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// A migrated instance or a lost spool resumes from the verified remote daily
// object. The app publication lock orders this replacement against other nodes.
func (s *Shipper) copyOwnedArchiveHistory(ctx context.Context, dst io.Writer, path string, ref state.RuntimeLogArchive) error {
	if !ref.Published() {
		return copyArchiveHistory(dst, path)
	}
	match, err := archiveMatches(path, ref)
	if err != nil {
		return err
	}
	if match {
		return copyArchiveHistory(dst, path)
	}
	client, ok := s.s3.(archiveOpener)
	if !ok {
		return errors.New("logarchive: archive store cannot recover published history")
	}
	body, err := client.OpenObject(ctx, ref.StorageKey)
	if err != nil {
		return fmt.Errorf("recover remote archive: %w", err)
	}
	defer func() { _ = body.Close() }()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, digest), io.LimitReader(body, ref.Bytes))
	if err != nil {
		return fmt.Errorf("copy remote archive: %w", err)
	}
	var extra [1]byte
	count, tailErr := body.Read(extra[:])
	if n != ref.Bytes || count != 0 || !errors.Is(tailErr, io.EOF) || hex.EncodeToString(digest.Sum(nil)) != ref.SHA256 {
		return errors.New("logarchive: published history failed integrity check")
	}
	return nil
}
