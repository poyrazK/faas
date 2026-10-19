// Command teststorecheck fails a change that adds a test type embedding a state
// store, or a test interface embedding state.Store (CLAUDE.md, ADR-982). The
// compiler generates a wrapper for every promoted method of a type that embeds a
// store (~3,000 for *state.MemStore or *state.PgStore, ~80 MB of compiler heap)
// and for every method of every interface a package declares and converts to
// (~10 MB for a Store-sized one), so a few of them regrow a test compile by
// gigabytes. Only additions fail: each changed test file is compared with its
// version at -base, so the fakes that keep an embedding need no list. A field
// marked "teststorecheck:allow <reason>" is skipped.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

const (
	statePath   = "github.com/onebox-faas/faas/pkg/state"
	allowMarker = "teststorecheck:allow"
)

type finding struct {
	kind  string // "embed": a type embeds a store; "iface": an interface embeds state.Store
	owner string // the declared type, or the function holding a literal type
	store string // Store, MemStore or PgStore
	pos   token.Position
}

func (f finding) key() string { return f.kind + "|" + f.owner + "|" + f.store }

func main() {
	base := flag.String("base", "", "git ref to compare with (default: merge base with origin/main)")
	flag.Parse()
	if *base == "" {
		out, err := exec.Command("git", "merge-base", "HEAD", "origin/main").Output()
		if err != nil {
			fmt.Fprintln(os.Stderr, "teststorecheck: merge base with origin/main:", err)
			os.Exit(2)
		}
		*base = strings.TrimSpace(string(out))
	}
	files, err := changedFiles(*base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "teststorecheck:", err)
		os.Exit(2)
	}
	var added []finding
	for _, f := range files {
		head, err := os.ReadFile(f.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "teststorecheck:", err)
			os.Exit(2)
		}
		headFindings, err := findings(f.path, head)
		if err != nil {
			fmt.Fprintln(os.Stderr, "teststorecheck:", err)
			os.Exit(2)
		}
		var baseFindings []finding
		if f.basePath != "" {
			if src, ok := gitShow(*base, f.basePath); ok {
				// A base version that no longer parses has nothing to compare with.
				baseFindings, _ = findings(f.basePath, src)
			}
		}
		added = append(added, newFindings(baseFindings, headFindings)...)
	}
	if len(added) == 0 {
		return
	}
	for _, f := range added {
		fmt.Println(message(f))
	}
	fmt.Printf("\nteststorecheck: %d addition(s). A type that implements an optional store interface a statetest wrapper\nwould hide may keep its embedding; mark the field with // %s <reason>.\n", len(added), allowMarker)
	os.Exit(1)
}

func message(f finding) string {
	if f.kind == "iface" {
		return fmt.Sprintf("%s: %s embeds state.Store; the compiler generates a wrapper per method for each interface a package declares and converts to (~10 MB for a Store-sized one). List the methods it needs, or in pkg/state alias the shared one (type fooTestStore = contractTestStore).", f.pos, f.owner)
	}
	ctor := map[string]string{"Store": "NewStore", "MemStore": "NewMem", "PgStore": "NewPg"}[f.store]
	return fmt.Sprintf("%s: %s embeds the %s; the compiler generates a wrapper per promoted method (~80 MB of heap for a concrete store). Keep it in a named field (%s %s) and pass statetest.%s(fake) where a store is needed.", f.pos, f.owner, f.store, f.store, storeType(f.store), ctor)
}

func storeType(s string) string {
	if s == "Store" {
		return "state.Store"
	}
	return "*state." + s
}

type changed struct{ path, basePath string }

// changedFiles lists the test files that differ from base (renames keep their
// old path for the comparison) plus untracked ones.
func changedFiles(base string) ([]changed, error) {
	out, err := exec.Command("git", "diff", "--name-status", "-M", "--diff-filter=AMR", base).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", base, err)
	}
	var files []changed
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		switch {
		case len(fields) == 3 && strings.HasPrefix(fields[0], "R"):
			files = append(files, changed{path: fields[2], basePath: fields[1]})
		case len(fields) == 2 && fields[0] == "A":
			files = append(files, changed{path: fields[1]})
		case len(fields) == 2:
			files = append(files, changed{path: fields[1], basePath: fields[1]})
		}
	}
	untracked, err := exec.Command("git", "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	for _, p := range strings.Fields(string(untracked)) {
		files = append(files, changed{path: p})
	}
	var scoped []changed
	for _, f := range files {
		if inScope(f.path) {
			scoped = append(scoped, f)
		}
	}
	return scoped, nil
}

// inScope reports whether p is test code: a _test.go file, or a Go file of a
// test-helper package (a directory named *test). statetest embeds each store
// once on purpose.
func inScope(p string) bool {
	if !strings.HasSuffix(p, ".go") || strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "pkg/state/statetest/") {
		return false
	}
	return strings.HasSuffix(p, "_test.go") || strings.HasSuffix(path.Dir(p), "test")
}

func gitShow(ref, p string) ([]byte, bool) {
	out, err := exec.Command("git", "show", ref+":"+p).Output()
	return out, err == nil
}

// newFindings returns the head findings beyond those already in base, matched by
// kind, owner and store, so moving or editing an existing fake does not fail.
func newFindings(base, head []finding) []finding {
	seen := map[string]int{}
	for _, f := range base {
		seen[f.key()]++
	}
	var out []finding
	for _, f := range head {
		if seen[f.key()] > 0 {
			seen[f.key()]--
			continue
		}
		out = append(out, f)
	}
	return out
}

// findings parses one file and reports its store embeddings.
func findings(filename string, src []byte) ([]finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local := stateName(file)
	// Inside pkg/state the store types are unqualified.
	internal := file.Name.Name == "state" && local == ""
	if local == "" && !internal {
		return nil, nil
	}
	isStore := func(e ast.Expr) string {
		if star, ok := e.(*ast.StarExpr); ok {
			e = star.X
		}
		var name string
		switch e := e.(type) {
		case *ast.SelectorExpr:
			if id, ok := e.X.(*ast.Ident); ok && local != "" && id.Name == local {
				name = e.Sel.Name
			}
		case *ast.Ident:
			if internal {
				name = e.Name
			}
		}
		switch name {
		case "Store", "MemStore", "PgStore":
			return name
		}
		return ""
	}
	var out []finding
	names := map[ast.Node]string{}
	for _, decl := range file.Decls {
		owner := ""
		if fn, ok := decl.(*ast.FuncDecl); ok {
			owner = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				names[n.Type] = n.Name.Name
			case *ast.StructType:
				for _, field := range n.Fields.List {
					if s := isStore(field.Type); len(field.Names) == 0 && s != "" && !allowed(field) {
						out = append(out, finding{"embed", ownerOf(names, n, owner), s, fset.Position(field.Pos())})
					}
				}
			case *ast.InterfaceType:
				for _, field := range n.Methods.List {
					if len(field.Names) == 0 && isStore(field.Type) == "Store" && !allowed(field) {
						out = append(out, finding{"iface", ownerOf(names, n, owner), "Store", fset.Position(field.Pos())})
					}
				}
			}
			return true
		})
	}
	return out, nil
}

func ownerOf(names map[ast.Node]string, n ast.Node, fn string) string {
	if name, ok := names[n]; ok {
		return name
	}
	if fn != "" {
		return "a type literal in " + fn
	}
	return "a type literal"
}

func allowed(field *ast.Field) bool {
	for _, g := range []*ast.CommentGroup{field.Doc, field.Comment} {
		if g != nil && strings.Contains(g.Text(), allowMarker) {
			return true
		}
	}
	return false
}

// stateName is the name the file uses for the pkg/state import, or "".
func stateName(file *ast.File) string {
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == statePath {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return "state"
		}
	}
	return ""
}
