package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// dependencyCacheFamiliesDir holds one small pointer file per cache family,
// naming the dependency cache most recently published in it. The sweep
// ignores the directory; a pointer to an evicted cache is simply not used.
const dependencyCacheFamiliesDir = "families"

// dependencyCacheFamilyKey groups an account's apps that build with the same
// framework on the same runtime base. A new app (a fresh slug, or a
// project's next service) has no dependency cache of its own, so every
// service of a Compose project used to build cold even when its stages
// matched a sibling's (retail-store feedback A4, 2026-11-17). Seeding from
// the family's latest cache lets BuildKit reuse identical layers; it still
// validates every imported layer against its exact inputs, so different
// stages simply miss. The account is in the key: tenants never share.
func dependencyCacheFamilyKey(app state.App, framework Framework, runtimeBaseRef string) string {
	if app.AccountID == "" || app.PreviewPrNumber != 0 {
		return ""
	}
	h := sha256.New()
	for _, value := range []string{"dependency-cache-family", app.AccountID, string(framework), runtimeBaseRef} {
		_, _ = io.WriteString(h, value)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dependencyCacheFamilyPath(driveDir, family string) (string, bool) {
	if !validDependencyCacheKey(family) {
		return "", false
	}
	return filepath.Join(driveDir, devDependencyCacheDir, dependencyCacheFamiliesDir, family), true
}

// dependencyCacheStagePath returns the cache to stage into a build: its own
// when it exists, else the family's latest when that exists, else own (a
// cold build). The caller holds dependencyCacheMu.
func dependencyCacheStagePath(driveDir, ownPath, family, ownKey string) string {
	if _, err := os.Stat(ownPath); !errors.Is(err, fs.ErrNotExist) || family == "" {
		return ownPath
	}
	pointer, ok := dependencyCacheFamilyPath(driveDir, family)
	if !ok {
		return ownPath
	}
	raw, err := os.ReadFile(pointer)
	if err != nil {
		return ownPath
	}
	sibling := strings.TrimSpace(string(raw))
	if sibling == ownKey {
		return ownPath
	}
	siblingPath, err := dependencyCachePath(driveDir, sibling)
	if err != nil {
		return ownPath
	}
	if info, err := os.Stat(siblingPath); err != nil || !info.IsDir() {
		return ownPath
	}
	return siblingPath
}

// recordDependencyCacheFamily points family at key after key's cache was
// published. Best effort: a failure only loses the next app's warm start.
// The caller holds dependencyCacheMu.
func recordDependencyCacheFamily(driveDir, family, key string) {
	pointer, ok := dependencyCacheFamilyPath(driveDir, family)
	if !ok || !validDependencyCacheKey(key) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(pointer), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(pointer), ".pointer-*")
	if err != nil {
		return
	}
	_, writeErr := tmp.WriteString(key)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil || os.Rename(tmp.Name(), pointer) != nil {
		_ = os.Remove(tmp.Name())
	}
}
