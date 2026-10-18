package hosting

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// ReviewRelease binds immutable manifests to the observed site's active release
// and revision. Missing baselines are errors, never interpreted as an empty site.
func ReviewRelease(site api.HostingSite, releases []api.HostingRelease, target string) (api.HostingReleaseReview, error) {
	out := api.HostingReleaseReview{SiteID: site.ID, ExpectedRevision: site.Revision, BaseReleaseID: site.ActiveReleaseID, ReleaseID: target}
	invalid := errors.New("release comparison unavailable: refresh the active site and verified releases")
	if site.Status != "active" || site.Revision < 1 {
		return out, invalid
	}
	var before, after *api.HostingRelease
	seen := map[string]bool{}
	for i := range releases {
		r := &releases[i]
		if r.SiteID != site.ID || seen[r.ID] {
			return out, invalid
		}
		seen[r.ID] = true
		if r.ID == target {
			after = r
		}
		if r.ID == site.ActiveReleaseID {
			before = r
		}
	}
	if after == nil || after.State != "ready" || site.ActiveReleaseID != "" && (before == nil || before.State != "ready") {
		return out, invalid
	}
	var baseline *api.HostingManifest
	if before != nil {
		baseline = &before.Manifest
	}
	if after.Provenance != nil {
		p := *after.Provenance
		out.Provenance = &p
	}
	var err error
	out.HostingManifestDiff, err = compareManifests(baseline, after.Manifest)
	if err != nil {
		return out, invalid
	}
	return out, nil
}

func compareManifests(before *api.HostingManifest, after api.HostingManifest) (api.HostingManifestDiff, error) {
	out := api.HostingManifestDiff{Files: []api.HostingFileChange{}}
	if err := ValidateManifest(after); err != nil {
		return out, err
	}
	old := map[string]api.HostingFile{}
	if before != nil {
		if err := ValidateManifest(*before); err != nil {
			return out, err
		}
		out.BeforeSettings = api.HostingReleaseSettings{SPAFallback: before.SPAFallback, Config: before.Config}
		for _, f := range before.Files {
			old[f.Path] = f
			out.BeforeBytes += f.Size
		}
	}
	out.AfterSettings = api.HostingReleaseSettings{SPAFallback: after.SPAFallback, Config: after.Config}
	for _, f := range after.Files {
		out.AfterBytes += f.Size
		prev, exists := old[f.Path]
		delete(old, f.Path)
		if !exists {
			out.Added++
			out.Files = append(out.Files, api.HostingFileChange{Path: f.Path, Change: "added", After: &f})
		} else if prev != f {
			out.Changed++
			out.Files = append(out.Files, api.HostingFileChange{Path: f.Path, Change: "changed", Before: &prev, After: &f})
		}
	}
	for _, f := range old {
		out.Removed++
		out.Files = append(out.Files, api.HostingFileChange{Path: f.Path, Change: "removed", Before: &f})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	previous, _ := json.Marshal(out.BeforeSettings)
	next, _ := json.Marshal(out.AfterSettings)
	out.SettingsChanged = !bytes.Equal(previous, next)
	return out, nil
}
