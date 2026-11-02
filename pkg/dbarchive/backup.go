package dbarchive

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Backup ops.
const (
	// OpPutBackup streams a pg_basebackup tar as chunks; dbbackupd names
	// the backup and returns its id in Response.Name.
	OpPutBackup = "put_backup"
	// OpGetBackup streams, as chunks, the newest backup of the restore
	// source that is consistent before Request.Target (RFC 3339).
	OpGetBackup = "get_backup"
)

// MaxChunkBytes bounds one chunk of a backup stream.
const MaxChunkBytes = 1 << 20

// backupID is the UTC instant dbbackupd received the backup.
var backupID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)

// ValidBackupID reports whether id names a base backup.
func ValidBackupID(id string) bool { return backupID.MatchString(id) }

// BackupManifest describes one base backup, from its backup_label.
type BackupManifest struct {
	ID        string    `json:"id"`
	StartWAL  string    `json:"start_wal"`
	StartLSN  string    `json:"start_lsn"`
	StartTime time.Time `json:"start_time"`
	Timeline  int       `json:"timeline"`
	// Bytes is the uncompressed tar size.
	Bytes int64 `json:"bytes"`
}

// writeChunks copies r to w as length-prefixed chunks ending with an empty
// one.
func writeChunks(w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, MaxChunkBytes)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(n))
			if _, werr := w.Write(hdr[:]); werr != nil {
				return total, werr
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if errors.Is(err, io.EOF) {
			_, werr := w.Write([]byte{0, 0, 0, 0})
			return total, werr
		}
		if err != nil {
			return total, err
		}
	}
}

// chunkReader reads a chunked stream until its empty terminating chunk.
type chunkReader struct {
	r    io.Reader
	left uint32
	done bool
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	if c.left == 0 {
		var hdr [4]byte
		if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
			return 0, io.ErrUnexpectedEOF
		}
		c.left = binary.BigEndian.Uint32(hdr[:])
		if c.left == 0 {
			c.done = true
			return 0, io.EOF
		}
		if c.left > MaxChunkBytes {
			return 0, errors.New("dbarchive: chunk too large")
		}
	}
	if uint32(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= uint32(n)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, err
	}
	if errors.Is(err, io.EOF) && c.left > 0 {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (s *LocalStore) baseDir(databaseID string) (string, error) {
	if !ValidDatabaseID(databaseID) {
		return "", fmt.Errorf("dbarchive: invalid database id %q", databaseID)
	}
	return filepath.Join(s.dir, databaseID, "base"), nil
}

// PutBackup stores a pg_basebackup tar stream compressed, with its
// manifest, and returns the backup id. Nothing is visible until both are
// durable.
func (s *LocalStore) PutBackup(_ context.Context, databaseID string, r io.Reader, now time.Time) (BackupManifest, error) {
	dir, err := s.baseDir(databaseID)
	if err != nil {
		return BackupManifest{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupManifest{}, err
	}
	id := now.UTC().Format("20060102T150405Z")
	final := filepath.Join(dir, id+".tar.zst")
	if _, err := os.Stat(final); err == nil {
		return BackupManifest{}, ErrConflict
	}
	tmp, err := os.CreateTemp(dir, "."+id+".*.tmp")
	if err != nil {
		return BackupManifest{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	enc, err := zstd.NewWriter(tmp)
	if err != nil {
		_ = tmp.Close()
		return BackupManifest{}, err
	}
	// Tee the stream into the tar reader that finds backup_label.
	pr, pw := io.Pipe()
	labelCh := make(chan BackupManifest, 1)
	go func() {
		label, _ := readBackupLabel(pr)
		_, _ = io.Copy(io.Discard, pr)
		labelCh <- label
	}()
	n, copyErr := io.Copy(io.MultiWriter(enc, pw), r)
	_ = pw.CloseWithError(copyErr)
	manifest := <-labelCh
	err = copyErr
	if closeErr := enc.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return BackupManifest{}, fmt.Errorf("dbarchive: receive backup: %w", err)
	}
	if manifest.StartWAL == "" || manifest.StartTime.IsZero() {
		return BackupManifest{}, errors.New("dbarchive: backup has no backup_label")
	}
	manifest.ID, manifest.Bytes = id, n
	raw, err := json.Marshal(manifest)
	if err != nil {
		return BackupManifest{}, err
	}
	if err := writeFileSync(filepath.Join(dir, id+".json"), raw); err != nil {
		return BackupManifest{}, err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return BackupManifest{}, err
	}
	return manifest, syncDir(dir)
}

// readBackupLabel scans a base tar for backup_label.
func readBackupLabel(r io.Reader) (BackupManifest, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return BackupManifest{}, err
		}
		if hdr.Name != "backup_label" {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, 64<<10))
		if err != nil {
			return BackupManifest{}, err
		}
		return parseBackupLabel(string(body))
	}
}

var (
	labelStartWAL  = regexp.MustCompile(`(?m)^START WAL LOCATION: ([0-9A-F]+/[0-9A-F]+) \(file ([0-9A-F]{24})\)$`)
	labelStartTime = regexp.MustCompile(`(?m)^START TIME: (.+)$`)
	labelTimeline  = regexp.MustCompile(`(?m)^START TIMELINE: ([0-9]+)$`)
)

func parseBackupLabel(label string) (BackupManifest, error) {
	var m BackupManifest
	if g := labelStartWAL.FindStringSubmatch(label); g != nil {
		m.StartLSN, m.StartWAL = g[1], g[2]
	}
	if g := labelStartTime.FindStringSubmatch(label); g != nil {
		t, err := parseLabelTime(g[1])
		if err != nil {
			return m, err
		}
		m.StartTime = t
	}
	if g := labelTimeline.FindStringSubmatch(label); g != nil {
		_, _ = fmt.Sscanf(g[1], "%d", &m.Timeline)
	}
	if m.StartWAL == "" || m.StartTime.IsZero() {
		return m, errors.New("dbarchive: incomplete backup_label")
	}
	return m, nil
}

// parseLabelTime reads PostgreSQL's "2006-01-02 15:04:05 MST" timestamps.
func parseLabelTime(s string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05 MST", strings.TrimSpace(s))
}

// ListBackups returns the database's base backups, oldest first.
func (s *LocalStore) ListBackups(databaseID string) ([]BackupManifest, error) {
	dir, err := s.baseDir(databaseID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupManifest
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidBackupID(id) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, id+".tar.zst")); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // manifest under the store root
		if err != nil {
			return nil, err
		}
		var m BackupManifest
		if json.Unmarshal(raw, &m) == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// OpenBackup returns a backup's uncompressed tar.
func (s *LocalStore) OpenBackup(_ context.Context, databaseID, id string) (io.ReadCloser, error) {
	dir, err := s.baseDir(databaseID)
	if err != nil || !ValidBackupID(id) {
		return nil, fmt.Errorf("dbarchive: invalid backup reference")
	}
	f, err := os.Open(filepath.Join(dir, id+".tar.zst")) //nolint:gosec,forbidigo // validated id under the store root
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &decodeCloser{dec: dec, f: f}, nil
}

type decodeCloser struct {
	dec *zstd.Decoder
	f   *os.File
}

func (d *decodeCloser) Read(p []byte) (int, error) { return d.dec.Read(p) }
func (d *decodeCloser) Close() error               { d.dec.Close(); return d.f.Close() }

// backupStopTime reads the stop time from the backup history file the
// archive received at pg_backup_stop: recovery is consistent only past it.
// wal reads the history file (a TieredStore falls back to the remote).
func backupStopTime(ctx context.Context, wal Store, databaseID string, m BackupManifest) (time.Time, error) {
	parts := strings.SplitN(m.StartLSN, "/", 2)
	if len(parts) != 2 {
		return time.Time{}, errors.New("dbarchive: bad start lsn")
	}
	// The history file is <start wal>.<offset within the segment>.backup;
	// database VMs use PostgreSQL's default 16 MiB segments.
	var lo uint64
	if _, err := fmt.Sscanf(parts[1], "%X", &lo); err != nil {
		return time.Time{}, err
	}
	name := fmt.Sprintf("%s.%08X.backup", m.StartWAL, lo%(16<<20))
	rc, _, err := wal.OpenWAL(ctx, databaseID, name)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, 64<<10))
	if err != nil {
		return time.Time{}, err
	}
	g := regexp.MustCompile(`(?m)^STOP TIME: (.+)$`).FindStringSubmatch(string(body))
	if g == nil {
		return time.Time{}, errors.New("dbarchive: backup history has no stop time")
	}
	return parseLabelTime(g[1])
}

// SelectBackup returns the newest backup of databaseID that is consistent
// (its stop time) at or before target; wal reads backup history files.
func (s *LocalStore) SelectBackup(ctx context.Context, wal Store, databaseID string, target time.Time) (BackupManifest, error) {
	backups, err := s.ListBackups(databaseID)
	if err != nil {
		return BackupManifest{}, err
	}
	return selectBackup(ctx, wal, databaseID, target, backups)
}

// selectBackup picks from backups (oldest first).
func selectBackup(ctx context.Context, wal Store, databaseID string, target time.Time, backups []BackupManifest) (BackupManifest, error) {
	for i := len(backups) - 1; i >= 0; i-- {
		stop, err := backupStopTime(ctx, wal, databaseID, backups[i])
		if err == nil && !stop.After(target) {
			return backups[i], nil
		}
	}
	return BackupManifest{}, ErrNotFound
}

func writeFileSync(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // under the store root
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// PutBackup streams a backup tar to the host and returns its id.
func (c *Client) PutBackup(r io.Reader) (string, error) {
	if err := WriteLine(c.rw, Request{Op: OpPutBackup}); err != nil {
		return "", err
	}
	bw := bufio.NewWriterSize(c.rw, 256<<10)
	if _, err := writeChunks(bw, r); err != nil {
		return "", err
	}
	if err := bw.Flush(); err != nil {
		return "", err
	}
	var resp Response
	if err := ReadLine(c.br, &resp); err != nil {
		return "", err
	}
	return resp.Name, errorFor(resp.Error)
}

// GetBackup streams the restore source's backup consistent before target
// into w and returns its id.
func (c *Client) GetBackup(target time.Time, w io.Writer) (string, error) {
	if err := WriteLine(c.rw, Request{Op: OpGetBackup, Target: target.UTC().Format(time.RFC3339Nano)}); err != nil {
		return "", err
	}
	var resp Response
	if err := ReadLine(c.br, &resp); err != nil {
		return "", err
	}
	if err := errorFor(resp.Error); err != nil {
		return "", err
	}
	_, err := io.Copy(w, &chunkReader{r: c.br})
	return resp.Name, err
}
