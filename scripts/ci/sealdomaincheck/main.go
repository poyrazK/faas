// Command sealdomaincheck keeps the holders of private age identities from
// growing (ADR 20261022111812). It checks three things against baseline.txt,
// which may only shrink:
//
//   - credential: every systemd LoadCredential= of a private identity (a
//     path under /etc/faas/secrets ending in .age or .age.previous) in a
//     unit file or an Ansible drop-in. A unit may load the identities of
//     the domains it reads (pkg/sealdomain); anything else is baseline.
//   - loader: every direct identity load in daemon code (secretbox.LoadHostKey,
//     LoadHostKeys, LoadFleetAndHostKeys, age.ParseX25519Identity,
//     age.ParseIdentities), counted per file. New code loads through
//     sealdomain.LoadReaderIdentities. gregalectl, pkg/secretbox and
//     pkg/sealdomain are the sanctioned loaders and are not counted.
//   - plain: every bytea column in schema.sql is registered to a seal
//     domain, is a probe or known plaintext secret, or is listed as not
//     sealed. A new sealed column must name its domain.
//
// A finding missing from the baseline fails, and so does a baseline entry
// that no longer occurs, so the file stays exact. Run with -write to
// regenerate it after removing a holder.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

const baselineHeader = `# sealdomaincheck baseline (ADR 20261022111812). This file only shrinks.
# A new line is a new holder of a private age identity, a new direct
# identity loader in daemon code, or a new bytea column that is not sealed;
# each needs review. Regenerate with: go run ./scripts/ci/sealdomaincheck -write
`

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String("baseline", "scripts/ci/sealdomaincheck/baseline.txt", "baseline file, relative to -root")
	write := flag.Bool("write", false, "rewrite the baseline from the current tree")
	flag.Parse()

	found, err := scan(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sealdomaincheck:", err)
		os.Exit(2)
	}
	path := filepath.Join(*root, *baselinePath)
	if *write {
		if err := os.WriteFile(path, []byte(render(found)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "sealdomaincheck:", err)
			os.Exit(2)
		}
		return
	}
	baseline, err := readBaseline(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sealdomaincheck:", err)
		os.Exit(2)
	}
	problems := compare(found, baseline)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "sealdomaincheck:", p)
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "sealdomaincheck: see ADR 20261022111812; new code loads identities through sealdomain.LoadReaderIdentities and seals to its reader's domain")
		os.Exit(1)
	}
}

// scan returns every finding as a baseline line.
func scan(root string) (map[string]bool, error) {
	out := map[string]bool{}
	creds, err := scanCredentials(root)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		out[c] = true
	}
	loaders, err := scanLoaders(root)
	if err != nil {
		return nil, err
	}
	for file, n := range loaders {
		out[fmt.Sprintf("loader %s %d", file, n)] = true
	}
	plain, err := scanPlainColumns(filepath.Join(root, "schema.sql"))
	if err != nil {
		return nil, err
	}
	for _, c := range plain {
		out["plain "+c] = true
	}
	return out, nil
}

var credentialRe = regexp.MustCompile(`LoadCredential=[A-Za-z0-9_.-]+:(/etc/faas/secrets/\S+?\.age(?:\.previous)?)(?:$|\s|")`)

// scanCredentials lists private identity credentials not explained by the
// unit's own domains. Unit files are keyed by name, so the generated trees
// that hold the same unit count once; other deploy files by path.
func scanCredentials(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".generated" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(p)
		if ext != ".service" && ext != ".yml" && ext != ".yaml" && ext != ".j2" && ext != ".conf" {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		key := "file:" + filepath.ToSlash(rel)
		unit := ""
		if ext == ".service" {
			unit = strings.TrimSuffix(filepath.Base(p), ".service")
			key = "unit:" + unit
		}
		readerDomain := sealDomainReaderTemplate(filepath.ToSlash(rel))
		for _, m := range credentialRe.FindAllStringSubmatch(string(body), -1) {
			if unit != "" && ownIdentity(strings.TrimPrefix(unit, "faas-"), m[1]) {
				continue
			}
			if readerDomain != "" && domainIdentity(readerDomain, m[1]) {
				continue
			}
			seen[fmt.Sprintf("credential %s %s", key, m[1])] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out, nil
}

// ownIdentity reports whether path is the identity of a domain daemon reads.
func ownIdentity(daemon, path string) bool {
	for _, d := range sealdomain.ReaderDomains(daemon) {
		if domainIdentity(d, path) {
			return true
		}
	}
	return false
}

func domainIdentity(d sealdomain.Domain, path string) bool {
	spec, ok := sealdomain.Lookup(d)
	return ok && (path == spec.IdentityFile() || path == spec.IdentityFile()+".previous")
}

var readerTemplateRe = regexp.MustCompile(`^deploy/ansible/roles/seal_domains/templates/\d+-faas-seal-domain-([a-z-]+)-reader\.conf\.j2$`)

// sealDomainReaderTemplate returns the domain whose reader drop-in rel is:
// the seal_domains role installs it only into that domain's reader units.
func sealDomainReaderTemplate(rel string) sealdomain.Domain {
	m := readerTemplateRe.FindStringSubmatch(rel)
	if m == nil {
		return ""
	}
	d, err := sealdomain.Parse(m[1])
	if err != nil {
		return ""
	}
	return d
}

var loaderCalls = map[string]map[string]bool{
	"secretbox": {"LoadHostKey": true, "LoadHostKeys": true, "LoadFleetAndHostKeys": true},
	"age":       {"ParseX25519Identity": true, "ParseIdentities": true},
}

// Sanctioned loaders: the operator CLIs and the packages that define the
// loaders.
var loaderExempt = []string{"cmd/gregalectl/", "cmd/gregale/", "cmd/hostage-gen/", "pkg/secretbox/", "pkg/sealdomain/"}

func scanLoaders(root string) (map[string]int, error) {
	out := map[string]int{}
	fset := token.NewFileSet()
	for _, dir := range []string{"cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			for _, prefix := range loaderExempt {
				if strings.HasPrefix(rel, prefix) {
					return nil
				}
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("parse %s: %w", rel, err)
			}
			n := countLoaderCalls(f)
			if n > 0 {
				out[rel] = n
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func countLoaderCalls(f *ast.File) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && loaderCalls[pkg.Name][sel.Sel.Name] {
			n++
		}
		return true
	})
	return n
}

var createTableRe = regexp.MustCompile(`^CREATE TABLE (?:public\.)?([a-z0-9_]+) \(`)
var byteaColumnRe = regexp.MustCompile(`^\s+([a-z0-9_]+) bytea\b`)

// scanPlainColumns lists bytea columns that no domain owns.
func scanPlainColumns(schemaPath string) ([]string, error) {
	f, err := os.Open(schemaPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	known := map[string]bool{}
	for _, c := range append(append([]sealdomain.Column{}, sealdomain.ProbeColumns...), sealdomain.PlaintextSecretColumns...) {
		known[c.String()] = true
	}
	var out []string
	table := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := createTableRe.FindStringSubmatch(line); m != nil {
			table = m[1]
			continue
		}
		if line == ");" {
			table = ""
			continue
		}
		m := byteaColumnRe.FindStringSubmatch(line)
		if table == "" || m == nil {
			continue
		}
		col := table + "." + m[1]
		if _, ok := sealdomain.ColumnDomain(table, m[1]); ok || known[col] {
			continue
		}
		out = append(out, col)
	}
	return out, sc.Err()
}

func readBaseline(path string) (map[string]bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s is missing; create it with -write", path)
	}
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, nil
}

// compare reports findings missing from the baseline and baseline lines
// that no longer occur. A loader count that drops is reported as stale so
// the baseline follows it down.
func compare(found, baseline map[string]bool) []string {
	var problems []string
	for line := range found {
		if !baseline[line] {
			problems = append(problems, "new: "+line+describe(line, baseline))
		}
	}
	for line := range baseline {
		if !found[line] {
			problems = append(problems, "stale baseline entry (remove it, or run -write): "+line)
		}
	}
	sort.Strings(problems)
	return problems
}

func describe(line string, baseline map[string]bool) string {
	fields := strings.Fields(line)
	switch fields[0] {
	case "credential":
		return " — a unit may load only the identities of the domains it reads"
	case "loader":
		for b := range baseline {
			bf := strings.Fields(b)
			if bf[0] == "loader" && bf[1] == fields[1] {
				was, _ := strconv.Atoi(bf[2])
				now, _ := strconv.Atoi(fields[2])
				if now < was {
					return " — fewer loaders than the baseline; lower it"
				}
			}
		}
		return " — load through sealdomain.LoadReaderIdentities"
	case "plain":
		return " — register a sealed column to its reader's domain in pkg/sealdomain, or add a column that is not sealed to the baseline"
	}
	return ""
}

func render(found map[string]bool) string {
	lines := make([]string, 0, len(found))
	for l := range found {
		lines = append(lines, l)
	}
	sort.Strings(lines)
	return baselineHeader + strings.Join(lines, "\n") + "\n"
}
