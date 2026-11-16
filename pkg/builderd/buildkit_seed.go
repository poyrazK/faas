package builderd

// adr: 20261030122204

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BuildKitSeedEnv turns on the per-node BuildKit seed (ADR-20261030122204):
// builderd keeps an ext4 BuildKit root holding the platform images Railpack
// builds start from and gives every eligible build a reflink copy of it as
// drive2. Off by default.
const BuildKitSeedEnv = "FAAS_BUILD_BUILDKIT_SEED"

// ParseBuildKitSeed parses a BuildKitSeedEnv value. Empty is off.
func ParseBuildKitSeed(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (got %q)", BuildKitSeedEnv, raw)
	}
	return on, nil
}

// buildkitSeedFormat versions what a seed holds and how it is made; bump it
// when the seed run changes so old seeds are never attached.
const buildkitSeedFormat = "buildkit-seed/v1"

// buildkitSeedSource is the platform-owned app a seed run builds. Railpack
// plans it as any Node app, so the seed holds the frontend, the builder image
// and the runtime base under the references customer builds resolve. It
// holds no tenant data.
var buildkitSeedSource = []struct{ name, body string }{
	{"package.json", `{
  "name": "gregale-buildkit-seed",
  "version": "1.0.0",
  "private": true,
  "scripts": { "start": "node index.js" }
}
`},
	{"index.js", `const http = require("node:http");
http.createServer((_, res) => res.end("ok\n")).listen(Number(process.env.PORT) || 8080);
`},
}

// buildkitSeedSourceTar returns the seed app as a gzipped tarball with fixed
// metadata, so its digest, which the seed key covers, is stable.
func buildkitSeedSourceTar() ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	for _, f := range buildkitSeedSource {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body)),
			ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildkitSeedKey names a seed. It covers everything that decides what the
// seed run unpacks: the builder base (which carries guest-init, BuildKit and
// Railpack), the platform, the BuildKit snapshotter, the seed app and the
// seed format. A seed whose key differs is never attached.
func buildkitSeedKey(env BuildEnvironment, snapshotter string) (string, error) {
	if env.BuilderBaseIdentity == "" || env.BaseDigest == "" || env.TargetPlatform == "" {
		return "", errors.New("builder base identity is incomplete")
	}
	source, err := buildkitSeedSourceTar()
	if err != nil {
		return "", err
	}
	sourceSum := sha256.Sum256(source)
	h := sha256.New()
	for _, part := range []string{buildkitSeedFormat, env.BuilderBaseIdentity, env.BaseDigest, env.TargetPlatform,
		snapshotter, hex.EncodeToString(sourceSum[:])} {
		_, _ = fmt.Fprintf(h, "%d:%s\n", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildkitSeedDir is where a node's seeds live: beside the build-drive
// directory, on the same filesystem so a copy can share the seed's blocks,
// and outside it so the build-drive janitor never removes a seed.
func buildkitSeedDir(driveDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(driveDir)), "buildkit-seed")
}

// buildkitSeedCopyName is a build's copy of the seed, beside its drive1,
// which is where vmmd accepts a seed drive. The janitor removes a stray copy
// with the other build drives.
func buildkitSeedCopyName(instance string) string { return instance + "-buildkit.ext4" }

// buildkitSeedStore keeps a node's seeds: <key>.ext4 and, once the seed run
// that made it succeeded, <key>.ready. Only a ready seed is attached.
type buildkitSeedStore struct {
	dir string
	mu  sync.Mutex
}

func (s *buildkitSeedStore) seedPath(key string) string  { return filepath.Join(s.dir, key+".ext4") }
func (s *buildkitSeedStore) readyPath(key string) string { return filepath.Join(s.dir, key+".ready") }

// ready returns the seed for key when its seed run completed.
func (s *buildkitSeedStore) ready(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.readyPath(key)); err != nil {
		return "", false
	}
	info, err := os.Stat(s.seedPath(key))
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return s.seedPath(key), true
}

// errNoReadySeed is copyTo's answer while the node has no seed for the key:
// the normal state until the next idle slot makes one.
var errNoReadySeed = errors.New("no ready BuildKit seed")

// buildKitSeedEligible reports whether a build may take a copy of the seed.
// Only a Railpack build starts from the platform images a seed holds. A warm
// builder keeps BuildKit on drive1, which its snapshot carries.
func buildKitSeedEligible(req VMRequest) bool {
	return !req.BuildKitSeedOnly && !req.KeepWarm && req.Framework != FrameworkDocker && req.DockerfileBuild == nil
}

// copyTo gives a build its own copy of the ready seed for key at dst, sharing
// the seed's blocks. It fails rather than copy bytes: a build without a seed
// only runs as today.
func (s *buildkitSeedStore) copyTo(key, dst string) error {
	src, ok := s.ready(key)
	if !ok {
		return errNoReadySeed
	}
	if err := cloneFile(src, dst); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// install makes the drive a seed run filled the seed for key and removes
// every other seed. drive must be on the seed directory's filesystem.
func (s *buildkitSeedStore) install(key, drive string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("mkdir seed dir: %w", err)
	}
	if err := os.Rename(drive, s.seedPath(key)); err != nil {
		return fmt.Errorf("install seed: %w", err)
	}
	if err := os.WriteFile(s.readyPath(key), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o640); err != nil {
		_ = os.Remove(s.seedPath(key))
		return fmt.Errorf("mark seed ready: %w", err)
	}
	s.pruneLocked(key)
	return nil
}

// removeAll drops every seed, for a node short of build-drive space. Builds
// already running keep their copies; they share blocks, not files.
func (s *buildkitSeedStore) removeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked("")
}

// pruneLocked removes every seed file except keep's.
func (s *buildkitSeedStore) pruneLocked(keep string) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if keep != "" && (name == keep+".ext4" || name == keep+".ready") {
			continue
		}
		if !e.Type().IsRegular() {
			continue
		}
		_ = os.Remove(filepath.Join(s.dir, name))
	}
}

// removeBuildKitSeeds drops the seeds kept beside driveDir.
func removeBuildKitSeeds(driveDir string) {
	(&buildkitSeedStore{dir: buildkitSeedDir(driveDir)}).removeAll()
}
