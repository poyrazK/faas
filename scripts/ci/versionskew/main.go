// Command versionskew fails a change that would break a daemon or guest still
// running the previous release (docs/release-compatibility.md, the N±1 rule).
//
// Units deploy independently, so release N runs beside release N-1: an N-1
// schedd calls an N vmmd, an N host writes app.json for an N-1 guest-init
// restored from a snapshot. Two contracts cross those boundaries:
//
//   - proto: the gRPC services in api/proto. Compared as protoc descriptor
//     sets. Removing an RPC or field needs a deprecation in the previous
//     release first; types, numbers, names, cardinality and oneofs are fixed.
//   - guest: the JSON files host and guest exchange (pkg/api AppManifest,
//     BuildManifest, BuildDone). Keys are only ever added, and an added key
//     under an existing object must be omitempty.
//
// Both are compared against the previous release tag, not the pull request
// base, because what must keep working is the release already deployed: a
// field added and removed again between two releases broke nobody. A finding
// that the base already has is reported as pre-existing and does not fail, so
// one bad merge does not turn every later pull request red.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// finding is one incompatibility. Key is stable across runs so it can be
// matched against the base's findings and the allowlist.
type finding struct {
	Key     string
	Message string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, w io.Writer) int {
	fs := flag.NewFlagSet("versionskew", flag.ContinueOnError)
	repo := fs.String("repo", ".", "repository root")
	checks := fs.String("check", "proto,guest", "comma-separated checks: proto, guest")
	base := fs.String("base", os.Getenv("VERSIONSKEW_BASE"), "pull request base commit (default: merge-base with origin/main)")
	release := fs.String("release", envOr("VERSIONSKEW_RELEASE", "auto"), "previous release ref; auto = newest v* tag that is an ancestor of -base")
	allowPath := fs.String("allow", "scripts/ci/versionskew/allow.txt", "allowlist of accepted finding keys, relative to -repo")
	protoRoot := fs.String("proto-root", "api/proto", "proto source root, relative to -repo")
	guestPkg := fs.String("guest-package", "pkg/api", "package holding the guest protocol types, relative to -repo")
	guestTypes := fs.String("guest-types", strings.Join(defaultGuestTypes, ","), "comma-separated root types of the guest protocol")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	baseRef, err := resolveBase(*repo, *base)
	if err != nil {
		say(w, "::error::versionskew: %v\n", err)
		return 2
	}
	releaseRef, err := resolveRelease(w, *repo, *release, baseRef)
	if err != nil {
		say(w, "::error::versionskew: %v\n", err)
		return 2
	}
	allow, err := readAllowlist(*repo + "/" + *allowPath)
	if err != nil {
		say(w, "::error::versionskew: %v\n", err)
		return 2
	}
	say(w, "versionskew: previous release %s, base %s, head = working tree\n", releaseRef, short(baseRef))

	var introduced, preexisting []finding
	for _, check := range strings.Split(*checks, ",") {
		var c skewCheck
		switch check = strings.TrimSpace(check); check {
		case "proto":
			c = skewCheck{
				load:    func(ref string) (any, error) { return loadProto(*repo, *protoRoot, ref) },
				compare: func(old, cur any) []finding { return compareProto(old.(*protoSchema), cur.(*protoSchema)) },
				size: func(s any) string {
					p := s.(*protoSchema)
					return fmt.Sprintf("%d services, %d messages, %d enums", len(p.services), len(p.messages), len(p.enums))
				},
			}
		case "guest":
			roots := splitList(*guestTypes)
			c = skewCheck{
				load:    func(ref string) (any, error) { return loadGuest(*repo, *guestPkg, ref, roots) },
				compare: func(old, cur any) []finding { return compareGuest(old.(guestSchema), cur.(guestSchema)) },
				size: func(s any) string {
					return fmt.Sprintf("%d JSON keys under %s", len(s.(guestSchema)), strings.Join(roots, ", "))
				},
			}
		default:
			say(w, "::error::versionskew: unknown check %q\n", check)
			return 2
		}
		in, pre, err := c.classify(w, check, releaseRef, baseRef)
		if err != nil {
			say(w, "::error::versionskew %s: %v\n", check, err)
			return 2
		}
		introduced = append(introduced, in...)
		preexisting = append(preexisting, pre...)
	}
	return report(w, introduced, preexisting, allow, releaseRef)
}

// skewCheck loads one contract at a ref ("" = working tree) and compares two
// loaded versions.
type skewCheck struct {
	load    func(ref string) (any, error)
	compare func(old, cur any) []finding
	size    func(schema any) string
}

// classify splits release→head findings into the ones this change introduces
// and the ones the base already carried.
func (c skewCheck) classify(w io.Writer, name, releaseRef, baseRef string) (introduced, preexisting []finding, err error) {
	load, compare := c.load, c.compare
	old, err := load(releaseRef)
	if err != nil {
		return nil, nil, fmt.Errorf("load %s: %w", releaseRef, err)
	}
	cur, err := load("")
	if err != nil {
		return nil, nil, fmt.Errorf("load working tree: %w", err)
	}
	// An empty side would compare clean; print sizes so a log shows what
	// was actually compared.
	say(w, "versionskew %s: %s has %s; head has %s\n", name, releaseRef, c.size(old), c.size(cur))
	headFindings := compare(old, cur)
	if len(headFindings) == 0 {
		return nil, nil, nil
	}
	baseKeys := map[string]bool{}
	if baseRef != releaseRef {
		atBase, err := load(baseRef)
		if err != nil {
			return nil, nil, fmt.Errorf("load %s: %w", baseRef, err)
		}
		for _, f := range compare(old, atBase) {
			baseKeys[f.Key] = true
		}
	}
	for _, f := range headFindings {
		if baseKeys[f.Key] {
			preexisting = append(preexisting, f)
		} else {
			introduced = append(introduced, f)
		}
	}
	return introduced, preexisting, nil
}

func report(w io.Writer, introduced, preexisting []finding, allow map[string]string, releaseRef string) int {
	used := map[string]bool{}
	failed := 0
	for _, f := range sortFindings(introduced) {
		if reason, ok := allow[f.Key]; ok {
			used[f.Key] = true
			say(w, "::notice::versionskew: allowed %s (%s)\n", f.Key, reason)
			continue
		}
		failed++
		say(w, "::error::versionskew: %s\n  %s\n", f.Key, f.Message)
	}
	for _, f := range sortFindings(preexisting) {
		used[f.Key] = used[f.Key] || allow[f.Key] != ""
		say(w, "::warning::versionskew: pre-existing on the base, not introduced here: %s\n  %s\n", f.Key, f.Message)
	}
	var stale []string
	for key := range allow {
		if !used[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		say(w, "::notice::versionskew: allowlist entry no longer matches a finding against %s; delete it: %s\n", releaseRef, key)
	}
	if failed > 0 {
		say(w, "versionskew: %d incompatible change(s) against %s. See docs/release-compatibility.md for the deprecate-then-remove sequence.\n", failed, releaseRef)
		return 1
	}
	say(w, "versionskew: OK against %s\n", releaseRef)
	return 0
}

func sortFindings(fs []finding) []finding {
	out := append([]finding(nil), fs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// readAllowlist parses "<key>  # <reason>" lines. A reason is mandatory: an
// exception nobody can explain is not an exception.
func readAllowlist(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	allow := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, reason, _ := strings.Cut(text, "#")
		key, reason = strings.TrimSpace(key), strings.TrimSpace(reason)
		if key == "" || reason == "" {
			return nil, fmt.Errorf("%s:%d: want \"<finding key>  # <reason>\"", path, line)
		}
		allow[key] = reason
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return allow, nil
}

func resolveBase(repo, base string) (string, error) {
	if base == "" {
		out, err := git(repo, "merge-base", "HEAD", "origin/main")
		if err != nil {
			return "", fmt.Errorf("no -base given and no merge-base with origin/main: %w", err)
		}
		base = out
	}
	sha, err := git(repo, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve base %q: %w", base, err)
	}
	return sha, nil
}

// resolveRelease returns the previous release. "auto" is the newest v* tag
// reachable from the base. A checkout without tags falls back to the base
// itself and warns: every change against main is still checked, but a
// deprecation that has not shipped yet then counts as shipped.
func resolveRelease(w io.Writer, repo, release, baseRef string) (string, error) {
	if release != "auto" {
		if _, err := git(repo, "rev-parse", "--verify", release+"^{commit}"); err != nil {
			return "", fmt.Errorf("resolve release %q: %w", release, err)
		}
		return release, nil
	}
	if tag, err := git(repo, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", baseRef); err == nil && tag != "" {
		return tag, nil
	}
	say(w, "::warning::versionskew: no v* release tag is an ancestor of %s (fetch tags?); comparing against the base instead\n", short(baseRef))
	return baseRef, nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// say writes one line of the report; a failed write to the CI log has
// nowhere better to go.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
