package clientgen

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

// Exercise the emitted clients rather than mirroring their rendering logic.
func TestInvestigationClientRegressions(t *testing.T) {
	c := sampleContract(t)
	c.Parsed.Enums["api.notes_row"] = []string{"collision"}
	for _, name := range []string{"a,b", "-", "`"} {
		c.Parsed.Tables[0].Columns = append(c.Parsed.Tables[0].Columns, dataapi.Column{Name: name, Type: "string", Nullable: true, Optional: true, Insertable: true, Updatable: true})
	}
	for _, name := range []string{"base_url", "access_token", "server_key", "subject", "anonymous", "timeout"} {
		c.Parsed.Tables = append(c.Parsed.Tables, dataapi.Table{Schema: "api", Name: name})
	}
	var leaked atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); _, _ = w.Write([]byte(`[]`)) }))
	defer trap.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/v1/redirect" {
			http.Redirect(w, r, trap.URL+"/trap", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/rest/v1/odd?name" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.Contains(r.URL.RawQuery, "limit=") && len(r.URL.Query()["or"]) == 0 {
			http.Error(w, "lost caller filter", 400)
			return
		}
		if r.URL.Path == "/rest/v1/notes" {
			_, _ = w.Write([]byte("[{\"a,b\":\"comma\",\"-\":\"dash\",\"`\":\"tick\"}]"))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	for _, lang := range []string{"go", "python"} {
		t.Run(lang, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, source []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), source, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var source []byte
			var err error
			var command *exec.Cmd
			if lang == "go" {
				source, err = RenderGo(c, "main")
				if err != nil {
					t.Fatal(err)
				}
				write("client.go", source)
				write("go.mod", []byte("module regression\n\ngo 1.21\n"))
				write("main.go", []byte(goHardeningSmoke))
				command = exec.Command("go", "run", ".", server.URL)
				command.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOWORK=off")
			} else {
				source, err = RenderPython(c)
				if err != nil {
					t.Fatal(err)
				}
				write("client.py", source)
				write("smoke.py", []byte(pythonHardeningSmoke))
				command = exec.Command("python3", "smoke.py", server.URL)
			}
			command.Dir = dir
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated %s failed: %v\n%s", lang, err, output)
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatalf("redirect dispatched %d requests", leaked.Load())
	}
}

const goHardeningSmoke = `package main
import("context";"encoding/json";"os")
func must(err error){if err!=nil{panic(err)}}
func main(){
 ctx:=context.Background();c:=&Client{BaseURL:os.Args[1],ServerKey:"private"}
 for _,method:=range []string{"GET","POST"}{var err error;if method=="GET"{_,err=c.Relation("api","redirect").Get(ctx)}else{_,err=c.Relation("api","redirect").Insert(ctx,map[string]any{"x":1})};if err==nil{panic("followed redirect")}}
 _,err:=c.Relation("api","odd?name").Get(ctx);must(err)
 order:=[]CursorOrder{{Column:"id",Unique:true}}
 for _,cursor:=range []Cursor{nil,{1}} {page,err:=c.Notes2().Or("body.eq.mine,body.eq.shared").Offset(99).Page(order,2,cursor,nil);must(err);if len(page.params["or"])!=1+btoi(cursor!=nil)||page.params.Get("offset")!=""{panic(page.params)};_,err=page.Get(ctx);must(err)}
 rows,err:=c.Notes2().Get(ctx);must(err);if rows[0].AB==nil||*rows[0].AB!="comma"||*rows[0].X!="dash"||*rows[0].X2!="tick"{panic("column names changed")}
 text:="value";insert:=Notes2Insert{Body:"body",AB:&text,X:&text,X2:&text,SetNull:[]string{"class"}}
 encoded,err:=json.Marshal(insert);must(err);var fields map[string]any;must(json.Unmarshal(encoded,&fields));if fields["a,b"]!="value"||fields["-"]!="value"||fields["` + "`" + `"]!="value"||fields["class"]!=nil{panic(string(encoded))};if _,ok:=fields["status"];ok{panic("omitted default was sent")}
 encoded,err=json.Marshal(CreateNoteArgs{SetNull:[]string{"body","priority"}});must(err);if string(encoded)!="{\"body\":null,\"priority\":null}"{panic(string(encoded))}
 _,err=json.Marshal(Notes2Insert{Class:&text,SetNull:[]string{"class"}});if err==nil{panic("conflicting null accepted")}
 for _,base:=range []string{"http://remote.example","https://private@data.example","https://data.example/path","https://data.example?x=1"}{bad:=&Client{BaseURL:base,AccessToken:func(context.Context)(string,error){panic("credential read before URL validation")}};_,err=bad.Relation("api","notes").Get(ctx);if err==nil{panic(base)}}
}
func btoi(b bool)int{if b{return 1};return 0}
`

const pythonHardeningSmoke = `import sys
from client import DataClient,CursorOrder
c=DataClient(sys.argv[1],server_key="private")
for method in ("get","insert"):
 try:
  table=c.table("redirect")
  table.get() if method=="get" else table.insert({"x":1})
  raise AssertionError("followed redirect")
 except ValueError as e:
  assert "redirect" in str(e)
c.table("odd?name").get()
for cursor in (None,[1]):
 page=c.notes().or_("body.eq.mine,body.eq.shared").offset(99).page([{"column":"id","unique":True}],2,after=cursor)
 assert len([p for p in page._params if p[0]=="or"])==(1 if cursor is None else 2)
 assert not any(p[0]=="offset" for p in page._params)
 page.get()
for name in ("base_url","access_token","server_key","subject","anonymous","timeout"):
 getattr(c,name+"2")().get()
for base in ("http://remote.example","https://private@data.example","https://data.example/path","https://data.example?x=1"):
 try:
  DataClient(base,access_token=lambda: (_ for _ in ()).throw(AssertionError("credential read"))).table("notes").get()
  raise AssertionError(base)
 except ValueError:
  pass
`
