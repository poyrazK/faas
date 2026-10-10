// Package devpatch decides whether a developer edit could be applied as a
// live source patch instead of a rebuild (ADR-740).
//
// ClassifyRailpackPlan runs inside the builder VM against the plan Railpack
// generated for that build. Evaluate runs in apid against the files a
// developer sync changed. Both are pure so they behave identically wherever
// they run and are fully unit-testable.
package devpatch

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// alwaysRebuild are source-root files that configure the build itself, so a
// change must always go through a real build even when no plan step names
// them.
var alwaysRebuild = []string{"railpack.json", "gregale.yaml", ".gregaleignore"}

// railpackPlan is the subset of Railpack's BuildPlan (core/plan, v0.38.0)
// needed to decide whether the application source is copied unchanged.
type railpackPlan struct {
	Steps  []railpackStep `json:"steps"`
	Deploy struct {
		Inputs []railpackLayer `json:"inputs"`
	} `json:"deploy"`
}

type railpackStep struct {
	Name     string            `json:"name"`
	Inputs   []railpackLayer   `json:"inputs"`
	Commands []json.RawMessage `json:"commands"`
}

type railpackLayer struct {
	Step    string   `json:"step"`
	Local   bool     `json:"local"`
	Include []string `json:"include"`
}

func (l railpackLayer) includesAll() bool {
	if len(l.Include) == 0 {
		return true
	}
	for _, include := range l.Include {
		if include == "." || include == "./" || include == "/app" || include == "/app/" {
			return true
		}
	}
	return false
}

// isExec reports a command that runs a process. Railpack serializes exec
// commands as {"cmd": ...}; copy, path and file commands never transform the
// application source. A string command without a COPY/PATH/FILE prefix is
// also an exec command.
func isExec(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		upper := strings.ToUpper(strings.TrimSpace(text))
		for _, prefix := range []string{"COPY", "PATH", "FILE"} {
			if strings.HasPrefix(upper, prefix) {
				return false
			}
		}
		return true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return true // unknown shape: assume it can change the source
	}
	if _, exec := fields["cmd"]; exec {
		return true
	}
	// Only the known non-exec shapes are safe: copy has "src", path and
	// file have "path". Anything else is assumed to change the source.
	_, copyCommand := fields["src"]
	_, pathOrFile := fields["path"]
	return !copyCommand && !pathOrFile
}

// localCopySource returns the source-relative path a copy command reads from
// the local build context. A copy with "image" reads another image instead.
func localCopySource(raw json.RawMessage) (string, bool) {
	var src string
	var text string
	if json.Unmarshal(raw, &text) == nil {
		fields := strings.Fields(text)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") {
			return "", false
		}
		src = fields[1]
	} else {
		var copyCommand struct {
			Image string `json:"image"`
			Src   string `json:"src"`
		}
		if json.Unmarshal(raw, &copyCommand) != nil || copyCommand.Src == "" || copyCommand.Image != "" {
			return "", false
		}
		src = copyCommand.Src
	}
	src = strings.TrimPrefix(path.Clean("/"+src), "/")
	if src == "" {
		return "", false
	}
	return src, true
}

func notVerbatim(reason string) api.DevPatchSourceMap {
	return api.DevPatchSourceMap{Version: api.DevPatchSourceMapVersion, Reason: reason}
}

// ClassifyRailpackPlan reports whether the build described by a Railpack plan
// copies the application source into the image unchanged.
//
// The source is verbatim when no step that receives the full local source,
// directly or through another step, runs a command, and the deploy image
// includes the full output of such a step. Local inputs of other steps (for
// example package.json and lockfiles for the install step) become
// RebuildPaths.
func ClassifyRailpackPlan(data []byte) api.DevPatchSourceMap {
	var plan railpackPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return notVerbatim(api.DevPatchReasonPlanUnreadable)
	}
	carriesSource := make(map[string]bool, len(plan.Steps))
	rebuild := map[string]bool{}
	for _, name := range alwaysRebuild {
		rebuild[name] = true
	}
	foundSource := false
	// Steps reference earlier steps by name, so one ordered pass sees every
	// upstream step before the steps that consume it.
	for _, step := range plan.Steps {
		source := false
		for _, input := range step.Inputs {
			switch {
			case input.Local && input.includesAll():
				source, foundSource = true, true
			case input.Local:
				for _, include := range input.Include {
					rebuild[strings.TrimPrefix(path.Clean(include), "/app/")] = true
				}
			case input.Step != "" && carriesSource[input.Step] && input.includesAll():
				source = true
			}
		}
		if !source {
			// Railpack's install steps copy manifests and lockfiles from the
			// local context with copy commands rather than local layers.
			for _, command := range step.Commands {
				if src, ok := localCopySource(command); ok {
					rebuild[src] = true
				}
			}
			continue
		}
		for _, command := range step.Commands {
			if isExec(command) {
				return notVerbatim(api.DevPatchReasonBuildCommand)
			}
		}
		carriesSource[step.Name] = true
	}
	if !foundSource {
		return notVerbatim(api.DevPatchReasonNoSourceLayer)
	}
	deployed := false
	for _, input := range plan.Deploy.Inputs {
		if (input.Local || carriesSource[input.Step]) && input.includesAll() {
			deployed = true
		}
	}
	if !deployed {
		return notVerbatim(api.DevPatchReasonSourceNotDeployed)
	}
	paths := make([]string, 0, len(rebuild))
	for p := range rebuild {
		if p != "" && p != "." {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return api.DevPatchSourceMap{
		Version:      api.DevPatchSourceMapVersion,
		Verbatim:     true,
		ImageDir:     api.DevPatchImageDir,
		RebuildPaths: paths,
	}
}

// Change is one source path a developer sync added, modified or deleted,
// relative to the uploaded archive root.
type Change struct {
	Path    string
	Deleted bool
	// Regular is false for directories, symlinks and other non-file entries.
	Regular bool
	Dir     bool
	Size    int64
}

// Entry is one source archive entry: its tar typeflag, mode, size, and the
// content digest for regular files.
type Entry struct {
	Type   byte
	Mode   int64
	Size   int64
	Digest string
}

// Diff lists what changed from base to current, sorted by path, so a patch is
// always complete relative to the base build no matter how many syncs
// happened in between.
func Diff(base, current map[string]Entry) []Change {
	changes := make([]Change, 0)
	for name, entry := range current {
		if previous, ok := base[name]; ok && previous == entry {
			continue
		}
		changes = append(changes, Change{
			Path:    name,
			Regular: entry.Type == tarTypeReg,
			Dir:     entry.Type == tarTypeDir,
			Size:    entry.Size,
		})
	}
	for name, entry := range base {
		if _, ok := current[name]; !ok && entry.Type != tarTypeDir {
			changes = append(changes, Change{Path: name, Deleted: true})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

const (
	tarTypeReg = '0'
	tarTypeDir = '5'
)

// Evaluate reports whether changes could be applied as a live patch to a
// deployment built with sourceMap. sourceRoot is the archive-relative
// directory the build used ("" for the archive root); changes outside it do
// not reach the image and need no patch.
func Evaluate(sourceMap *api.DevPatchSourceMap, sourceRoot string, changes []Change) api.DevPatchPreview {
	if sourceMap == nil {
		return api.DevPatchPreview{Reason: api.DevPatchReasonNoLiveBuild}
	}
	if sourceMap.Version != api.DevPatchSourceMapVersion {
		return api.DevPatchPreview{Reason: api.DevPatchReasonUnsupportedVersion}
	}
	if !sourceMap.Verbatim {
		return api.DevPatchPreview{Reason: sourceMap.Reason}
	}
	root := strings.Trim(path.Clean("/"+sourceRoot), "/")
	var preview api.DevPatchPreview
	for _, change := range changes {
		rel, inside := Relative(root, change.Path)
		if !inside {
			continue
		}
		if change.Dir {
			continue
		}
		if !change.Deleted && !change.Regular {
			return api.DevPatchPreview{Reason: api.DevPatchReasonUnsupportedEntry}
		}
		if matchesAny(sourceMap.RebuildPaths, rel) {
			return api.DevPatchPreview{Reason: api.DevPatchReasonRebuildInput}
		}
		preview.ChangedPaths++
		preview.PatchBytes += change.Size
	}
	if preview.ChangedPaths > api.DevPatchMaxEntries || preview.PatchBytes > api.DevPatchMaxBytes {
		return api.DevPatchPreview{Reason: api.DevPatchReasonTooLarge, ChangedPaths: preview.ChangedPaths, PatchBytes: preview.PatchBytes}
	}
	preview.Eligible = true
	return preview
}

// Relative returns name relative to the archive-relative root directory and
// whether it lies inside it ("" root means the whole archive).
func Relative(root, name string) (string, bool) {
	name = strings.Trim(path.Clean("/"+name), "/")
	if root == "" {
		return name, true
	}
	if name == root {
		return "", true
	}
	if strings.HasPrefix(name, root+"/") {
		return strings.TrimPrefix(name, root+"/"), true
	}
	return "", false
}

// matchesAny reports an exact path, a glob match, or a path inside a listed
// directory (a rebuild input such as "prisma" covers "prisma/schema.prisma").
func matchesAny(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if rel == pattern || strings.HasPrefix(rel, pattern+"/") {
			return true
		}
		if ok, err := path.Match(pattern, rel); err == nil && ok {
			return true
		}
		if ok, err := path.Match(pattern, path.Base(rel)); err == nil && ok && !strings.Contains(pattern, "/") {
			return true
		}
	}
	return false
}
