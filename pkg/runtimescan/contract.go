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
	if _, err := runtimeadmission.HashArtifactSources(r.Sources); err != nil {
		return err
	}
	var bytes int64
	for _, source := range r.Sources {
		if source.Bytes > api.ApplicationStandardRuntimeScanMaxBytes-bytes {
			return scanview.ErrLimit
		}
		bytes += source.Bytes
	}
	return nil
}

type View struct {
	WorkloadName   string
	SourceTree     scanview.Tree
	ProjectionTree scanview.Tree
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
	hash, err := runtimeadmission.HashArtifactSources(expected.Sources)
	if err != nil || r.Version != Version || r.InputHash != expected.InputHash || r.SourcesHash != hash || r.TargetDir != expected.TargetDir {
		return runtimeadmission.ErrInvalid
	}
	workloads := map[string]bool{"": true}
	for _, source := range expected.Sources {
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
