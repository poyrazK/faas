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
	"regexp"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
)

// resolveWorkspaceContext returns the repository root and the selected
// member's path when sourceDir is an explicitly selected workspace workload.
// The caller can then upload the repository as the build context while keeping
// the selected member as the build working directory. It reads the working
// tree; deploy uses resolveWorkspaceContextReason with the tree it ships.
//
// Workspace detection intentionally reuses reposcan's graph parser instead of
// maintaining a second list of package.json/pnpm/go.work conventions in the
// deploy command. A directory must be both declared by a workspace manifest
// and carry a build marker; unmarked workspace directories do not cause the
// upload scope to expand.
func resolveWorkspaceContext(repoRoot, sourceDir string) (contextRoot, sourceRoot string, ok bool, err error) {
	contextRoot, sourceRoot, ok, _, err = resolveWorkspaceContextReason(repoRoot, sourceDir, worktreeContextTree{os.DirFS(repoRoot)})
	return contextRoot, sourceRoot, ok, err
}

// resolveWorkspaceContextReason is resolveWorkspaceContext plus the reason to
// show the user when sourceDir is a workspace member, deciding the member's
// Dockerfile context from tree: the files the deploy will upload
// (ADR-20261126090000).
func resolveWorkspaceContextReason(repoRoot, sourceDir string, tree contextTree) (contextRoot, sourceRoot string, ok bool, reason string, err error) {
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
			own, why := memberDockerfileOwnsContext(tree, root)
			if own {
				return "", "", false, why, nil
			}
			abs, absErr := filepath.Abs(repoRoot)
			if absErr != nil {
				return "", "", false, "", absErr
			}
			return filepath.Clean(abs), root, true, why, nil
		}
	}
	return "", "", false, "", nil
}

// deployContextTree is the tree a deploy of sourceDir uploads: the working
// tree with --worktree or when the repository has no committed source to ship,
// otherwise committed HEAD.
func deployContextTree(repoRoot, sourceDir string, worktree, explicitHead bool) contextTree {
	if !worktree {
		var prov zeroConfigProvenance
		var ok bool
		if explicitHead {
			var err error
			prov, err = resolveExplicitHeadProvenance(sourceDir)
			ok = err == nil
		} else {
			prov, ok, _ = resolveZeroConfigProvenance(sourceDir)
		}
		if ok && prov.SHA != "" {
			return &headContextTree{root: prov.Root, rev: prov.SHA}
		}
	}
	return worktreeContextTree{os.DirFS(repoRoot)}
}

// contextTree answers the questions the context decision asks of the files a
// deploy uploads. Names are slash-separated and relative to the repository.
type contextTree interface {
	readFile(name string) ([]byte, error)
	exists(name string) bool
	// glob reports whether pattern, relative to dir, matches any entry.
	glob(dir, pattern string) bool
}

type worktreeContextTree struct{ fsys fs.FS }

func (t worktreeContextTree) readFile(name string) ([]byte, error) { return fs.ReadFile(t.fsys, name) }

func (t worktreeContextTree) exists(name string) bool {
	_, err := fs.Stat(t.fsys, name)
	return err == nil
}

func (t worktreeContextTree) glob(dir, pattern string) bool {
	sub, err := fs.Sub(t.fsys, dir)
	if err != nil {
		return false
	}
	matches, err := fs.Glob(sub, pattern)
	return err == nil && len(matches) > 0
}

// headContextTree reads a committed revision. Its path index is loaded on
// first use, so a member without a Dockerfile costs one `git show`.
type headContextTree struct {
	root, rev string
	paths     map[string]bool
	loadErr   error
}

func (t *headContextTree) readFile(name string) ([]byte, error) {
	out, err := runGitCmd(t.root, "show", t.rev+":"+name)
	return []byte(out), err
}

func (t *headContextTree) index() map[string]bool {
	if t.paths != nil || t.loadErr != nil {
		return t.paths
	}
	out, err := runGitCmd(t.root, "ls-tree", "-r", "-z", "--name-only", t.rev)
	if err != nil {
		t.loadErr = err
		return nil
	}
	t.paths = map[string]bool{}
	for _, name := range strings.Split(out, "\x00") {
		for name != "" && name != "." && !t.paths[name] {
			t.paths[name] = true
			name = path.Dir(name)
		}
	}
	return t.paths
}

func (t *headContextTree) exists(name string) bool { return t.index()[name] }

func (t *headContextTree) glob(dir, pattern string) bool {
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	for name := range t.index() {
		rel, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if matched, err := path.Match(pattern, rel); err == nil && matched {
			return true
		}
	}
	return false
}

// memberDockerfileOwnsContext reports whether a workspace member's own
// Dockerfile was written for the member directory as its build context, as
// `docker build <member>` or Compose's `build: ./<member>` would send it.
// That holds when some COPY/ADD source exists only in the member and none
// exists only at the repository root. Anything else, including a Dockerfile
// whose sources exist in both places or cannot be resolved, keeps the
// repository context of ADR-144, which Dockerfiles that copy a root lockfile
// or sibling packages need. The reason names the deciding sources either way.
func memberDockerfileOwnsContext(tree contextTree, member string) (bool, string) {
	body, err := tree.readFile(path.Join(member, "Dockerfile"))
	if err != nil {
		return false, fmt.Sprintf("building %s from the repository root, as a workspace member", member)
	}
	var memberOnly, rootOnly []string
	for _, source := range dockerfileContextSources(body) {
		inMember, inRoot := contextSourceExists(tree, member, source), contextSourceExists(tree, ".", source)
		switch {
		case inMember && !inRoot:
			memberOnly = append(memberOnly, source)
		case inRoot && !inMember:
			rootOnly = append(rootOnly, source)
		}
	}
	switch {
	case len(rootOnly) > 0:
		return false, fmt.Sprintf("building %s from the repository root: its Dockerfile copies %s, %s only there", member, sourceList(rootOnly), existVerb(rootOnly))
	case len(memberOnly) > 0:
		return true, fmt.Sprintf("building %s from its own directory: its Dockerfile copies %s, %s there and not at the repository root", member, sourceList(memberOnly), existVerb(memberOnly))
	default:
		return false, fmt.Sprintf("building %s from the repository root, as a workspace member: its Dockerfile copies nothing that exists only in %s", member, member)
	}
}

// sourceList names up to three sources and counts the rest.
func sourceList(sources []string) string {
	sources = append([]string(nil), sources...)
	sort.Strings(sources)
	unique := sources[:0]
	for i, s := range sources {
		if i == 0 || s != sources[i-1] {
			unique = append(unique, s)
		}
	}
	if len(unique) <= 3 {
		return strings.Join(unique, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(unique[:3], ", "), len(unique)-3)
}

func existVerb(sources []string) string {
	if len(sources) == 1 {
		return "which exists"
	}
	return "which exist"
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

var dockerfileHeredoc = regexp.MustCompile(`<<(-?)["']?([A-Za-z_][A-Za-z0-9_]*)["']?`)

// dockerfileInstructions returns one string per instruction, as Docker's
// parser sees them: continuation lines joined, comment and blank lines inside
// a continuation dropped, and heredoc bodies skipped.
func dockerfileInstructions(dockerfile []byte) []string {
	var out []string
	var current strings.Builder
	var heredocs []string
	scanner := bufio.NewScanner(bytes.NewReader(dockerfile))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if len(heredocs) > 0 {
			if line == heredocs[0] {
				heredocs = heredocs[1:]
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if cont, ok := strings.CutSuffix(line, "\\"); ok {
			current.WriteString(cont)
			current.WriteByte(' ')
			continue
		}
		current.WriteString(line)
		instruction := strings.TrimSpace(current.String())
		current.Reset()
		out = append(out, instruction)
		for _, m := range dockerfileHeredoc.FindAllStringSubmatch(instruction, -1) {
			heredocs = append(heredocs, m[2])
		}
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// contextSourceExists reports whether a COPY source names something below
// base. A glob matches when any entry matches it; base is never a pattern.
func contextSourceExists(tree contextTree, base, source string) bool {
	if strings.ContainsAny(source, "*?[") {
		return tree.glob(base, source)
	}
	return tree.exists(path.Join(base, source))
}
