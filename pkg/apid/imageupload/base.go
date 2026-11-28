package imageupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/storage"
)

// An app's image base is its latest uploaded image, kept so a redeploy can
// leave out the layers it shares (ADR-20261127120000 amendment 1). Each app
// has at most one: image-bases/<app_id>.tar, the canonical archive, and
// image-bases/<app_id>.json, its api.ImageBase summary.

// ErrNoBase means the app has no stored image base.
var ErrNoBase = errors.New("imageupload: the app has no image base")

// BaseStore keeps image bases in the deployment's storage backend.
type BaseStore struct {
	be storage.StorageBackend

	// saves orders SaveAsync per app: one save at a time, and a save
	// that a newer commit overtook is dropped.
	mu    sync.Mutex
	seq   uint64
	saves map[string]*appSaves
}

type appSaves struct {
	mu     sync.Mutex
	latest uint64 // newest commit's sequence number, guarded by BaseStore.mu
}

// NewBaseStore stores bases in be.
func NewBaseStore(be storage.StorageBackend) *BaseStore {
	return &BaseStore{be: be, saves: map[string]*appSaves{}}
}

// FullPath is where a thin upload's rebuilt archive sits next to its .part
// file. The thin .part stays as uploaded, so its checksum still verifies and
// a retried commit rebuilds again; removing the upload removes both.
func FullPath(partPath string) string { return partPath + ".full" }

// Rebuild and staging temps in the spool. sourcespool removes leftovers
// (a crash mid-rebuild) by these names.
const (
	baseTempPattern = ".image-base-*.tar"
	fullTempPattern = ".image-full-*.tar"
)

// CommitWorkBytes is how many bytes a commit may have to process: a thin
// upload is rebuilt to at most the largest plan's image cap.
func CommitWorkBytes(totalSize int64, reuse bool) int64 {
	if reuse {
		return max(totalSize, api.MustLimitsFor(api.PlanScale).ImageArchiveMaxBytes())
	}
	return totalSize
}

// OpenBaseStore uses the remote backend a split-box install selects
// (FAAS_STORAGE_BACKEND oci, gcs or s3), else a directory under the upload
// spool, which a single-box install shares.
func OpenBaseStore(ctx context.Context, spoolRoot string) (*BaseStore, error) {
	if storage.IsRemoteBackendKind(os.Getenv("FAAS_STORAGE_BACKEND")) {
		be, err := storage.BackendFromEnvContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("image base storage: %w", err)
		}
		return NewBaseStore(be), nil
	}
	be, err := storage.NewLocalStorageBackend(spoolRoot)
	if err != nil {
		return nil, fmt.Errorf("image base storage: %w", err)
	}
	return NewBaseStore(be), nil
}

func archiveKey(appID string) string { return "image-bases/" + appID + ".tar" }
func summaryKey(appID string) string { return "image-bases/" + appID + ".json" }

// Summary returns the app's image base, or ErrNoBase.
func (s *BaseStore) Summary(ctx context.Context, appID string) (api.ImageBase, error) {
	rc, err := s.be.Get(ctx, summaryKey(appID))
	if err != nil {
		if storage.IsNotFound(err) {
			return api.ImageBase{}, ErrNoBase
		}
		return api.ImageBase{}, fmt.Errorf("read image base summary: %w", err)
	}
	defer func() { _ = rc.Close() }()
	var base api.ImageBase
	if err := json.NewDecoder(io.LimitReader(rc, api.LocalOCIMaxManifestBytes)).Decode(&base); err != nil {
		return api.ImageBase{}, fmt.Errorf("decode image base summary: %w", err)
	}
	return base, nil
}

// Fetch downloads the app's base archive into a new file in dir, which the
// caller removes. It returns ErrNoBase when there is none.
func (s *BaseStore) Fetch(ctx context.Context, appID, dir string) (*os.File, error) {
	rc, err := s.be.Get(ctx, archiveKey(appID))
	if err != nil {
		if storage.IsNotFound(err) {
			return nil, ErrNoBase
		}
		return nil, fmt.Errorf("read image base: %w", err)
	}
	defer func() { _ = rc.Close() }()
	f, err := os.CreateTemp(dir, baseTempPattern) //nolint:forbidigo // server-owned upload spool dir.
	if err != nil {
		return nil, fmt.Errorf("stage image base: %w", err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("stage image base: %w", err)
	}
	return f, nil
}

// Save stores the accepted archive at path as the app's base, replacing the
// one before. The archive goes first, so a summary never names layers its
// archive lacks for long; a commit that finds one missing answers
// image_base_changed and the CLI uploads in full.
func (s *BaseStore) Save(ctx context.Context, appID, path string, summary ociarchive.Summary) error {
	f, err := os.Open(path) //nolint:forbidigo // server-created upload spool path.
	if err != nil {
		return fmt.Errorf("open accepted archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := s.be.Put(ctx, archiveKey(appID), f); err != nil {
		return fmt.Errorf("store image base: %w", err)
	}
	base := api.ImageBase{ManifestDigest: summary.ManifestDigest}
	for _, l := range summary.LayerDescriptors {
		base.Layers = append(base.Layers, api.ImageBaseLayer{Digest: l.Digest, Size: l.Size})
	}
	body, err := json.Marshal(base)
	if err != nil {
		return err
	}
	if err := s.be.Put(ctx, summaryKey(appID), bytes.NewReader(body)); err != nil {
		return fmt.Errorf("store image base summary: %w", err)
	}
	return nil
}

// Delete removes the app's base, if any.
func (s *BaseStore) Delete(ctx context.Context, appID string) error {
	var errs []error
	for _, key := range []string{summaryKey(appID), archiveKey(appID)} {
		if err := s.be.Delete(ctx, key); err != nil && !storage.IsNotFound(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RebuildProblem rebuilds the full archive from the thin upload at partPath
// and the app's base into FullPath(partPath), leaving the upload as it is,
// and returns the full archive's size. Anything wrong on the base's side (a
// missing layer, no base, a storage error) answers image_base_changed, which
// the CLI answers with a full upload; image_archive_invalid is kept for
// faults in the uploaded bytes.
func (s *BaseStore) RebuildProblem(ctx context.Context, appID, partPath string, maxBytes int64) (int64, *api.Problem) {
	dir := filepath.Dir(partPath)
	base, err := s.Fetch(ctx, appID, dir)
	if err != nil {
		return 0, api.ErrImageBaseChanged()
	}
	defer func() { _ = base.Close(); _ = os.Remove(base.Name()) }()
	info, err := base.Stat()
	if err != nil {
		return 0, api.ErrImageBaseChanged()
	}
	idx, err := ociarchive.IndexBase(base, info.Size())
	if err != nil {
		return 0, api.ErrImageBaseChanged()
	}
	thin, err := os.Open(partPath) //nolint:forbidigo // server-created upload spool path.
	if err != nil {
		return 0, api.ErrImageArchiveInvalid("the uploaded archive is unreadable")
	}
	defer func() { _ = thin.Close() }()
	full, err := os.CreateTemp(dir, fullTempPattern) //nolint:forbidigo // server-owned upload spool dir.
	if err != nil {
		return 0, api.ErrImageArchiveInvalid("the uploaded archive could not be staged")
	}
	fullPath := full.Name()
	defer func() { _ = os.Remove(fullPath) }()
	_, err = ociarchive.Rebuild(thin, idx, &capWriter{w: full, left: maxBytes})
	closeErr := full.Close()
	switch {
	case errors.Is(err, ociarchive.ErrBaseMissing):
		return 0, api.ErrImageBaseChanged()
	case errors.Is(err, errOverCap):
		return 0, api.ErrImageArchiveInvalid(fmt.Sprintf("the image is larger than the plan's %d MB cap", maxBytes>>20))
	case errors.Is(err, ociarchive.ErrInvalid):
		return 0, api.ErrImageArchiveInvalid(err.Error())
	case err != nil || closeErr != nil:
		return 0, api.ErrImageBaseChanged()
	}
	if err := os.Rename(fullPath, FullPath(partPath)); err != nil { //nolint:forbidigo // server-owned upload spool paths.
		return 0, api.ErrImageArchiveInvalid("the rebuilt image could not be staged")
	}
	st, err := os.Stat(FullPath(partPath)) //nolint:forbidigo // server-owned upload spool path.
	if err != nil {
		return 0, api.ErrImageArchiveInvalid("the rebuilt image could not be staged")
	}
	return st.Size(), nil
}

// SaveAsync stores the accepted archive as the app's base in the background.
// It is best effort: on failure the next upload sends every layer. Saves of
// one app run one at a time in commit order, and one a newer commit
// overtook is skipped, so the summary and archive come from the same image
// and the newest image wins. (Two apid replicas can still interleave; a
// summary naming layers the archive lacks only costs a full upload.)
func (s *BaseStore) SaveAsync(ctx context.Context, appID, path string, summary ociarchive.Summary, onErr func(error)) {
	s.mu.Lock()
	s.seq++
	mine := s.seq
	app := s.saves[appID]
	if app == nil {
		app = &appSaves{}
		s.saves[appID] = app
	}
	app.latest = mine
	s.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	go func() {
		app.mu.Lock()
		defer app.mu.Unlock()
		s.mu.Lock()
		overtaken := app.latest != mine
		s.mu.Unlock()
		if overtaken {
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := s.Save(ctx, appID, path, summary); err != nil && onErr != nil {
			onErr(err)
		}
	}()
}

var errOverCap = errors.New("imageupload: rebuilt image over the cap")

// capWriter refuses to write more than left bytes.
type capWriter struct {
	w    io.Writer
	left int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, errOverCap
	}
	n, err := c.w.Write(p)
	c.left -= int64(n)
	return n, err
}
