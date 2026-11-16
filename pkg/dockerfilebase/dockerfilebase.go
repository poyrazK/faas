// Package dockerfilebase lists the image repositories a Dockerfile pulls
// (ADR-20261110210000 slice 3j-4): FROM images, COPY --from images and
// RUN --mount from= images, with ARG values expanded. The control plane
// records them for a deployment so secretd lets the deployment's builds pull
// only those repositories with the app's registry credential.
//
// The parser is deliberately small. It reads what BuildKit's Dockerfile
// frontend reads for these instructions (parser directives, line
// continuations, comments, heredocs, ARG scoping, ${VAR}, ${VAR:-word} and
// ${VAR:+word}) and nothing else. A reference it cannot resolve is reported
// in Result.Unresolved, never guessed.
package dockerfilebase

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxRepositories bounds one deployment's recorded scope; it matches the
// deployments.build_registry_scope CHECK.
const MaxRepositories = 64

// ErrTooManyRepositories is returned when a source names more repositories
// than MaxRepositories.
var ErrTooManyRepositories = errors.New("dockerfilebase: too many image repositories")

// Result is what one or more Dockerfiles pull.
type Result struct {
	// Repositories are canonical "host/repository" keys, sorted and
	// distinct (RepositoryKey).
	Repositories []string
	// Unresolved counts references that did not expand to an image name
	// (an ARG without a value, a malformed reference).
	Unresolved int
}

// builderPlatformArgs are BuildKit's automatic platform ARGs for the
// x86_64 builder VMs; they are defined in the global scope.
var builderPlatformArgs = map[string]string{
	"TARGETPLATFORM": "linux/amd64", "TARGETOS": "linux", "TARGETARCH": "amd64", "TARGETVARIANT": "",
	"BUILDPLATFORM": "linux/amd64", "BUILDOS": "linux", "BUILDARCH": "amd64", "BUILDVARIANT": "",
}

// Parse reads one Dockerfile. buildArgs are the build's --build-arg values;
// they override ARG defaults of the same name.
func Parse(dockerfile []byte, buildArgs map[string]string) (Result, error) {
	set := map[string]bool{}
	unresolved, err := parseInto(dockerfile, buildArgs, set)
	if err != nil {
		return Result{}, err
	}
	return finish(set, unresolved)
}

// Merge combines results, for a source with several Dockerfiles.
func Merge(results ...Result) (Result, error) {
	set := map[string]bool{}
	unresolved := 0
	for _, r := range results {
		for _, repo := range r.Repositories {
			set[repo] = true
		}
		unresolved += r.Unresolved
	}
	return finish(set, unresolved)
}

func finish(set map[string]bool, unresolved int) (Result, error) {
	if len(set) > MaxRepositories {
		return Result{}, fmt.Errorf("%w: %d, at most %d", ErrTooManyRepositories, len(set), MaxRepositories)
	}
	out := make([]string, 0, len(set))
	for repo := range set {
		out = append(out, repo)
	}
	sort.Strings(out)
	return Result{Repositories: out, Unresolved: unresolved}, nil
}

type instruction struct {
	keyword string
	args    string
}

func parseInto(dockerfile []byte, buildArgs map[string]string, set map[string]bool) (int, error) {
	instructions, err := instructions(dockerfile)
	if err != nil {
		return 0, err
	}
	global := map[string]string{}
	for k, v := range builderPlatformArgs {
		global[k] = v
	}
	stages := map[string]bool{}
	stageCount := 0
	var stageArgs map[string]string
	unresolved := 0
	add := func(raw string, vars map[string]string) {
		ref, ok := expand(raw, vars)
		if !ok || ref == "" {
			unresolved++
			return
		}
		lower := strings.ToLower(ref)
		if lower == "scratch" || stages[lower] {
			return
		}
		if n, err := strconv.Atoi(ref); err == nil && n >= 0 && n < stageCount {
			return
		}
		key, err := Canonical(ref)
		if err != nil {
			unresolved++
			return
		}
		set[key] = true
	}
	for _, in := range instructions {
		switch in.keyword {
		case "ARG":
			scope := global
			if stageArgs != nil {
				scope = stageArgs
			}
			for _, decl := range splitWords(in.args) {
				name, value, hasDefault := strings.Cut(decl, "=")
				if v, ok := buildArgs[name]; ok {
					scope[name] = v
					continue
				}
				if hasDefault {
					expanded, _ := expand(unquote(value), mergeVars(global, stageArgs))
					scope[name] = expanded
				} else if stageArgs != nil {
					// A stage's ARG without a default reuses the global value.
					if v, ok := global[name]; ok {
						scope[name] = v
					}
				}
			}
		case "FROM":
			words := splitWords(in.args)
			image, alias := "", ""
			for i := 0; i < len(words); i++ {
				w := words[i]
				if strings.HasPrefix(w, "--") {
					continue
				}
				if image == "" {
					image = w
					continue
				}
				if strings.EqualFold(w, "AS") && i+1 < len(words) {
					alias = strings.ToLower(words[i+1])
					break
				}
			}
			if image != "" {
				add(image, global)
			}
			stageCount++
			if alias != "" {
				stages[alias] = true
			}
			stageArgs = map[string]string{}
		case "COPY":
			for _, w := range splitWords(in.args) {
				if v, ok := strings.CutPrefix(w, "--from="); ok {
					add(v, mergeVars(global, stageArgs))
				}
				if !strings.HasPrefix(w, "--") {
					break
				}
			}
		case "RUN":
			for _, w := range splitWords(in.args) {
				if !strings.HasPrefix(w, "--") {
					break
				}
				spec, ok := strings.CutPrefix(w, "--mount=")
				if !ok {
					continue
				}
				for _, field := range strings.Split(spec, ",") {
					if v, ok := strings.CutPrefix(field, "from="); ok {
						add(v, mergeVars(global, stageArgs))
					}
				}
			}
		}
	}
	return unresolved, nil
}

func mergeVars(global, stage map[string]string) map[string]string {
	out := make(map[string]string, len(global)+len(stage))
	for k, v := range global {
		out[k] = v
	}
	for k, v := range stage {
		out[k] = v
	}
	return out
}

var (
	directiveRE = regexp.MustCompile(`^#\s*([a-zA-Z][a-zA-Z0-9]*)\s*=\s*(.+?)\s*$`)
	heredocRE   = regexp.MustCompile(`<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)`)
)

// maxDockerfileBytes bounds one Dockerfile.
const maxDockerfileBytes = 1 << 20

// instructions splits a Dockerfile into instructions: directives read,
// comments dropped, continuations joined, heredoc bodies skipped.
func instructions(dockerfile []byte) ([]instruction, error) {
	if len(dockerfile) > maxDockerfileBytes {
		return nil, fmt.Errorf("dockerfilebase: Dockerfile over %d bytes", maxDockerfileBytes)
	}
	sc := bufio.NewScanner(bytes.NewReader(dockerfile))
	sc.Buffer(make([]byte, 0, 64*1024), maxDockerfileBytes)
	escape := byte('\\')
	directives := true
	var out []instruction
	var pending strings.Builder
	continuing := false
	var heredocs []heredoc
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if len(heredocs) > 0 {
			h := heredocs[0]
			candidate := line
			if h.stripTabs {
				candidate = strings.TrimLeft(candidate, "\t")
			}
			if candidate == h.word {
				heredocs = heredocs[1:]
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if directives {
			if m := directiveRE.FindStringSubmatch(trimmed); m != nil {
				if strings.EqualFold(m[1], "escape") && (m[2] == "`" || m[2] == `\`) {
					escape = m[2][0]
				}
				continue
			}
			directives = false
		}
		if strings.HasPrefix(trimmed, "#") {
			// A comment, also inside a continuation.
			continue
		}
		if trimmed == "" {
			// Blank lines end nothing, also inside a continuation.
			continue
		}
		body := strings.TrimRight(line, " \t")
		if strings.HasSuffix(body, string(escape)) {
			// The escape and the line break go; the next line follows as is.
			pending.WriteString(strings.TrimSuffix(body, string(escape)))
			continuing = true
			continue
		}
		pending.WriteString(body)
		continuing = false
		full := strings.TrimSpace(pending.String())
		pending.Reset()
		if full == "" {
			continue
		}
		keyword, args, _ := strings.Cut(full, " ")
		keyword = strings.ToUpper(keyword)
		args = strings.TrimSpace(args)
		if keyword == "RUN" || keyword == "COPY" || keyword == "ADD" {
			heredocs = append(heredocs, parseHeredocs(args)...)
		}
		out = append(out, instruction{keyword: keyword, args: args})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("dockerfilebase: read Dockerfile: %w", err)
	}
	if continuing {
		full := strings.TrimSpace(pending.String())
		if keyword, args, _ := strings.Cut(full, " "); keyword != "" {
			out = append(out, instruction{keyword: strings.ToUpper(keyword), args: strings.TrimSpace(args)})
		}
	}
	return out, nil
}

type heredoc struct {
	word      string
	stripTabs bool
}

func parseHeredocs(args string) []heredoc {
	var out []heredoc
	for _, m := range heredocRE.FindAllStringSubmatch(args, -1) {
		if m[2] != m[4] {
			continue
		}
		out = append(out, heredoc{word: m[3], stripTabs: m[1] == "-"})
	}
	return out
}

// splitWords splits on whitespace, keeping quoted runs together.
func splitWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// expand substitutes $VAR, ${VAR}, ${VAR:-word} and ${VAR:+word}. An unset
// variable is empty, as in BuildKit. ok is false for a malformed expression.
func expand(s string, vars map[string]string) (string, bool) {
	s = unquote(s)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		if s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", false
			}
			expr := s[i+2 : i+2+end]
			i += 2 + end
			name, op, word := expr, "", ""
			if j := strings.Index(expr, ":-"); j >= 0 {
				name, op, word = expr[:j], ":-", expr[j+2:]
			} else if j := strings.Index(expr, ":+"); j >= 0 {
				name, op, word = expr[:j], ":+", expr[j+2:]
			}
			if !validName(name) {
				return "", false
			}
			v := vars[name]
			switch op {
			case ":-":
				if v == "" {
					v = word
				}
			case ":+":
				if v != "" {
					v = word
				}
			}
			b.WriteString(v)
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j == i+1 {
			b.WriteByte(c)
			continue
		}
		b.WriteString(vars[s[i+1:j]])
		i = j - 1
	}
	return b.String(), true
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

var (
	hostRE       = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)
	repositoryRE = regexp.MustCompile(`^[a-z0-9]+([._-]+[a-z0-9]+)*(/[a-z0-9]+([._-]+[a-z0-9]+)*)*$`)
)

// Canonical turns an image reference ("node:22", "ghcr.io/acme/base@sha256:…")
// into its repository key: lower-case registry host, Docker Hub's aliases
// folded into docker.io, Docker Hub's official images under library/.
func Canonical(ref string) (string, error) {
	name := ref
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i:], "/") {
		name = name[:i]
	}
	host, repository := "docker.io", name
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, repository = strings.ToLower(first), rest
	}
	return RepositoryKey(host, repository)
}

// RepositoryKey is the canonical "host/repository" key secretd compares a
// pull against.
func RepositoryKey(host, repository string) (string, error) {
	host = NormalizeHost(host)
	if host == "docker.io" && !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	if !hostRE.MatchString(host) || !repositoryRE.MatchString(repository) || len(host)+1+len(repository) > 255 {
		return "", fmt.Errorf("dockerfilebase: not an image repository: %q", host+"/"+repository)
	}
	return host + "/" + repository, nil
}

// NormalizeHost lower-cases a registry host and folds Docker Hub's API and
// index hosts into docker.io.
func NormalizeHost(host string) string {
	host = strings.ToLower(host)
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return "docker.io"
	}
	return host
}
