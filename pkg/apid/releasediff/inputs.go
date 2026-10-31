package releasediff

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Package is one SBOM component: a package name (its purl without version
// when the SBOM has one) and its version.
type Package struct {
	Name    string
	Version string
}

// ErrSBOMTooLarge refuses an SBOM over the read limit.
var ErrSBOMTooLarge = errors.New("releasediff: SBOM exceeds the read limit")

// ParseSBOM reads the components of a CycloneDX JSON SBOM, at most maxBytes
// of it.
func ParseSBOM(r io.Reader, maxBytes int64) ([]Package, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, ErrSBOMTooLarge
	}
	var doc struct {
		Components []struct {
			Group   string `json:"group"`
			Name    string `json:"name"`
			Version string `json:"version"`
			Purl    string `json:"purl"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make([]Package, 0, len(doc.Components))
	for _, c := range doc.Components {
		name := purlName(c.Purl)
		if name == "" {
			name = c.Name
			if c.Group != "" {
				name = c.Group + "/" + c.Name
			}
		}
		if name != "" {
			out = append(out, Package{Name: name, Version: c.Version})
		}
	}
	return out, nil
}

// purlName is a purl without its version, qualifiers and subpath, such as
// "pkg:npm/express".
func purlName(purl string) string {
	if !strings.HasPrefix(purl, "pkg:") {
		return ""
	}
	if i := strings.IndexAny(purl, "?#"); i >= 0 {
		purl = purl[:i]
	}
	if i := strings.LastIndex(purl, "@"); i > strings.LastIndex(purl, "/") {
		purl = purl[:i]
	}
	return purl
}

// PackageChanges compares two package lists by name. A name with several
// versions compares as the sorted, joined version list. Each result list
// holds at most limit entries, sorted by name; truncated reports a cut.
func PackageChanges(from, to []Package, limit int) (added, removed, changed []api.ReleaseDiffPackage, truncated bool) {
	a, b := versions(from), versions(to)
	added, removed, changed = []api.ReleaseDiffPackage{}, []api.ReleaseDiffPackage{}, []api.ReleaseDiffPackage{}
	for name, v := range b {
		old, ok := a[name]
		switch {
		case !ok:
			added = append(added, api.ReleaseDiffPackage{Name: name, To: v})
		case old != v:
			changed = append(changed, api.ReleaseDiffPackage{Name: name, From: old, To: v})
		}
	}
	for name, v := range a {
		if _, ok := b[name]; !ok {
			removed = append(removed, api.ReleaseDiffPackage{Name: name, From: v})
		}
	}
	cut := func(list []api.ReleaseDiffPackage) []api.ReleaseDiffPackage {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		if len(list) > limit {
			truncated = true
			return list[:limit]
		}
		return list
	}
	return cut(added), cut(removed), cut(changed), truncated
}

func versions(pkgs []Package) map[string]string {
	byName := map[string][]string{}
	for _, p := range pkgs {
		if !slices.Contains(byName[p.Name], p.Version) {
			byName[p.Name] = append(byName[p.Name], p.Version)
		}
	}
	out := make(map[string]string, len(byName))
	for name, vs := range byName {
		slices.Sort(vs)
		out[name] = strings.Join(vs, ",")
	}
	return out
}

// Predecessor is the newest deployment of to's app and scope created before
// it that took traffic: it completed a rollout, is live, or was superseded.
func Predecessor(deployments []state.Deployment, to state.Deployment) (state.Deployment, bool) {
	var best state.Deployment
	found := false
	for _, d := range deployments {
		if d.ID == to.ID || d.AppID != to.AppID || scope(d.Scope) != scope(to.Scope) || !d.CreatedAt.Before(to.CreatedAt) {
			continue
		}
		if d.RolloutCompletedAt == nil && d.Status != state.DeployLive && d.Status != state.DeploySuperseded {
			continue
		}
		if !found || d.CreatedAt.After(best.CreatedAt) {
			best, found = d, true
		}
	}
	return best, found
}

func scope(s string) string {
	if s == "" {
		return api.DefaultEnvScope
	}
	return s
}
