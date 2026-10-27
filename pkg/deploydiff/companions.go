package deploydiff

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// companionView is a companion as the diff shows it. PublicHost is the
// hostname a companion port opens on the edge (docs/companions.md); Public
// marks a port whose hostname the preview cannot spell because the app's
// domain is not known yet.
type companionView struct {
	api.DeploymentCompanion
	PublicHost string `json:"public_host,omitempty"`
	Public     bool   `json:"public,omitempty"`
}

// diffCompanions compares the live deployment's companions with the set the
// deploy will send (Hunt 14 H14-53). A field the manifest leaves unset (a
// preset's image, port or size, a default type) is filled in by the server,
// so only fields the manifest sets count as a change.
func diffCompanions(out *Diff, baseline Baseline, pending Pending) {
	if !pending.CompanionsKnown {
		return
	}
	var before []api.DeploymentCompanion
	if baseline.LatestDeployment != nil {
		before = baseline.LatestDeployment.Companions
	}
	domain := appsDomain(baseline.App, out.Slug)
	previous := make(map[string]api.DeploymentCompanion, len(before))
	for _, c := range before {
		previous[c.Name] = c
	}
	declared := make(map[string]bool, len(pending.Companions))
	for _, next := range pending.Companions {
		declared[next.Name] = true
		prev, existed := previous[next.Name]
		switch {
		case !existed:
			out.Changes = append(out.Changes, Change{Field: "companion." + next.Name, Kind: ChangeAdd,
				After: AsAny(viewCompanion(next, 0, out.Slug, domain))})
		case companionChanged(prev, next):
			out.Changes = append(out.Changes, Change{Field: "companion." + next.Name, Kind: ChangeModify,
				Before: AsAny(viewCompanion(prev, 0, out.Slug, domain)),
				After:  AsAny(viewCompanion(next, prev.Port, out.Slug, domain))})
		}
	}
	removed := make([]api.DeploymentCompanion, 0)
	for _, prev := range before {
		if !declared[prev.Name] {
			removed = append(removed, prev)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Name < removed[j].Name })
	for _, prev := range removed {
		out.Changes = append(out.Changes, Change{Field: "companion." + prev.Name, Kind: ChangeRemove,
			Before: AsAny(viewCompanion(prev, 0, out.Slug, domain))})
	}
}

// companionChanged reports a difference in a field the manifest sets.
func companionChanged(prev, next api.DeploymentCompanion) bool {
	switch {
	case next.Preset != prev.Preset, next.PrimaryIngress != prev.PrimaryIngress:
		return true
	case next.Image != "" && next.Image != prev.Image:
		return true
	case next.Type != "" && prev.Type != "" && next.Type != prev.Type:
		return true
	case next.RAMMB != 0 && next.RAMMB != prev.RAMMB:
		return true
	case next.Port != 0 && next.Port != prev.Port:
		return true
	case next.Port == 0 && prev.Port != 0 && next.Preset == "":
		// A custom companion that drops its port also drops its hostname.
		return true
	}
	return false
}

// viewCompanion names the public hostname of a companion with a port. A
// preset that leaves its port unset keeps the live port (inheritedPort).
func viewCompanion(c api.DeploymentCompanion, inheritedPort int, slug, domain string) companionView {
	v := companionView{DeploymentCompanion: c}
	port := c.Port
	if port == 0 && c.Preset != "" {
		port = inheritedPort
	}
	if port == 0 {
		return v
	}
	if domain == "" {
		v.Public = true
		return v
	}
	v.PublicHost = slug + "--" + c.Name + "." + domain
	return v
}

// appsDomain derives the platform apps domain from the app's platform URL
// (https://<slug>.<domain>); empty before the app exists.
func appsDomain(app *api.AppResponse, slug string) string {
	if app == nil || app.URL == "" {
		return ""
	}
	u, err := url.Parse(app.URL)
	if err != nil {
		return ""
	}
	domain, ok := strings.CutPrefix(u.Hostname(), slug+".")
	if !ok {
		return ""
	}
	return domain
}

// printCompanion renders one companion change: "+ name image port" with the
// public hostname a port opens, "~ name" with the before and after shapes,
// or "- name".
func printCompanion(w io.Writer, c Change) {
	name := strings.TrimPrefix(c.Field, "companion.")
	switch c.Kind {
	case ChangeAdd:
		after, _ := c.After.Value.(companionView)
		_, _ = fmt.Fprintf(w, "  + %s  %s\n", name, describeCompanion(after))
	case ChangeRemove:
		before, _ := c.Before.Value.(companionView)
		_, _ = fmt.Fprintf(w, "  - %s  %s\n", name, describeCompanion(before))
	default:
		before, _ := c.Before.Value.(companionView)
		after, _ := c.After.Value.(companionView)
		_, _ = fmt.Fprintf(w, "  ~ %s  %s → %s\n", name, describeCompanion(before), describeCompanion(after))
	}
}

func describeCompanion(v companionView) string {
	parts := make([]string, 0, 4)
	switch {
	case v.Preset != "":
		parts = append(parts, "preset="+v.Preset)
	case v.Image != "":
		parts = append(parts, "image="+v.Image)
	}
	if v.Port != 0 {
		parts = append(parts, "port="+strconv.Itoa(v.Port))
	}
	if v.RAMMB != 0 {
		parts = append(parts, "ram="+strconv.Itoa(v.RAMMB)+"MB")
	}
	switch {
	case v.PublicHost != "":
		parts = append(parts, "public at "+v.PublicHost)
	case v.Public:
		parts = append(parts, "public at <app>--"+v.Name+".<apps domain>")
	}
	if len(parts) == 0 {
		return "(no port)"
	}
	return strings.Join(parts, " ")
}
