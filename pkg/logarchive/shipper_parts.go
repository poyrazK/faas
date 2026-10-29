package logarchive

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
)

// partGzipSuffix names the compressed upload while it is sent. It does not
// end in archiveGzipSuffix, so the retention purger never mistakes it for
// shipped history.
const partGzipSuffix = ".part.gz"

// uploadPart sends one sealed upload as its own part object under
// DayPartPrefix (ADR-20261028110000) and then drops it: no earlier flush is
// re-sent and no local history is kept, so a day costs one upload of each
// line and the spool holds only what is not yet in the bucket.
func (s *Shipper) uploadPart(ctx context.Context, f FileInfo) (int64, error) {
	path, err := s.spool.PreparePartUpload(f.Instance, f.Day)
	if err != nil {
		return 0, fmt.Errorf("prepare %s: %w", f.Path, err)
	}
	if path == "" {
		return 0, nil
	}
	//nolint:forbidigo // path is the spool-owned sealed upload PreparePartUpload returned.
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	gzPath := strings.TrimSuffix(path, spoolUploadSuffix) + partGzipSuffix
	digest, size, err := gzipUpload(path, gzPath)
	defer func() { _ = os.Remove(gzPath) }()
	if err != nil {
		return 0, err
	}
	key := partKey(f.Instance, f.Day, info.ModTime().UnixNano(), digest)
	//nolint:forbidigo // gzPath is the compressed sibling of a spool-owned upload.
	body, err := os.Open(gzPath)
	if err != nil {
		return 0, fmt.Errorf("open %s for upload: %w", gzPath, err)
	}
	defer func() { _ = body.Close() }()
	uploadStart := s.now()
	err = s.s3.PutObject(ctx, key, "application/gzip", body, size)
	s.metrics.ObserveUploadDuration(s.now().Sub(uploadStart).Seconds())
	if err != nil {
		return 0, fmt.Errorf("put %s: %w", key, err)
	}
	if err := s.spool.CompleteUpload(path); err != nil {
		return 0, err
	}
	return size, nil
}

// partKey names a part by the upload's seal time and content. Key order is
// write order, and a retry of an upload that reached the bucket before the
// shipper could drop it rewrites the same object instead of duplicating it.
func partKey(instance, day string, sealedUnixNano int64, digest []byte) string {
	return fmt.Sprintf("%s%019d-%x%s", DayPartPrefix(instance, day), sealedUnixNano, digest[:8], archiveGzipSuffix)
}

// gzipUpload compresses src into dst and returns the SHA-256 of src's bytes
// and dst's size.
func gzipUpload(src, dst string) ([]byte, int64, error) {
	//nolint:forbidigo // src is a spool-owned sealed upload.
	in, err := os.Open(src)
	if err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	//nolint:forbidigo // dst is the compressed sibling of a spool-owned upload.
	out, err := os.Create(dst)
	if err != nil {
		return nil, 0, fmt.Errorf("create %s: %w", dst, err)
	}
	hash := sha256.New()
	gz, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		_ = out.Close()
		return nil, 0, fmt.Errorf("gzip writer: %w", err)
	}
	if _, err := io.Copy(gz, io.TeeReader(in, hash)); err != nil {
		_ = gz.Close()
		_ = out.Close()
		return nil, 0, fmt.Errorf("gzip copy: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = out.Close()
		return nil, 0, fmt.Errorf("gzip close: %w", err)
	}
	size, err := out.Seek(0, io.SeekCurrent)
	if err != nil {
		_ = out.Close()
		return nil, 0, fmt.Errorf("size %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return nil, 0, fmt.Errorf("close %s: %w", dst, err)
	}
	return hash.Sum(nil), size, nil
}
