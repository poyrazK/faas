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
	out := api.HostingReleaseReview{SiteID: site.ID, ExpectedRevision: site.Revision, BaseReleaseID: site.ActiveReleaseID, ReleaseID: target, Files: []api.HostingFileChange{}}
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
	old := map[string]api.HostingFile{}
	if before != nil {
		if ValidateManifest(before.Manifest) != nil {
			return out, invalid
		}
		out.BeforeSettings = api.HostingReleaseSettings{SPAFallback: before.Manifest.SPAFallback, Config: before.Manifest.Config}
		for _, f := range before.Manifest.Files {
			old[f.Path] = f
			out.BeforeBytes += f.Size
		}
	}
	if ValidateManifest(after.Manifest) != nil {
		return out, invalid
	}
	out.AfterSettings = api.HostingReleaseSettings{SPAFallback: after.Manifest.SPAFallback, Config: after.Manifest.Config}
	for _, f := range after.Manifest.Files {
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
