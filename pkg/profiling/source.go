package profiling

import (
	"io/fs"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// SourceProvenance comes only from the already authorized deployment row.
// ManagedPaths means the platform builds the source under /app. Dockerfile
// and image layouts have no such mapping; absolute filenames stay unlinked.
type SourceProvenance struct {
	SourceURL, CommitSHA, SourceRoot string
	ManagedPaths, Function           bool
}

// LinkSources adds bounded, commit-pinned links without reading customer files
// or contacting the Git provider. GitHub applies the viewer's repository access.
func LinkSources(out *api.ProfileResponse, origin SourceProvenance) {
	source := sourceRevision(origin)
	out.Source = &source
	root, err := sourcecontext.StorageRoot(origin.SourceRoot)
	if err != nil || root != "" && sourceFile(root, SourceProvenance{}) != root {
		source.Available = false
		source.Reason = "The deployment's source root cannot be mapped."
	}
	budget := api.ProfileMaxViewSymbolBytes
	link := func(file string, line int64) *api.ProfileSourceLocation {
		if !source.Available || line <= 0 {
			return nil
		}
		repositoryRelative := false
		if prefix := "github.com/" + source.Repository + "/"; strings.HasPrefix(file, prefix) {
			file = strings.TrimPrefix(file, prefix)
			repositoryRelative = true
		} else if strings.HasPrefix(out.Query.Runtime, "go") && !strings.HasPrefix(file, "/") && !strings.HasPrefix(file, "./") {
			// Trimmed Go standard-library and dependency module paths are not
			// repository-relative. Only the recorded module prefix is mapped.
			return nil
		}
		if !origin.ManagedPaths && !repositoryRelative {
			return nil
		}
		file = sourceFile(file, origin)
		if file == "" {
			return nil
		}
		if root != "" && !repositoryRelative {
			file = root + "/" + file
		}
		parts := strings.Split(file, "/")
		for i := range parts {
			parts[i] = url.PathEscape(parts[i])
		}
		location := api.ProfileSourceLocation{Path: file, Line: line,
			URL: "https://github.com/" + source.Repository + "/blob/" + source.CommitSHA + "/" + strings.Join(parts, "/") + "#L" + strconv.FormatInt(line, 10)}
		cost := len(location.URL) + len(location.Path)
		if cost > budget {
			return nil
		}
		budget -= cost
		return &location
	}
	for i := range out.Functions {
		f := &out.Functions[i]
		f.Source = link(f.File, f.Line)
	}
	var visit func(*api.ProfileStack)
	visit = func(frame *api.ProfileStack) {
		if frame == nil {
			return
		}
		frame.Source = link(frame.File, frame.Line)
		for _, child := range frame.Children {
			visit(child)
		}
	}
	visit(out.Flamegraph)
}

// SourceRevision returns validated, immutable GitHub provenance for an
// authorized deployment. It does not contact GitHub or verify source bytes.
func SourceRevision(origin SourceProvenance) api.ProfileSource {
	return sourceRevision(origin)
}

// LinkSourcePath resolves a repository-relative path already mapped by
// LinkSources. It revalidates the path before constructing a commit-pinned URL.
func LinkSourcePath(origin SourceProvenance, path string, line int64) (*api.ProfileSourceLocation, *api.ProfileSource) {
	source := sourceRevision(origin)
	if !source.Available || line <= 0 || path == "" || len(path) > api.ProfileMaxSymbolBytes ||
		!utf8.ValidString(path) || strings.ContainsAny(path, "\\:%<>") ||
		strings.IndexFunc(path, func(ch rune) bool { return ch < 0x20 || ch == 0x7f }) >= 0 || !fs.ValidPath(path) {
		return nil, &source
	}
	for _, part := range strings.Split(path, "/") {
		switch part {
		case "", ".", "..", "node_modules", ".venv", "venv", "__pycache__", ".git":
			return nil, &source
		}
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	location := &api.ProfileSourceLocation{Path: path, Line: line,
		URL: "https://github.com/" + source.Repository + "/blob/" + source.CommitSHA + "/" + strings.Join(parts, "/") + "#L" + strconv.FormatInt(line, 10)}
	if len(location.URL)+len(location.Path) > api.ProfileMaxViewSymbolBytes {
		return nil, &source
	}
	return location, &source
}

func sourceRevision(origin SourceProvenance) api.ProfileSource {
	unavailable := api.ProfileSource{Reason: "This deployment has no recorded GitHub repository and full commit SHA."}
	body, ok := strings.CutPrefix(origin.SourceURL, "github://")
	if !ok || len(origin.SourceURL) > api.ProfileMaxSymbolBytes {
		return unavailable
	}
	repository, revision, ok := strings.Cut(body, "@")
	parts := strings.Split(repository, "/")
	if !ok || len(parts) != 2 || !safeRepositoryPart(parts[0]) || !safeRepositoryPart(parts[1]) {
		return unavailable
	}
	sha := origin.CommitSHA
	if sha == "" {
		sha = revision
	}
	if !fullCommitSHA(sha) || revision != sha {
		return unavailable
	}
	return api.ProfileSource{Available: true, Repository: repository, CommitSHA: sha, CommitURL: "https://github.com/" + repository + "/commit/" + sha}
}

func safeRepositoryPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	for _, ch := range part {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' && ch != '.' {
			return false
		}
	}
	return true
}

func fullCommitSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, ch := range sha {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func sourceFile(file string, origin SourceProvenance) string {
	if strings.HasPrefix(file, "file:") {
		parsed, err := url.Parse(file)
		if err != nil || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/app/") {
			return ""
		}
		file = parsed.Path
	}
	if !utf8.ValidString(file) || len(file) > api.ProfileMaxSymbolBytes || strings.ContainsAny(file, "\\:%<>") || strings.IndexFunc(file, func(ch rune) bool { return ch < 0x20 || ch == 0x7f }) >= 0 {
		return ""
	}
	if strings.HasPrefix(file, "/") {
		if !origin.ManagedPaths || !strings.HasPrefix(file, "/app/") {
			return ""
		}
		file = strings.TrimPrefix(file, "/app/")
	}
	file = strings.TrimPrefix(file, "./")
	if file == "." || !fs.ValidPath(file) {
		return ""
	}
	for _, part := range strings.Split(file, "/") {
		switch part {
		case "node_modules", ".venv", "venv", "__pycache__", ".git":
			return ""
		}
	}
	// Function adapters are generated or rewritten by rootfs assembly. Their
	// line numbers do not describe the repository source, even with symbols.
	if origin.Function && (file == "node22.js" || file == "node24.js" || file == "handler.py") {
		return ""
	}
	// Python assembly preserves the customer's file by renaming it before
	// writing the adapter; the implementation keeps the original line numbers.
	if origin.ManagedPaths && origin.Function && file == ".faas-handler.py" {
		return "handler.py"
	}
	return file
}

// An aggregated comparison links the hottest mapped line on each side.
// Equal CPU picks the lower line, making links independent of map order.
func preferredSource(next, previous api.ProfileFunction) bool {
	if (next.Source != nil) != (previous.Source != nil) {
		return next.Source != nil
	}
	return next.SelfCPUSeconds > previous.SelfCPUSeconds || next.SelfCPUSeconds == previous.SelfCPUSeconds && next.Line < previous.Line
}
