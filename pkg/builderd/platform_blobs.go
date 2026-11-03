package builderd

// adr: 20261031103801

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/state"
)

// SharedPlatformBlobsEnv makes published dependency caches share one copy of
// each platform image layer (ADR-20261031103801). Default off.
const SharedPlatformBlobsEnv = "FAAS_BUILD_SHARED_PLATFORM_BLOBS"

// ParseSharedPlatformBlobs parses a SharedPlatformBlobsEnv value. An
// unparsable value is an error so a typo fails startup.
func ParseSharedPlatformBlobs(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (got %q)", SharedPlatformBlobsEnv, raw)
	}
	return on, nil
}

// platformBlobsDir holds the stored copy of each shared platform layer, beside
// the caches that link to it. The sweep never treats it as a cache.
const platformBlobsDir = ".platform-blobs"

// platformLayersRailpackVersion is the RAILPACK_VERSION in
// images/builder-base.Dockerfile that platformLayers was made for. A test
// fails when they differ; scripts/railpack-platform-layers.sh prints the list
// for a new version.
const platformLayersRailpackVersion = "0.38.0"

// platformLayers are the amd64 layer digests of
// ghcr.io/railwayapp/railpack-builder:mise-2026.8.13, the public image every
// Railpack plan of this version starts from. `mode=max` cache exports carry
// these layers unchanged. Only blobs on this list are ever shared between
// caches: nothing an app's build produced is.
var platformLayers = map[string]struct{}{
	"27ee9a8250487842a26b1ffa1215982ba9ae27010bce1997d52f9f8628578d17": {},
	"00f78834a2fef03250be89a7741ff39d0e6bbd860298ea129a84ce5eadda1f6d": {},
	"cbc19164244e861d91ebd80a17e2c78c5be43a8059a32ab6fc459a92002b22f2": {},
	"4f4fb700ef54461cfa02571ae0db9a0dc1e0cdb5577484a6d75e68dc38e8acc1": {},
	"97ddafb7ae42dc361ead5750681f5140a4cffac033a60f8ab568bfbb2a898acd": {},
	"59e1e910848fd38e4673fa08caf31b12559689ba3a52e317dee727d8a5a730ac": {},
	"2469fb8031239dea7cedae095763566c1d91935cb4467346f5c0f7de3b18ea1a": {},
	"d97750091828c7b76f05003a751ca0ef6162c858da66cb5ec7628fcfeca39147": {},
}

// sharePlatformBlobs replaces each listed blob of the staged cache at dir with
// a hard link to the one stored copy under root, the dependency-cache root on
// the same filesystem. A blob is linked only when its sha256 is its name: the
// guest wrote the cache, so a name proves nothing. Any failure leaves that blob
// as the cache's own copy. It returns how many blobs and bytes it linked.
func sharePlatformBlobs(root, dir string, layers map[string]struct{}) (int, int64) {
	blobs := filepath.Join(dir, "blobs", "sha256")
	entries, err := os.ReadDir(blobs)
	if err != nil {
		return 0, 0
	}
	var linked int
	var bytes int64
	for _, entry := range entries {
		digest := entry.Name()
		if _, ok := layers[digest]; !ok {
			continue
		}
		path := filepath.Join(blobs, digest)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if sum, err := blobSHA256(path); err != nil || sum != digest {
			continue
		}
		stored := filepath.Join(root, platformBlobsDir, "sha256", digest)
		if err := linkStoredBlob(stored, path, filepath.Join(filepath.Dir(dir), "."+digest+".link"), info.Size()); err != nil {
			continue
		}
		linked++
		bytes += info.Size()
	}
	return linked, bytes
}

// linkStoredBlob points path at the stored copy, through tmp so the swap is
// one rename. With no stored copy yet, the verified file at path becomes it.
// Stored copies are read-only: caches are only ever created, renamed, linked
// and unlinked, never written in place.
func linkStoredBlob(stored, path, tmp string, size int64) error {
	info, err := os.Lstat(stored)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(stored), 0o700); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o444); err != nil {
			return err
		}
		return os.Link(path, stored)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return fmt.Errorf("stored platform blob %s is not a %d-byte file", filepath.Base(stored), size)
	}
	_ = os.Remove(tmp)
	if err := os.Link(stored, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// blobSHA256 returns the hex sha256 of a blob file.
//
//nolint:forbidigo // path is a regular file in a cache builderd staged under its own dependency-cache root; the walk rejected links and other entries.
func blobSHA256(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- builderd's dependency-cache root.
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

// removeOrphanPlatformBlobs drops stored copies that no cache links to any
// more (link count 1). A copy still linked from a build's staging directory
// stays until that build is done.
func removeOrphanPlatformBlobs(root string) {
	dir := filepath.Join(root, platformBlobsDir, "sha256")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink == 1 {
			_ = os.Remove(path)
		}
	}
}

// diskBytes is the regular-file bytes under paths, counting each inode once:
// a platform layer linked into many caches occupies its bytes once on disk.
func diskBytes(paths ...string) (int64, error) {
	type inode struct{ dev, ino uint64 }
	seen := map[inode]struct{}{}
	var total int64
	for _, root := range paths {
		err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				key := inode{dev: uint64(st.Dev), ino: st.Ino}
				if _, dup := seen[key]; dup {
					return nil
				}
				seen[key] = struct{}{}
			}
			total += info.Size()
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// logSharedPlatformBytes records, for the one-node canary, how much of a
// stored cache links to shared platform layers.
func (b *Builderd) logSharedPlatformBytes(build state.Build, out BuildOutcome) {
	if out.DependencyCacheSharedBytes > 0 {
		b.log.Info("builderd: dependency cache shares platform layers", "build", build.ID,
			"deployment", build.DeploymentID, "shared_bytes", out.DependencyCacheSharedBytes)
	}
}
