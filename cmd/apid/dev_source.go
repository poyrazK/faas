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
	if meta.base == "" {
		manifest, err := sourcedelta.Inspect(uploadedPath, archiveLimits)
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
	outputPath := filepath.Join(spoolRoot(), randomToken(12)+".tar.gz")
	manifest, err := sourcedelta.Apply(basePath, uploadedPath, outputPath, meta.base, meta.target, meta.deleted, archiveLimits)
	if errors.Is(err, sourcedelta.ErrBaseRevision) {
		_ = os.Remove(basePath)
		return "", 0, api.ErrDevSourceBaseMissing()
	}
	if err != nil {
		return "", 0, api.ErrSourceInvalid("apply developer source delta: " + err.Error())
	}
	return outputPath, manifest.CompressedBytes, nil
}

func (s *server) devSourceBasePath(acct state.Account, app state.App, revision string) (string, bool) {
	path := filepath.Join(s.devSourceCacheDir(acct, app), revision+".tar.gz")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if time.Since(info.ModTime()) > api.DevSourceCacheTTL {
		_ = os.Remove(path)
		return "", false
	}
	return path, true
}

func (s *server) devSourceCacheDir(acct state.Account, app state.App) string {
	digest := sha256.Sum256([]byte(acct.ID + "\x00" + app.ID))
	return filepath.Join(spoolRoot(), "dev-source-cache", hex.EncodeToString(digest[:]))
}

func (s *server) publishDevSource(acct state.Account, app state.App, sourcePath, revision string, limits api.Limits) error {
	if !sourcedelta.ValidRevision(revision) {
		return errors.New("invalid developer source revision")
	}
	s.devSourceCacheMu.Lock()
	defer s.devSourceCacheMu.Unlock()
	dir := s.devSourceCacheDir(acct, app)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		return fmt.Errorf("create developer source cache: %w", err)
	}
	dst := filepath.Join(dir, revision+".tar.gz")
	tmp := dst + "." + randomToken(6) + ".tmp"
	in, err := os.Open(sourcePath) //nolint:gosec,forbidigo // apid-owned random spool path already validated by sourcedelta.Inspect.
	if err != nil {
		return fmt.Errorf("open developer source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o660)
	if err != nil {
		return fmt.Errorf("create developer source cache file: %w", err)
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
		if entry.Name() != revision+".tar.gz" {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return pruneDevSourceCache(filepath.Join(spoolRoot(), "dev-source-cache"), dst)
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
