package runtimequalification

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func stageNativeAsset(ctx context.Context, source io.ReadCloser, destination, want string, maxBytes int64) error {
	//nolint:gosec // Exclusive creation inside the owner's fresh private staging directory.
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(fmt.Errorf("create staged native asset: %w", err), source.Close())
	}
	h := sha256.New()
	size, readErr := io.Copy(io.MultiWriter(file, h), io.LimitReader(contextReader{ctx, source}, maxBytes+1))
	err = errors.Join(readErr, source.Close(), file.Sync(), file.Close())
	if err != nil {
		return fmt.Errorf("stage native asset: %w", err)
	}
	if size == 0 || size > maxBytes || hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("staged native asset differs or exceeds bound: %w", ErrEvidence)
	}
	return nil
}

// Extract only ordinary files/directories from the selected Git object archive.
// No worktree/untracked files, symlinks, hard links, devices or special modes can
// enter the privileged build. This repository has no tracked symlinks/gitlinks.
func extractNativeSource(ctx context.Context, source io.Reader, destination string, maxBytes int64) error {
	limited := io.LimitReader(contextReader{ctx, source}, maxBytes+1)
	counted := &countingReader{r: limited}
	archive := tar.NewReader(counted)
	seen := map[string]bool{}
	var contentBytes int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read native source archive: %w", err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || !filepath.IsLocal(name) || path.Clean(name) != name || strings.Contains(name, "\\") || seen[name] || header.Size < 0 || header.Mode&0o7000 != 0 {
			return fmt.Errorf("unsafe or duplicate native archive path/mode: %w", ErrEvidence)
		}
		seen[name] = true
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if header.Size > maxBytes-contentBytes {
				return fmt.Errorf("native source content exceeds bound: %w", ErrEvidence)
			}
			contentBytes += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o400)
			if header.Mode&0o111 != 0 {
				mode = 0o500
			}
			//nolint:gosec // Validated local archive path; reject links and duplicates, create exclusively under private root.
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("native source contains a link or special entry: %w", ErrEvidence)
		}
	}
	// Drain archive padding while enforcing the entire stream budget, not only
	// regular file sizes. This also lets the Git producer terminate before Wait.
	if _, err := io.Copy(io.Discard, counted); err != nil {
		return err
	}
	if counted.n > maxBytes {
		return fmt.Errorf("native source archive exceeds bound: %w", ErrEvidence)
	}
	if len(seen) == 0 {
		return fmt.Errorf("empty native source archive: %w", ErrEvidence)
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}
