package runtimeupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

func (s Stager) stageSource(ctx context.Context, id string, serving state.Deployment) error {
	info, err := os.Lstat(s.SpoolRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: spool root unavailable or unsafe", ErrSourceIntegrity)
	}
	root, err := os.OpenRoot(s.SpoolRoot)
	if err != nil {
		return fmt.Errorf("open source spool: %w", err)
	}
	defer func() { _ = root.Close() }()
	name := id + ".tar.gz"
	// Reuse only a complete verified handoff; never replace an existing path.
	f, err := openStagedFile(root, name, serving.SourceBytes)
	if errors.Is(err, os.ErrNotExist) {
		f, err = s.materializeSource(ctx, root, name, serving)
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := verifySource(ctx, io.Discard, f, serving); err != nil {
		return err
	}
	// Reusing an uncertain publication must establish durability too, including
	// when an earlier directory sync failed after the candidate link appeared.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync verified source: %w", err)
	}
	if err := syncSourceSpool(root); err != nil {
		return err
	}
	if s.Source == nil {
		return nil // single-host local spool handoff
	}
	key := "sources/" + id + ".tar.gz"
	retained, err := s.Source.Get(ctx, key)
	if err == nil {
		defer func() { _ = retained.Close() }()
		return verifySource(ctx, io.Discard, retained, serving)
	}
	if !sourceMissing(err) {
		return fmt.Errorf("read candidate source object: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind staged source: %w", err)
	}
	if err := s.Source.Put(ctx, key, contextSourceReader{ctx, f}); err != nil {
		return fmt.Errorf("publish candidate source object: %w", err)
	}
	// Verify the canonical read after Put, including backends that returned
	// success for a partial/corrupt upload. Builderd verifies again at use.
	retained, err = s.Source.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("read published source object: %w", err)
	}
	defer func() { _ = retained.Close() }()
	return verifySource(ctx, io.Discard, retained, serving)
}

func (s Stager) materializeSource(ctx context.Context, root *os.Root, name string, serving state.Deployment) (*os.File, error) {
	source, err := s.retainedSource(ctx, root, serving)
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	temp := ".runtime-upgrade-" + uuid.NewString()
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create source staging file: %w", err)
	}
	defer func() { _ = f.Close(); _ = root.Remove(temp) }()
	if err := verifySource(ctx, f, source, serving); err != nil {
		return nil, err
	}
	if err := f.Chmod(0o444); err != nil {
		return nil, fmt.Errorf("seal staged source: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, fmt.Errorf("sync staged source: %w", err)
	}
	// Link publishes without overwriting a concurrent or uncertain handoff.
	if err := root.Link(temp, name); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("publish staged source: %w", err)
	}
	return openStagedFile(root, name, serving.SourceBytes)
}

func syncSourceSpool(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open source spool for sync: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync source spool: %w", err)
	}
	return nil
}

func (s Stager) retainedSource(ctx context.Context, root *os.Root, serving state.Deployment) (io.ReadCloser, error) {
	if serving.BuildID != "" {
		id, err := uuid.Parse(serving.BuildID)
		if err != nil || id == uuid.Nil || id.String() != serving.BuildID {
			return nil, state.ErrConflict
		}
		if s.Source != nil {
			source, err := s.Source.Get(ctx, "sources/"+serving.BuildID+".tar.gz")
			if err == nil {
				return source, nil
			}
			if !sourceMissing(err) {
				return nil, fmt.Errorf("read retained source object: %w", err)
			}
		}
	}
	rel, err := filepath.Rel(s.SpoolRoot, serving.SourcePath)
	if err != nil || !filepath.IsAbs(serving.SourcePath) || !filepath.IsLocal(rel) || rel == "." {
		return nil, fmt.Errorf("%w: retained source escapes spool", ErrSourceIntegrity)
	}
	return openStagedFile(root, rel, serving.SourceBytes)
}

// Root confines path resolution even if a directory is replaced during open.
// Reject symlink components and nonregular objects; nonblocking/no-follow open
// avoids a replaced final FIFO or link blocking/exfiltrating before fstat.
func openStagedFile(root *os.Root, name string, size int64) (*os.File, error) {
	parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() != size)) {
			return nil, fmt.Errorf("%w: unsafe source file", ErrSourceIntegrity)
		}
	}
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open confined source: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		_ = f.Close()
		return nil, fmt.Errorf("%w: source changed during open", ErrSourceIntegrity)
	}
	return f, nil
}

func verifySource(ctx context.Context, dst io.Writer, source io.Reader, serving state.Deployment) error {
	hash := sha256.New()
	r := contextSourceReader{ctx, source}
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(r, serving.SourceBytes))
	if err != nil {
		return fmt.Errorf("copy retained source: %w", err)
	}
	var extra [1]byte
	extraN, extraErr := io.ReadFull(r, extra[:])
	if extraErr != nil && !errors.Is(extraErr, io.EOF) {
		return fmt.Errorf("finish retained source: %w", extraErr)
	}
	if n != serving.SourceBytes || extraN != 0 || hex.EncodeToString(hash.Sum(nil)) != serving.SourceSHA256 {
		return ErrSourceIntegrity
	}
	return nil
}

type contextSourceReader struct {
	ctx context.Context
	io.Reader
}

func (r contextSourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func sourceMissing(err error) bool {
	return errors.Is(err, storage.ErrNotFound) || errors.Is(err, os.ErrNotExist)
}
