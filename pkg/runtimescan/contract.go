// Package runtimescan binds native composed views to complete source blobs.
// A scan materialization receipt is evidence, not runtime admission or approval.
package runtimescan

import (
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/scanview"
)

// StagingRoot and TargetPrefix define the private projection handoff path.
// Mounting and native path validation remain owned by vmmd.
const StagingRoot = "/dev/shm/faas-base-staging"
const TargetPrefix = "imaged-runtime-scan-"

const Version uint32 = 1

type Request struct {
	Version   uint32
	InputHash string
	Sources   []runtimeadmission.ArtifactSource
	TargetDir string
}

func (r Request) Validate() error {
	if r.Version != Version || !runtimeadmission.ValidHash(r.InputHash) || !filepath.IsAbs(r.TargetDir) || filepath.Clean(r.TargetDir) != r.TargetDir || len(r.TargetDir) > api.ApplicationStandardBaseMaxPathBytes || strings.ContainsAny(r.TargetDir, "\x00\r\n") {
		return runtimeadmission.ErrInvalid
	}
	return validateSources(r.InputHash, r.Sources)
}

func validateSources(inputHash string, sources []runtimeadmission.ArtifactSource) error {
	if !runtimeadmission.ValidHash(inputHash) {
		return runtimeadmission.ErrInvalid
	}
	if _, err := runtimeadmission.HashArtifactSources(sources); err != nil {
		return err
	}
	var bytes int64
	for _, source := range sources {
		if source.Bytes > api.ApplicationStandardRuntimeScanMaxBytes-bytes {
			return scanview.ErrLimit
		}
		bytes += source.Bytes
	}
	return nil
}

type View struct {
	WorkloadName   string        `json:"workload_name"`
	SourceTree     scanview.Tree `json:"source_tree"`
	ProjectionTree scanview.Tree `json:"projection_tree"`
}

// Facts retains byte and view bindings without a temporary scanner pathname.
// It is scan evidence, never approval or a capability to mount or execute.
type Facts struct {
	Version     uint32 `json:"version"`
	InputHash   string `json:"input_hash"`
	SourcesHash string `json:"sources_hash"`
	Views       []View `json:"views"`
}

type Receipt struct {
	Version                           uint32
	InputHash, SourcesHash, TargetDir string
	Views                             []View
}

func (r Receipt) Check(expected Request) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if r.TargetDir != expected.TargetDir {
		return runtimeadmission.ErrInvalid
	}
	return r.Facts().Check(expected.InputHash, expected.Sources)
}

func (r Receipt) Facts() Facts {
	return Facts{Version: r.Version, InputHash: r.InputHash, SourcesHash: r.SourcesHash, Views: append([]View(nil), r.Views...)}
}

func (r Facts) Check(inputHash string, sources []runtimeadmission.ArtifactSource) error {
	if err := validateSources(inputHash, sources); err != nil {
		return err
	}
	hash, err := runtimeadmission.HashArtifactSources(sources)
	if err != nil || r.Version != Version || r.InputHash != inputHash || r.SourcesHash != hash {
		return runtimeadmission.ErrInvalid
	}
	workloads := map[string]bool{"": true}
	for _, source := range sources {
		if source.Kind == "sidecar-layer" {
			workloads[source.WorkloadName] = true
		}
	}
	if len(r.Views) != len(workloads) {
		return runtimeadmission.ErrInvalid
	}
	var bytes int64
	var entries int
	for _, view := range r.Views {
		if !workloads[view.WorkloadName] || !validTree(view.SourceTree) || !validTree(view.ProjectionTree) || view.SourceTree.ProjectionDigest != view.ProjectionTree.ProjectionDigest || view.SourceTree.Entries != view.ProjectionTree.Entries || view.SourceTree.Bytes != view.ProjectionTree.Bytes {
			return runtimeadmission.ErrInvalid
		}
		delete(workloads, view.WorkloadName)
		if view.SourceTree.Bytes > api.ApplicationStandardRuntimeScanMaxBytes-bytes || view.SourceTree.Entries > api.ApplicationStandardRuntimeScanMaxEntries-entries {
			return scanview.ErrLimit
		}
		bytes += view.SourceTree.Bytes
		entries += view.SourceTree.Entries
	}
	return nil
}

func validTree(t scanview.Tree) bool {
	return t.Version == scanview.Version && runtimeadmission.ValidHash(t.Digest) && runtimeadmission.ValidHash(t.ProjectionDigest) && t.Entries > 0 && t.Entries <= api.ApplicationStandardRuntimeScanMaxEntries && t.Bytes >= 0 && t.Bytes <= api.ApplicationStandardRuntimeScanMaxBytes
}

func ViewDirectory(workload string) string {
	if workload == "" {
		return "main"
	}
	return "sidecar-" + workload
}
