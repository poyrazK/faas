// Run from the platform repository: go test sdk/request-contracts/cli-source-preparation_test.go
// Compile the actual CLI selection code in a stdlib-only harness. This keeps
// the contract cheap without substituting another implementation of its rules.
package contracts_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISourcePreparationContract(t *testing.T) {
	rootOutput, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("resolve platform repository from test working directory: %v", err)
	}
	root := strings.TrimSpace(string(rootOutput))
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(root, name))
		if os.IsNotExist(err) { // Support the SDK-only sparse checkout used locally.
			cmd := exec.Command("git", "show", "HEAD:"+name)
			cmd.Dir = root
			data, err = cmd.Output()
		}
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	keep := map[string]bool{}
	for _, name := range strings.Fields(`defaultExcludeDirs defaultExcludeFiles buildOutputDirs gregaleignoreFile dockerignoreFile
		gregaleignorePattern packRules packSelection packEntry maxSymlinkHops SourceArchiveMaxEntries SourceArchiveMaxExpandedBytes
		parseGregaleignore loadGregaleignore matchGregaleignoreOne globMatchSegments parseDockerignore loadPackRules
		collectPackEntries resolvePackSymlink selection`) {
		keep[name] = true
	}
	var code bytes.Buffer
	code.WriteString(`package main
import ("bufio"; "bytes"; "fmt"; "os"; "path"; "path/filepath"; "strings")
type deploySourceFilesReport struct{}
func (*deploySourceFilesReport) observe(string, bool, int64, packSelection, bool) error { return nil }
`)
	fset := token.NewFileSet()
	for _, name := range []string{"cmd/gregale/pack.go", "cmd/gregale/pack_selection.go", "cmd/gregale/git_archive_normalize.go", "pkg/api/limits.go"} {
		file, err := parser.ParseFile(fset, name, read(name), 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if keep[d.Name.Name] {
					if err := printer.Fprint(&code, fset, d); err != nil {
						t.Fatal(err)
					}
					code.WriteByte('\n')
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					selected := false
					switch s := spec.(type) {
					case *ast.TypeSpec:
						selected = keep[s.Name.Name]
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if keep[n.Name] {
								selected = true
							}
						}
					}
					if selected {
						if err := printer.Fprint(&code, fset, &ast.GenDecl{Tok: d.Tok, Specs: []ast.Spec{spec}}); err != nil {
							t.Fatal(err)
						}
						code.WriteByte('\n')
					}
				}
			}
		}
	}
	tmp := t.TempDir()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("selection.go", code.Bytes())
	scanner, err := parser.ParseFile(fset, "scan.go", read("pkg/secretscan/scan.go"), 0)
	if err != nil {
		t.Fatal(err)
	}
	scanner.Name.Name = "main"
	var scan bytes.Buffer
	if err := printer.Fprint(&scan, fset, scanner); err != nil {
		t.Fatal(err)
	}
	write("scan.go", scan.Bytes())
	write("contract.json", read("sdk/request-contracts/source-preparation.json"))
	write("go.mod", []byte("module sourcecontract\n\ngo 1.26.0\n"))
	write("contract_test.go", []byte(cliSourceHarness))
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("resolve selected Go toolchain: %v", err)
	}
	cmd := exec.Command(goPath, "test", "-count=1", "-v", ".")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI source contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}

const cliSourceHarness = `package main
import ("encoding/json"; "os"; "path/filepath"; "reflect"; "sort"; "strings"; "testing")
func TestSharedCases(t *testing.T) {
  var fixture struct {
    Cases []struct { Name string; Files map[string]string; Links map[string]string; SourceRoot string ` + "`json:\"source_root\"`" + `; Exclude []string; Included []string }
    Secrets []struct {Name string; LineParts []string ` + "`json:\"line_parts\"`" + `; Blocked bool}
  }
  data,err:=os.ReadFile("contract.json"); if err!=nil {t.Fatal(err)}
  if err=json.Unmarshal(data,&fixture); err!=nil {t.Fatal(err)}
  if SourceArchiveMaxEntries!=10000 || SourceArchiveMaxExpandedBytes!=2<<30 {t.Fatal("update SDK safety caps to match pkg/api/limits.go")}
  for _,item:=range fixture.Cases {t.Run(item.Name,func(t *testing.T) {
    root:=t.TempDir(); root,err=filepath.EvalSymlinks(root); if err!=nil {t.Fatal(err)}
    for name,body:=range item.Files {p:=filepath.Join(root,filepath.FromSlash(name)); if err:=os.MkdirAll(filepath.Dir(p),0700);err!=nil {t.Fatal(err)}; if err:=os.WriteFile(p,[]byte(body),0600);err!=nil {t.Fatal(err)}}
    for name,target:=range item.Links {if err:=os.Symlink(target,filepath.Join(root,name));err!=nil {t.Fatal(err)}}
    docker:=false
    for name:=range item.Files {if strings.EqualFold(name,filepath.ToSlash(filepath.Join(item.SourceRoot,"Dockerfile"))) {docker=true}}
    extra:=map[string]bool{}; for _,name:=range item.Exclude {extra[name]=true}
    var entries []packEntry
    if err:=collectPackEntries(root,root,"",loadPackRules(root,docker),extra,&entries,0);err!=nil {t.Fatal(err)}
    var actual []string; for _,e:=range entries {if !e.info.IsDir() {actual=append(actual,e.rel)}}
    sort.Strings(actual);sort.Strings(item.Included)
    if !reflect.DeepEqual(actual,item.Included) {t.Fatalf("actual %v, want %v",actual,item.Included)}
  })}
  for _,item:=range fixture.Secrets {t.Run(item.Name,func(t *testing.T) {if blocked:=len(ScanEnvContent(".env",[]byte(strings.Join(item.LineParts, "")+"\n")))>0;blocked!=item.Blocked {t.Fatalf("CLI strict scan=%v, want %v",blocked,item.Blocked)}})}
}
`
