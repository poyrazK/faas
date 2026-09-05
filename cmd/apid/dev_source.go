package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
	"github.com/onebox-faas/faas/pkg/state"
)

type devSourceMetadata struct {
	base    string
	target  string
	deleted []string
	seen    map[string]bool
}

func (m *devSourceMetadata) readField(name string, part *multipart.Part) *api.Problem {
	if m.seen == nil {
		m.seen = make(map[string]bool)
	}
	if m.seen[name] {
		return api.ErrSourceInvalid("duplicate developer source metadata field " + name)
	}
	m.seen[name] = true
	max := int64(sha256.Size*2 + 1)
	if name == "dev_source_deleted" {
		max = api.DevSourceMetadataMaxBytes + 1
	}
	b, err := io.ReadAll(io.LimitReader(part, max))
	if err != nil || int64(len(b)) >= max {
		return api.ErrSourceInvalid(name + " is too large")
	}
	switch name {
	case "dev_source_base":
		m.base = strings.TrimSpace(string(b))
	case "dev_source_target":
		m.target = strings.TrimSpace(string(b))
	case "dev_source_deleted":
		if err := json.Unmarshal(b, &m.deleted); err != nil {
			return api.ErrSourceInvalid("dev_source_deleted must be a JSON string array")
		}
	}
	return nil
}

func (m devSourceMetadata) validate() *api.Problem {
	if !sourcedelta.ValidRevision(m.target) {
		return api.ErrSourceInvalid("dev_source_target must be a lowercase SHA-256 revision")
	}
	if m.base != "" && !sourcedelta.ValidRevision(m.base) {
		return api.ErrSourceInvalid("dev_source_base must be a lowercase SHA-256 revision")
	}
	if m.base == "" && len(m.deleted) > 0 {
		return api.ErrSourceInvalid("a complete developer source snapshot cannot delete paths")
	}
	if len(m.deleted) > api.SourceArchiveMaxEntries {
		return api.ErrSourceInvalid("developer source deletion list has too many entries")
	}
	return nil
}

func sourceDeltaLimits(limits api.Limits) sourcedelta.Limits {
	return sourcedelta.Limits{
		MaxEntries:         api.SourceArchiveMaxEntries,
		MaxCompressedBytes: int64(limits.SourceTarballMaxMB) * 1024 * 1024,
		MaxExpandedBytes:   int64(limits.SourceTarballMaxMB) * 1024 * 1024,
	}
}

func (s *server) prepareDevSource(acct state.Account, app state.App, uploadedPath string, meta devSourceMetadata, limits api.Limits) (string, int64, *api.Problem) {
	if prob := meta.validate(); prob != nil {
		return "", 0, prob
	}
	archiveLimits := sourceDeltaLimits(limits)
	uploaded, err := openDevSourceArchive(uploadedPath)
	if err != nil {
		return "", 0, api.ErrSourceInvalid("open developer source: " + err.Error())
	}
	defer func() { _ = uploaded.Close() }()
	if meta.base == "" {
		manifest, err := sourcedelta.Inspect(uploaded, archiveLimits)
		if err != nil {
			return "", 0, api.ErrSourceInvalid("inspect developer source: " + err.Error())
		}
		if manifest.Revision != meta.target {
			return "", 0, api.ErrSourceInvalid("developer source target revision does not match uploaded content")
		}
		return uploadedPath, manifest.CompressedBytes, nil
	}

	s.devSourceCacheMu.Lock()
	defer s.devSourceCacheMu.Unlock()
	basePath, ok := s.devSourceBasePath(acct, app, meta.base)
	if !ok {
		return "", 0, api.ErrDevSourceBaseMissing()
	}
	base, err := openDevSourceArchive(basePath)
	if err != nil {
		return "", 0, api.ErrDevSourceBaseMissing()
	}
	defer func() { _ = base.Close() }()
	output, err := os.CreateTemp(spoolRoot(), "dev-source-*.tar.gz")
	if err != nil {
		return "", 0, api.ErrSourceInvalid("create reconstructed developer source: " + err.Error())
	}
	outputPath := output.Name()
	defer func() { _ = output.Close() }()
	manifest, err := sourcedelta.Apply(base, uploaded, output, meta.base, meta.target, meta.deleted, archiveLimits)
	if errors.Is(err, sourcedelta.ErrBaseRevision) {
		// codeql[go/path-injection] basePath is a canonical digest path beneath the daemon-owned spool root.
		_ = os.Remove(basePath)
		_ = os.Remove(outputPath)
		return "", 0, api.ErrDevSourceBaseMissing()
	}
	if err != nil {
		_ = os.Remove(outputPath)
		return "", 0, api.ErrSourceInvalid("apply developer source delta: " + err.Error())
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(outputPath)
		return "", 0, api.ErrSourceInvalid("close reconstructed developer source: " + err.Error())
	}
	return outputPath, manifest.CompressedBytes, nil
}

func (s *server) devSourceBasePath(acct state.Account, app state.App, revision string) (string, bool) {
	canonical, ok := canonicalDevSourceRevision(revision)
	if !ok {
		return "", false
	}
	cachePath := filepath.Join(s.devSourceCacheDir(acct, app), canonical+".tar.gz")
	f, err := openDevSourceArchive(cachePath)
	if err != nil {
		return "", false
	}
	info, err := f.Stat()
	_ = f.Close()
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if time.Since(info.ModTime()) > api.DevSourceCacheTTL {
		// codeql[go/path-injection] cachePath uses a decoded and re-encoded SHA-256 beneath a hashed daemon-owned directory.
		_ = os.Remove(cachePath)
		return "", false
	}
	return cachePath, true
}

func (s *server) devSourceCacheDir(acct state.Account, app state.App) string {
	digest := sha256.Sum256([]byte(acct.ID + "\x00" + app.ID))
	return filepath.Join(spoolRoot(), "dev-source-cache", hex.EncodeToString(digest[:]))
}

func (s *server) publishDevSource(acct state.Account, app state.App, sourcePath, revision string, limits api.Limits) error {
	canonical, ok := canonicalDevSourceRevision(revision)
	if !ok {
		return errors.New("invalid developer source revision")
	}
	s.devSourceCacheMu.Lock()
	defer s.devSourceCacheMu.Unlock()
	dir := s.devSourceCacheDir(acct, app)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		return fmt.Errorf("create developer source cache: %w", err)
	}
	dst := filepath.Join(dir, canonical+".tar.gz")
	in, err := openDevSourceArchive(sourcePath)
	if err != nil {
		return fmt.Errorf("open developer source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.CreateTemp(dir, ".dev-source-*.tmp")
	if err != nil {
		return fmt.Errorf("create developer source cache file: %w", err)
	}
	tmp := out.Name()
	if err := out.Chmod(0o660); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("set developer source cache permissions: %w", err)
	}
	keep := false
	defer func() {
		_ = out.Close()
		if !keep {
			_ = os.Remove(tmp)
		}
	}()
	max := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	n, err := io.Copy(out, io.LimitReader(in, max+1))
	if err != nil {
		return fmt.Errorf("copy developer source cache: %w", err)
	}
	if n > max {
		return fmt.Errorf("developer source cache exceeds %d bytes", max)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync developer source cache: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close developer source cache: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("publish developer source cache: %w", err)
	}
	keep = true
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.Name() != canonical+".tar.gz" {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return pruneDevSourceCache(filepath.Join(spoolRoot(), "dev-source-cache"), dst)
}

func canonicalDevSourceRevision(revision string) (string, bool) {
	if !sourcedelta.ValidRevision(revision) {
		return "", false
	}
	digest, err := hex.DecodeString(revision)
	if err != nil || len(digest) != sha256.Size {
		return "", false
	}
	return hex.EncodeToString(digest), true
}

func openDevSourceArchive(filename string) (*os.File, error) {
	rootPath, err := filepath.Abs(spoolRoot())
	if err != nil {
		return nil, fmt.Errorf("resolve spool root: %w", err)
	}
	clean, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolve archive path: %w", err)
	}
	rel, err := filepath.Rel(rootPath, clean)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, errors.New("developer source path escapes the spool root")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open spool root: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("developer source is not a regular file")
	}
	return f, nil
}

func pruneDevSourceCache(root, preserve string) error {
	type cachedSource struct {
		path    string
		size    int64
		modTime time.Time
	}
	var (
		files []cachedSource
		total int64
	)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".gz" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		if time.Since(info.ModTime()) > api.DevSourceCacheTTL && path != preserve {
			return os.Remove(path)
		}
		files = append(files, cachedSource{path: path, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan developer source cache: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, file := range files {
		if total <= api.DevSourceCacheMaxBytes {
			break
		}
		if file.path == preserve {
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("evict developer source cache: %w", err)
		}
		total -= file.size
	}
	if total > api.DevSourceCacheMaxBytes {
		return fmt.Errorf("developer source cache remains above %d bytes", api.DevSourceCacheMaxBytes)
	}
	return nil
}
