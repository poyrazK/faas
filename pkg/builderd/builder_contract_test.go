package builderd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// guestInitBuildContractSourceSHA256 pins the guest-init build-mode code that
// guestInitBuildContractVersion describes.
const guestInitBuildContractSourceSHA256 = "dc541ee37bb2601bde4a398736a87ff74455710ebd0c73d0f5406663aebb63ef"

// guestInitBuildRoots are the guest-init entry points a builder VM runs after
// the shared boot prelude: the BuildKit cgroup mount and the build itself.
var guestInitBuildRoots = []string{"mountCgroup2", "runBuild"}

// TestGuestInitBuildContractIsPinned keeps the build cache honest. The cache
// reuses builds across guest-init releases, so any change to the code a
// builder VM runs must be judged: if it can change what a build produces,
// bump guestInitBuildContractVersion so every cached build is redone.
func TestGuestInitBuildContractIsPinned(t *testing.T) {
	got, decls := guestInitBuildContractDigest(t, filepath.Join("..", "..", "guest", "init"))
	if got == guestInitBuildContractSourceSHA256 {
		return
	}
	t.Fatalf("guest-init build-mode code changed (%d declarations reachable from %v).\n"+
		"If this change can alter what a build produces, bump guestInitBuildContractVersion "+
		"in pkg/builderd/builder_contract.go so cached builds are not reused.\n"+
		"Then set guestInitBuildContractSourceSHA256 = %q.",
		len(decls), guestInitBuildRoots, got)
}

// guestInitBuildContractDigest hashes every package-level declaration of
// guest/init (as built for linux) reachable from guestInitBuildRoots. It
// resolves names, not types, so it over-approximates: a shadowing local or a
// same-named field pulls a declaration in. Comments and formatting are
// ignored.
func guestInitBuildContractDigest(t *testing.T, dir string) (string, []string) {
	t.Helper()
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH = "linux", "amd64"
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	decls := map[string][]ast.Node{}
	methods := map[string][]string{}
	for _, path := range paths {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if ok, err := ctx.MatchFile(dir, name); err != nil || !ok {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			indexDecl(decl, decls, methods)
		}
	}
	if len(decls) == 0 {
		t.Fatalf("no guest-init declarations found under %s", dir)
	}

	seen := map[string]bool{}
	queue := append([]string(nil), guestInitBuildRoots...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		nodes, ok := decls[name]
		if !ok {
			continue
		}
		seen[name] = true
		queue = append(queue, methods[name]...)
		for _, node := range nodes {
			ast.Inspect(node, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name != "_" && !seen[id.Name] {
					if _, ok := decls[id.Name]; ok {
						queue = append(queue, id.Name)
					}
				}
				return true
			})
		}
	}
	for _, root := range guestInitBuildRoots {
		if !seen[root] {
			t.Fatalf("guest-init build root %s no longer exists; update guestInitBuildRoots", root)
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		for _, node := range decls[name] {
			if fn, ok := node.(*ast.FuncDecl); ok {
				undocumented := *fn
				undocumented.Doc = nil
				node = &undocumented
			}
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, node); err != nil {
				t.Fatal(err)
			}
			h.Write([]byte(name + "\n"))
			h.Write(buf.Bytes())
			h.Write([]byte("\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil)), names
}

func indexDecl(decl ast.Decl, decls map[string][]ast.Node, methods map[string][]string) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		name := d.Name.Name
		if d.Recv != nil && len(d.Recv.List) > 0 {
			typ := d.Recv.List[0].Type
			if star, ok := typ.(*ast.StarExpr); ok {
				typ = star.X
			}
			if generic, ok := typ.(*ast.IndexExpr); ok {
				typ = generic.X
			}
			if id, ok := typ.(*ast.Ident); ok {
				name = id.Name + "." + name
				methods[id.Name] = append(methods[id.Name], name)
			}
		}
		decls[name] = append(decls[name], d)
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				decls[s.Name.Name] = append(decls[s.Name.Name], s)
			case *ast.ValueSpec:
				for _, id := range s.Names {
					decls[id.Name] = append(decls[id.Name], s)
				}
			}
		}
	}
}

func TestGuestInitBuildContractDigestTracksBuildCodeOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package main\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("build_linux.go", "func runBuild() { helper(); _ = limit }\nfunc mountCgroup2() {}\n// limit bounds builds.\nconst limit = 1\nfunc helper() {}\n")
	write("app_linux.go", "func runApp() {}\n")
	write("other_darwin.go", "func runBuild() {}\n")
	base, names := guestInitBuildContractDigest(t, dir)
	if strings.Join(names, ",") != "helper,limit,mountCgroup2,runBuild" {
		t.Fatalf("closure = %v", names)
	}

	write("app_linux.go", "func runApp() { println() }\n")
	write("build_linux.go", "func runBuild() { helper(); _ = limit }\nfunc mountCgroup2() {}\n// limit caps builds.\nconst limit = 1\n\nfunc helper() {}\n")
	if got, _ := guestInitBuildContractDigest(t, dir); got != base {
		t.Fatal("app-mode code, comments or formatting changed the build contract digest")
	}

	write("build_linux.go", "func runBuild() { helper(); _ = limit }\nfunc mountCgroup2() {}\nconst limit = 2\nfunc helper() {}\n")
	if got, _ := guestInitBuildContractDigest(t, dir); got == base {
		t.Fatal("a build-mode constant changed without changing the digest")
	}
}
