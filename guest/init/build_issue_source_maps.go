package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

// The raw filesystem stays inside drive1, outside /build/out, so vmmd never
// exports that extra tree to the host. The artifact sidecar is sanitized; the
// existing OCI image (including customer-shipped .map files) stays unchanged.
const buildIssueSourceMapRootfs = "/build/issue-source-map-rootfs"

func prepareBuildIssueSourceMaps(m api.BuildManifest) error {
	if m.IssueSourceMaps == nil {
		return nil
	}
	if m.Framework != api.FrameworkRailpackNode || m.DevWatchCommand != "" {
		return errors.New("build source maps require a production Node source build")
	}
	if err := issues.ValidateSourceMapBuildConfig(*m.IssueSourceMaps); err != nil {
		return err
	}
	// Remove a previous warm attempt's local export before BuildKit starts.
	return os.RemoveAll(buildIssueSourceMapRootfs)
}

func exportBuildIssueSourceMaps(m api.BuildManifest) error {
	if m.IssueSourceMaps == nil {
		return nil
	}
	defer func() { _ = os.RemoveAll(buildIssueSourceMapRootfs) }()
	return writeBuildIssueSourceMaps(m, buildIssueSourceMapRootfs)
}

func writeBuildIssueSourceMaps(m api.BuildManifest, rootfs string) error {
	root, err := os.OpenRoot(rootfs)
	if err != nil {
		return fmt.Errorf("open final source-map filesystem: %w", err)
	}
	defer func() { _ = root.Close() }()
	set, err := issues.DiscoverBuildSourceMaps(root, *m.IssueSourceMaps)
	if err != nil {
		return fmt.Errorf("collect final source maps: %w", err)
	}
	digest, err := buildIssueSourceMapImageSHA256(m.OutDir)
	if err != nil {
		return err
	}
	data, err := json.Marshal(api.BuildIssueSourceMaps{BuildID: m.BuildID, DeploymentID: m.DeploymentID, SourceSHA256: m.SourceSHA256, ImageSHA256: digest, Request: set.Request})
	if err != nil || len(data) > api.IssueSourceMapBuildMetadataMaxBytes {
		return errors.New("sanitized source-map handoff exceeds its byte bound")
	}
	file, err := os.CreateTemp(m.OutDir, ".issue-source-maps-*")
	if err != nil {
		return fmt.Errorf("create source-map handoff: %w", err)
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write source-map handoff: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync source-map handoff: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source-map handoff: %w", err)
	}
	return os.Rename(file.Name(), filepath.Join(m.OutDir, api.BuildIssueSourceMapsFile))
}

func buildIssueSourceMapImageSHA256(out string) (string, error) {
	root, err := os.OpenRoot(out)
	if err != nil {
		return "", fmt.Errorf("open source-map image directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	image, err := root.OpenFile("image.tar", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("open source-map image: %w", err)
	}
	defer func() { _ = image.Close() }()
	info, err := image.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > api.MaxExportedLayerBytes {
		return "", errors.New("source-map image must be a bounded regular file")
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(image, api.MaxExportedLayerBytes+1))
	if err != nil || n != info.Size() {
		return "", errors.New("source-map image changed while hashing")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
