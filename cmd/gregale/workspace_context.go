package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// resolveWorkspaceContext returns the repository root and the selected
// member's path when sourceDir is an explicitly selected workspace workload.
// The caller can then upload the repository as the build context while keeping
// the selected member as the build working directory.
//
// Workspace detection intentionally reuses reposcan's graph parser instead of
// maintaining a second list of package.json/pnpm/go.work conventions in the
// deploy command. A directory must be both declared by a workspace manifest
// and carry a build marker; unmarked workspace directories do not cause the
// upload scope to expand.
func resolveWorkspaceContext(repoRoot, sourceDir string) (contextRoot, sourceRoot string, ok bool, err error) {
	contextRoot, sourceRoot, ok, _, err = resolveWorkspaceContextReason(repoRoot, sourceDir)
	return contextRoot, sourceRoot, ok, err
}

// resolveWorkspaceContextReason is resolveWorkspaceContext plus, when a
// member keeps its own directory as the build context, the reason to show the
// user (ADR-20261126090000).
func resolveWorkspaceContextReason(repoRoot, sourceDir string) (contextRoot, sourceRoot string, ok bool, reason string, err error) {
	rel, err := gitRelativePath(repoRoot, sourceDir)
	if err != nil {
		return "", "", false, "", err
	}
	root, err := sourcecontext.StorageRoot(rel)
	if err != nil {
		return "", "", false, "", err
	}
	if root == "" {
		return "", "", false, "", nil
	}

	members, err := reposcan.WorkspaceMemberPaths(os.DirFS(repoRoot))
	if err != nil {
		return "", "", false, "", err
	}
	for _, member := range members {
		memberRoot, memberErr := sourcecontext.StorageRoot(member)
		if memberErr != nil {
			continue
		}
		if memberRoot == root {
			if own, why := memberDockerfileOwnsContext(os.DirFS(repoRoot), root); own {
				return "", "", false, why, nil
			}
			abs, absErr := filepath.Abs(repoRoot)
			if absErr != nil {
				return "", "", false, "", absErr
			}
			return filepath.Clean(abs), root, true, "", nil
		}
	}
	return "", "", false, "", nil
}

// memberDockerfileOwnsContext reports whether a workspace member's own
// Dockerfile was written for the member directory as its build context, as
// `docker build <member>` or Compose's `build: ./<member>` would send it.
// That holds when some COPY/ADD source exists only in the member and none
// exists only at the repository root. Anything else, including a Dockerfile
// whose sources exist in both places or cannot be resolved, keeps the
// repository context of ADR-144, which Dockerfiles that copy a root lockfile
// or sibling packages need.
func memberDockerfileOwnsContext(repo fs.FS, member string) (bool, string) {
	body, err := fs.ReadFile(repo, path.Join(member, "Dockerfile"))
	if err != nil {
		return false, ""
	}
	var memberOnly, rootOnly []string
	for _, source := range dockerfileContextSources(body) {
		inMember, inRoot := contextSourceExists(repo, member, source), contextSourceExists(repo, ".", source)
		switch {
		case inMember && !inRoot:
			memberOnly = append(memberOnly, source)
		case inRoot && !inMember:
			rootOnly = append(rootOnly, source)
		}
	}
	if len(memberOnly) == 0 || len(rootOnly) > 0 {
		return false, ""
	}
	sort.Strings(memberOnly)
	memberOnly = compactStrings(memberOnly)
	shown := memberOnly
	if len(shown) > 3 {
		shown = shown[:3]
	}
	verb := "exist"
	if len(memberOnly) == 1 {
		verb = "exists"
	}
	return true, fmt.Sprintf("building %s from its own directory: its Dockerfile copies %s, which %s there and not at the repository root",
		member, strings.Join(shown, ", "), verb)
}

func compactStrings(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// dockerfileContextSources lists the build-context paths a Dockerfile's
// COPY and ADD instructions read. Stage copies (--from), URLs, heredocs and
// paths with build variables are skipped: they say nothing about the context.
func dockerfileContextSources(dockerfile []byte) []string {
	var out []string
	for _, line := range dockerfileInstructions(dockerfile) {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") && !strings.EqualFold(fields[0], "ADD") {
			continue
		}
		args, fromStage := fields[1:], false
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			if strings.HasPrefix(strings.ToLower(args[0]), "--from") {
				fromStage = true
			}
			args = args[1:]
		}
		if fromStage || len(args) == 0 {
			continue
		}
		if strings.HasPrefix(args[0], "[") {
			var list []string
			if json.Unmarshal([]byte(strings.Join(args, " ")), &list) != nil {
				continue
			}
			args = list
		}
		if len(args) < 2 {
			continue
		}
		for _, source := range args[:len(args)-1] {
			if strings.Contains(source, "://") || strings.HasPrefix(source, "git@") || strings.HasPrefix(source, "<<") || strings.Contains(source, "$") {
				continue
			}
			clean := path.Clean(strings.TrimPrefix(source, "/"))
			if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
				continue
			}
			out = append(out, clean)
		}
	}
	return out
}

// dockerfileInstructions joins continuation lines and drops comments.
func dockerfileInstructions(dockerfile []byte) []string {
	var out []string
	var current strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if current.Len() == 0 && strings.HasPrefix(line, "#") {
			continue
		}
		if cont, ok := strings.CutSuffix(line, "\\"); ok {
			current.WriteString(cont)
			current.WriteByte(' ')
			continue
		}
		current.WriteString(line)
		if s := strings.TrimSpace(current.String()); s != "" {
			out = append(out, s)
		}
		current.Reset()
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// contextSourceExists reports whether a COPY source names something below
// base. A glob matches when any entry matches it.
func contextSourceExists(repo fs.FS, base, source string) bool {
	name := path.Join(base, source)
	if strings.ContainsAny(source, "*?[") {
		matches, err := fs.Glob(repo, name)
		return err == nil && len(matches) > 0
	}
	_, err := fs.Stat(repo, name)
	return err == nil
}
