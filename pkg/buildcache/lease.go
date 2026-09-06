// Package buildcache defines the filesystem handoff shared by builderd and
// imaged for cached build artifacts.
package buildcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	LeaseDir    = ".leases"
	leaseSuffix = ".oci.tar"
)

// LeasePath returns the deployment-specific hard-link path beneath cacheRoot.
func LeasePath(cacheRoot, deploymentID string) (string, error) {
	if cacheRoot == "" {
		return "", errors.New("build cache: empty root")
	}
	if !safeID(deploymentID) {
		return "", fmt.Errorf("build cache: invalid deployment id %q", deploymentID)
	}
	return filepath.Join(cacheRoot, LeaseDir, deploymentID+leaseSuffix), nil
}

// ParseLeasePath extracts a deployment ID from a path created by LeasePath.
func ParseLeasePath(path string) (string, bool) {
	if filepath.Base(filepath.Dir(path)) != LeaseDir {
		return "", false
	}
	base := filepath.Base(path)
	if !strings.HasSuffix(base, leaseSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(base, leaseSuffix)
	return id, safeID(id)
}

// Release removes a build-cache lease. Other paths are ignored so imaged can
// call it for every builder handoff without special-casing scratch builds.
func Release(path string) error {
	if _, ok := ParseLeasePath(path); !ok {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("build cache: release lease %s: %w", path, err)
	}
	return nil
}

func safeID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
