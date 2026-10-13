package dbarchive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Remote is off-node object storage for the archive. Keys are
// <prefix>/<database-id>/wal/<name> and <prefix>/<database-id>/base/<name>.
type Remote interface {
	Put(ctx context.Context, key string, r io.ReadSeeker, size int64) error
	// Get writes the object to w; a missing object is ErrNotFound.
	Get(ctx context.Context, key string, w io.Writer) error
}

// WALKey is the remote key of a WAL file.
func WALKey(prefix, databaseID, name string) string {
	return strings.TrimSuffix(prefix, "/") + "/" + databaseID + "/wal/" + name
}

// BaseKey is the remote key of a base backup file (data or manifest).
func BaseKey(prefix, databaseID, name string) string {
	return strings.TrimSuffix(prefix, "/") + "/" + databaseID + "/base/" + name
}

// S3Config reaches an S3-compatible bucket.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	PathStyle bool
	KeyID     string
	Secret    string
}

// S3Remote stores archive objects in an S3-compatible bucket.
type S3Remote struct {
	client *s3.Client
	bucket string
}

// NewS3Remote builds the client; it makes no request.
func NewS3Remote(cfg S3Config) (*S3Remote, error) {
	if cfg.Bucket == "" || cfg.KeyID == "" || cfg.Secret == "" {
		return nil, errors.New("dbarchive: bucket and credentials are required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	opts := s3.Options{
		Region:       cfg.Region,
		UsePathStyle: cfg.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.KeyID, cfg.Secret, ""),
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	return &S3Remote{client: s3.New(opts), bucket: cfg.Bucket}, nil
}

func (r *S3Remote) Put(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("dbarchive: upload %s: %w", key, err)
	}
	return nil
}

func (r *S3Remote) Get(ctx context.Context, key string, w io.Writer) error {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	var missing *types.NoSuchKey
	if errors.As(err, &missing) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("dbarchive: download %s: %w", key, err)
	}
	defer func() { _ = out.Body.Close() }()
	_, err = io.Copy(w, out.Body)
	return err
}

// TieredStore keeps the archive on the node and off it: writes land
// locally (durable before PostgreSQL is told), the Uploader ships them,
// and reads fall back to the remote, so a database restores on any node
// and after the local copy is pruned.
type TieredStore struct {
	*LocalStore
	Remote Remote
	Prefix string
}

func (s *TieredStore) OpenWAL(ctx context.Context, databaseID, name string) (io.ReadCloser, int64, error) {
	rc, size, err := s.LocalStore.OpenWAL(ctx, databaseID, name)
	if !errors.Is(err, ErrNotFound) || s.Remote == nil {
		return rc, size, err
	}
	path, err := s.walPath(databaseID, name)
	if err != nil {
		return nil, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+name+".*.fetch")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	err = s.Remote.Get(ctx, WALKey(s.Prefix, databaseID, name), tmp)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, 0, err
	}
	// A fetched file is already uploaded: mark it so it is not re-sent.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, 0, err
	}
	_ = markUploaded(s.dir, databaseID, name)
	return s.LocalStore.OpenWAL(ctx, databaseID, name)
}

func uploadedMarker(dir, databaseID, name string) string {
	return filepath.Join(dir, databaseID, "uploaded", name)
}

func markUploaded(dir, databaseID, name string) error {
	marker := uploadedMarker(dir, databaseID, name)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // marker under the store root
	if err != nil {
		return err
	}
	return f.Close()
}

// Uploader ships locally archived WAL and base backups that are not yet
// off the node. A backup's data goes before its manifest, so a remote
// manifest always names complete data.
type Uploader struct {
	Store  *LocalStore
	Remote Remote
	Prefix string
	// OnPending reports the files still waiting and the age of the oldest:
	// the off-node recovery point.
	OnPending func(count int, oldest time.Duration)
	now       func() time.Time
}

// archiveFile is one local archive file and its remote key.
type archiveFile struct {
	name, path, key string
}

// files lists a database's archive files in upload order: WAL, then each
// backup's data before its manifest.
func (u *Uploader) files(databaseID string) []archiveFile {
	var out []archiveFile
	walDir := filepath.Join(u.Store.dir, databaseID, "wal")
	if entries, err := os.ReadDir(walDir); err == nil {
		for _, e := range entries {
			if ValidWALName(e.Name()) {
				out = append(out, archiveFile{e.Name(), filepath.Join(walDir, e.Name()), WALKey(u.Prefix, databaseID, e.Name())})
			}
		}
	}
	baseDir := filepath.Join(u.Store.dir, databaseID, "base")
	if entries, err := os.ReadDir(baseDir); err == nil {
		for _, e := range entries {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok || !ValidBackupID(id) {
				continue
			}
			for _, name := range []string{id + ".tar.zst", e.Name()} {
				out = append(out, archiveFile{name, filepath.Join(baseDir, name), BaseKey(u.Prefix, databaseID, name)})
			}
		}
	}
	return out
}

// RunOnce uploads every pending archive file and returns how many it
// shipped. A failed upload stops the pass; the next pass retries it.
func (u *Uploader) RunOnce(ctx context.Context) (int, error) {
	now := time.Now
	if u.now != nil {
		now = u.now
	}
	databases, err := os.ReadDir(u.Store.dir)
	if err != nil {
		return 0, err
	}
	shipped, pending := 0, 0
	var oldest time.Duration
	var firstErr error
	for _, db := range databases {
		if !db.IsDir() || !ValidDatabaseID(db.Name()) {
			continue
		}
		for _, f := range u.files(db.Name()) {
			if _, err := os.Stat(uploadedMarker(u.Store.dir, db.Name(), f.name)); err == nil {
				continue
			}
			if firstErr == nil {
				firstErr = u.upload(ctx, db.Name(), f)
				if firstErr == nil {
					shipped++
					continue
				}
			}
			pending++
			if info, err := os.Stat(f.path); err == nil && now().Sub(info.ModTime()) > oldest {
				oldest = now().Sub(info.ModTime())
			}
		}
	}
	if u.OnPending != nil {
		u.OnPending(pending, oldest)
	}
	return shipped, firstErr
}

func (u *Uploader) upload(ctx context.Context, databaseID string, file archiveFile) error {
	f, err := os.Open(file.path) //nolint:gosec,forbidigo // a path listed under the store root
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := u.Remote.Put(ctx, file.key, f, info.Size()); err != nil {
		return err
	}
	return markUploaded(u.Store.dir, databaseID, file.name)
}

// Run uploads every interval until ctx ends.
func (u *Uploader) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := u.RunOnce(ctx); err != nil && onError != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
