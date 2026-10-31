// Package releasediff compares two releases of one app (ADR-20261021101117):
// their code, configuration keys, packages, runtime settings and release
// commands. It holds no state; apid loads the inputs.
package releasediff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Inputs is everything one comparison reads. From is nil for an app's first
// release. A nil config revision or package list means unknown.
type Inputs struct {
	App        state.App
	From       *state.Deployment
	To         state.Deployment
	FromConfig *state.ConfigRevision
	ToConfig   *state.ConfigRevision
	ConfigNote string
	FromPkgs   []Package
	ToPkgs     []Package
	PkgsKnown  bool
	PkgsNote   string
}

// Compare builds the diff.
func Compare(in Inputs) api.ReleaseDiff {
	out := api.ReleaseDiff{App: in.App.Slug, To: side(in.To), Runtime: []api.ReleaseDiffField{},
		Config:       api.ReleaseDiffConfig{Variables: []api.ReleaseDiffKey{}, Secrets: []api.ReleaseDiffKey{}},
		Dependencies: api.ReleaseDiffDependencies{Added: []api.ReleaseDiffPackage{}, Removed: []api.ReleaseDiffPackage{}, Changed: []api.ReleaseDiffPackage{}},
	}
	out.Code.ToCommit = in.To.CommitSHA
	out.Database.ToReleaseCommand = strings.Join(in.To.ReleaseCommand, " ")
	if in.From == nil {
		out.Code.Changed = true
		out.Config.Note = "this is the app's first release"
		out.Dependencies.Note = "this is the app's first release"
		out.Database.Changed = out.Database.ToReleaseCommand != ""
		out.Summary = "first release"
		return out
	}
	from := side(*in.From)
	out.From = &from
	out.Code = code(*in.From, in.To)
	out.Config = config(in.FromConfig, in.ToConfig, in.ConfigNote)
	out.Dependencies = dependencies(in)
	out.Runtime = RuntimeChanges(*in.From, in.To)
	out.Database = api.ReleaseDiffDatabase{
		FromReleaseCommand: strings.Join(in.From.ReleaseCommand, " "), ToReleaseCommand: out.Database.ToReleaseCommand,
	}
	out.Database.Changed = out.Database.FromReleaseCommand != out.Database.ToReleaseCommand
	out.Summary = Summary(out)
	return out
}

func side(d state.Deployment) api.ReleaseDiffSide {
	return api.ReleaseDiffSide{DeploymentID: d.ID, Revision: d.Revision, CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		DeployedBy: d.DeployedBy, Reason: d.Reason, Tag: d.Tag, PRNumber: d.PRNumber, Commit: d.CommitSHA}
}

func code(from, to state.Deployment) api.ReleaseDiffCode {
	out := api.ReleaseDiffCode{FromCommit: from.CommitSHA, ToCommit: to.CommitSHA,
		ImageChanged: from.ImageDigest != "" && to.ImageDigest != "" && from.ImageDigest != to.ImageDigest}
	commitChanged := from.CommitSHA != to.CommitSHA && (from.CommitSHA != "" || to.CommitSHA != "")
	sourceChanged := from.SourceSHA256 != "" && to.SourceSHA256 != "" && from.SourceSHA256 != to.SourceSHA256
	out.Changed = commitChanged || sourceChanged || out.ImageChanged
	if commitChanged && from.CommitSHA != "" && to.CommitSHA != "" {
		out.CompareURL = CompareURL(from.SourceURL, to.SourceURL, from.CommitSHA, to.CommitSHA)
	}
	return out
}

var commitSHA = func(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// CompareURL links GitHub's compare page when both releases came from the
// same GitHub repository; otherwise it is empty.
func CompareURL(fromSource, toSource, fromCommit, toCommit string) string {
	repo := githubRepo(toSource)
	if repo == "" || repo != githubRepo(fromSource) || !commitSHA(fromCommit) || !commitSHA(toCommit) {
		return ""
	}
	return "https://github.com/" + repo + "/compare/" + fromCommit + "..." + toCommit
}

// githubRepo is "owner/name" for an https GitHub URL, or empty.
func githubRepo(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	for _, p := range parts[:2] {
		if strings.ContainsAny(p, "?#%") {
			return ""
		}
	}
	return parts[0] + "/" + parts[1]
}

// ConfigAt is the newest revision created at or before t; revisions may be
// in any order. It reports false when none is that old.
func ConfigAt(revisions []state.ConfigRevision, t time.Time) (*state.ConfigRevision, bool) {
	var best *state.ConfigRevision
	for i := range revisions {
		r := &revisions[i]
		if r.CreatedAt.After(t) {
			continue
		}
		if best == nil || r.Revision > best.Revision {
			best = r
		}
	}
	return best, best != nil
}

func config(from, to *state.ConfigRevision, note string) api.ReleaseDiffConfig {
	out := api.ReleaseDiffConfig{Variables: []api.ReleaseDiffKey{}, Secrets: []api.ReleaseDiffKey{}}
	if from == nil || to == nil {
		out.Note = note
		if out.Note == "" {
			out.Note = "the config history no longer reaches one of these releases"
		}
		return out
	}
	out.Known = true
	out.Variables = keyChanges(from.Variables, to.Variables, func(a, b string) bool { return a == b })
	out.Secrets = keyChanges(from.Secrets, to.Secrets, func(a, b state.ConfigSecretFingerprint) bool { return a == b })
	return out
}

// keyChanges lists keys added, removed or changed, by key; values never
// leave this function.
func keyChanges[V any](from, to map[string]V, same func(a, b V) bool) []api.ReleaseDiffKey {
	out := []api.ReleaseDiffKey{}
	for k, b := range to {
		a, ok := from[k]
		switch {
		case !ok:
			out = append(out, api.ReleaseDiffKey{Key: k, Change: api.ReleaseDiffAdded})
		case !same(a, b):
			out = append(out, api.ReleaseDiffKey{Key: k, Change: api.ReleaseDiffChanged})
		}
	}
	for k := range from {
		if _, ok := to[k]; !ok {
			out = append(out, api.ReleaseDiffKey{Key: k, Change: api.ReleaseDiffRemoved})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func dependencies(in Inputs) api.ReleaseDiffDependencies {
	out := api.ReleaseDiffDependencies{Added: []api.ReleaseDiffPackage{}, Removed: []api.ReleaseDiffPackage{}, Changed: []api.ReleaseDiffPackage{}}
	if !in.PkgsKnown {
		out.Note = in.PkgsNote
		if out.Note == "" {
			out.Note = "one of these builds has no SBOM"
		}
		return out
	}
	out.Known = true
	out.Added, out.Removed, out.Changed, out.Truncated = PackageChanges(in.FromPkgs, in.ToPkgs, api.ReleaseDiffPackagesMax)
	return out
}

// RuntimeChanges lists the runtime settings that differ. Env overrides are
// compared by key only.
func RuntimeChanges(from, to state.Deployment) []api.ReleaseDiffField {
	out := []api.ReleaseDiffField{}
	add := func(field, a, b string) {
		if a != b {
			out = append(out, api.ReleaseDiffField{Field: field, From: a, To: b})
		}
	}
	add("kind", string(from.Kind), string(to.Kind))
	add("handler", from.Handler, to.Handler)
	add("runtime_base", from.RuntimeBase, to.RuntimeBase)
	add("port", portText(from.OverridePort), portText(to.OverridePort))
	add("entrypoint", strings.Join(from.OverrideEntrypoint, " "), strings.Join(to.OverrideEntrypoint, " "))
	add("command", strings.Join(from.OverrideCmd, " "), strings.Join(to.OverrideCmd, " "))
	add("min_instances", strconv.Itoa(from.MinInstances), strconv.Itoa(to.MinInstances))
	for _, f := range []struct {
		field    string
		from, to json.RawMessage
	}{
		{"healthcheck", from.OverrideHealthcheck, to.OverrideHealthcheck},
		{"liveness_probe", from.OverrideLivenessProbe, to.OverrideLivenessProbe},
		{"readiness_probe", from.OverrideReadinessProbe, to.OverrideReadinessProbe},
		{"sidecars", from.Sidecars, to.Sidecars},
		{"workflows", from.Workflows, to.Workflows},
	} {
		if !sameJSON(f.from, f.to) {
			out = append(out, api.ReleaseDiffField{Field: f.field, From: presence(f.from), To: presence(f.to)})
		}
	}
	add("env_override_keys", strings.Join(jsonKeys(from.OverrideEnv), ","), strings.Join(jsonKeys(to.OverrideEnv), ","))
	return out
}

func portText(p int) string {
	if p == 0 {
		return "default"
	}
	return strconv.Itoa(p)
}

// sameJSON compares two JSON documents ignoring formatting; empty and null
// are the same.
func sameJSON(a, b json.RawMessage) bool {
	norm := func(raw json.RawMessage) string {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || string(raw) == "null" {
			return ""
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return string(raw)
		}
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
	return norm(a) == norm(b)
}

func presence(raw json.RawMessage) string {
	if sameJSON(raw, nil) {
		return "none"
	}
	return "set"
}

func jsonKeys(raw json.RawMessage) []string {
	var m map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Summary is the one-line description of a diff.
func Summary(d api.ReleaseDiff) string {
	var parts []string
	if d.Code.Changed {
		parts = append(parts, "new code")
	}
	if n := len(d.Config.Variables) + len(d.Config.Secrets); n > 0 {
		parts = append(parts, plural(n, "config key"))
	}
	if n := len(d.Dependencies.Added) + len(d.Dependencies.Removed) + len(d.Dependencies.Changed); n > 0 {
		label := plural(n, "package")
		if d.Dependencies.Truncated {
			label = "more than " + label
		}
		parts = append(parts, label)
	}
	if n := len(d.Runtime); n > 0 {
		parts = append(parts, plural(n, "runtime setting"))
	}
	if d.Database.Changed {
		parts = append(parts, "release command changed")
	}
	if len(parts) == 0 {
		return "no changes found"
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
