package builderd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// Read the frozen, selected source archive, never the post-build source tree.
// Transport wrappers/workspaces use the same resolver as framework detection.
func buildIssueSourceMapConfig(archive, sourceRoot string) (*api.IssueSourceMapBuildConfig, error) {
	root, err := tarball.ResolveSourceRoot(archive, sourceRoot)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(archive) //nolint:forbidigo // archive is the server-owned source spool, validated and hashed by ProcessOne.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	needle := path.Join(root, api.IssueSourceMapBuildConfigFile)
	var cfg *api.IssueSourceMapBuildConfig
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(h.Name, "./") != needle {
			continue
		}
		if cfg != nil || !h.FileInfo().Mode().IsRegular() || h.Size > api.IssueSourceMapBuildConfigMaxBytes {
			return nil, errors.New("source-map build config must be one bounded regular file")
		}
		data, err := io.ReadAll(io.LimitReader(tr, api.IssueSourceMapBuildConfigMaxBytes+1))
		if err != nil {
			return nil, err
		}
		cfg = new(api.IssueSourceMapBuildConfig)
		if err := issues.DecodeSourceMapBuildJSON(data, cfg); err != nil {
			return nil, err
		}
		if err := issues.ValidateSourceMapBuildConfig(*cfg); err != nil {
			return nil, err
		}
	}
}

func readBuildIssueSourceMaps(image string, req VMRequest) (*issues.SourceMapSet, string, error) {
	root, err := os.OpenRoot(filepath.Dir(image))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = root.Close() }()
	data, err := issues.ReadSourceMapBuildFile(root, api.BuildIssueSourceMapsFile, api.IssueSourceMapBuildMetadataMaxBytes)
	if err != nil {
		return nil, "", fmt.Errorf("source-map build handoff missing or invalid (update the builder image if necessary): %w", err)
	}
	var handoff api.BuildIssueSourceMaps
	if err := issues.DecodeSourceMapBuildJSON(data, &handoff); err != nil {
		return nil, "", err
	}
	digest, err := hashFile(image)
	if err != nil {
		return nil, "", err
	}
	if handoff.BuildID != req.BuildID || handoff.DeploymentID != req.DeploymentID || handoff.SourceSHA256 != req.SourceSHA256 || handoff.ImageSHA256 != digest || handoff.Request.Directory != req.IssueSourceMaps.Directory {
		return nil, "", errors.New("source-map handoff does not match this build, deployment, source or image")
	}
	set, err := issues.CompileSourceMaps(handoff.Request)
	return set, digest, err
}

func validCachedIssueSourceMaps(cached CacheEntry, directory string) bool {
	if cached.Toolchain.IssueSourceMaps == nil || cached.Toolchain.IssueSourceMaps.Directory != directory {
		return false
	}
	set, err := issues.CompileSourceMaps(*cached.Toolchain.IssueSourceMaps)
	if err != nil || set.Digest != cached.Toolchain.IssueSourceMapsDigest {
		return false
	}
	digest, err := hashFile(cached.Path)
	return err == nil && digest == cached.Toolchain.IssueSourceMapsImageSHA256
}

func buildIssueSourceMapMetadata(in *api.PublishIssueSourceMapsRequest) string {
	if in == nil {
		return ""
	}
	set, err := issues.CompileSourceMaps(*in)
	if err != nil {
		return ""
	}
	return set.Digest
}

// Bound the complete cache envelope, including toolchain fields around maps.
func boundedBuildCacheMetadata(toolchain CacheToolchain) ([]byte, error) {
	data, err := json.Marshal(toolchain)
	if err != nil || len(data) > api.IssueSourceMapBuildMetadataMaxBytes {
		return nil, errors.New("build cache metadata exceeds its byte bound")
	}
	return data, nil
}
