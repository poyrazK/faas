// Run with cli-source-preparation_test.go from the platform repository.
// Extract the real dependency-free CLI marker and workspace matching helpers.
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

func TestCLIProjectInspectionContract(t *testing.T) {
	rootOutput, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("resolve platform repository from test working directory: %v", err)
	}
	root := strings.TrimSpace(string(rootOutput))
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(root, name))
		if os.IsNotExist(err) {
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
	for _, name := range strings.Fields(`Framework FrameworkNode FrameworkPython FrameworkGo FrameworkDocker FrameworkUnknown GoBuildToolchain appMarker appMarkers DetectFromFS expandWorkspacePatterns compileWorkspacePattern`) {
		keep[name] = true
	}
	var code bytes.Buffer
	code.WriteString("package main\nimport (\"fmt\";\"io/fs\";\"strings\";\"sort\";\"regexp\")\n")
	fset := token.NewFileSet()
	for _, name := range []string{"pkg/markers/framework.go", "pkg/markers/markers.go", "pkg/markers/detect.go", "pkg/reposcan/workspaces.go", "pkg/api/build.go"} {
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
				if d.Tok == token.IMPORT {
					continue
				}
				// Keep entire constant groups to preserve iota and associated declarations.
				selected := false
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						selected = selected || keep[s.Name.Name]
					case *ast.ValueSpec:
						for _, n := range s.Names {
							selected = selected || keep[n.Name]
						}
					}
				}
				if selected {
					if err := printer.Fprint(&code, fset, d); err != nil {
						t.Fatal(err)
					}
					code.WriteByte('\n')
				}
			}
		}
	}
	tmp := t.TempDir()
	for name, data := range map[string][]byte{"discovery.go": code.Bytes(), "go.mod": []byte("module projectcontract\n\ngo 1.26.0\n"), "contract.json": read("sdk/request-contracts/project-inspection.json"), "contract_test.go": []byte(cliProjectHarness)} {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("resolve selected Go toolchain: %v", err)
	}
	cmd := exec.Command(goPath, "test", "-count=1", "-v", ".")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI discovery contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}

const cliProjectHarness = `package main
import("encoding/json";"os";"path/filepath";"reflect";"testing")
func TestSharedProjectCases(t *testing.T){
 var fixture struct{GoBuildToolchain string; Cases []struct{Name string;Files map[string]string;CLI *struct{Framework string;Patterns []string;Members []string}}}
 data,err:=os.ReadFile("contract.json");if err!=nil{t.Fatal(err)};if err=json.Unmarshal(data,&fixture);err!=nil{t.Fatal(err)}
 if fixture.GoBuildToolchain!=GoBuildToolchain{t.Fatal("update SDK virtual go.mod toolchain")};for _,c:=range fixture.Cases{if c.CLI==nil{continue};t.Run(c.Name,func(t *testing.T){
  root:=t.TempDir();for name,body:=range c.Files{p:=filepath.Join(root,filepath.FromSlash(name));if err:=os.MkdirAll(filepath.Dir(p),0700);err!=nil{t.Fatal(err)};if err:=os.WriteFile(p,[]byte(body),0600);err!=nil{t.Fatal(err)}}
  if c.CLI.Framework!=""{value,err:=DetectFromFS(os.DirFS(root));if err!=nil||string(value)!=c.CLI.Framework{t.Fatalf("framework %s %v, want %s",value,err,c.CLI.Framework)}}
  if c.CLI.Patterns!=nil{value,err:=expandWorkspacePatterns(os.DirFS(root),c.CLI.Patterns);if err!=nil||!reflect.DeepEqual(value,c.CLI.Members){t.Fatalf("members %v %v, want %v",value,err,c.CLI.Members)}}
 })}
}
`
