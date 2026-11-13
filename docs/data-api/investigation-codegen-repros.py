#!/usr/bin/env python3
# HISTORICAL negative assertions; use the original investigation checkout.
# Current regression coverage is linked in investigation-findings.md.
"""Manual evidence for DAPI016-DAPI024 in investigation-findings.md.

Requirements: Python 3.9+, Node 22+, repository Go toolchain/dependencies.
Use GREGALE_GO=/path/to/go when the Go compiler is not named go on PATH.
These assertions document existing defects, not acceptance criteria for fixes.
Only generated temporary files, fake credentials, and loopback servers are used.
No hosted API, database, or real project output is changed.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

GENERATOR = r'''package main
import (
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "github.com/onebox-faas/faas/pkg/dataapi"
 "github.com/onebox-faas/faas/pkg/dataapi/clientgen"
)
func makeContract(tables []dataapi.Table,enums map[string][]string,funcs []dataapi.Function)dataapi.Contract{
 s:=dataapi.Snapshot{Version:4,Schemas:[]string{"api"},Tables:tables,Functions:funcs,Enums:enums,Composites:[]dataapi.Composite{}}
 if s.Functions==nil{s.Functions=[]dataapi.Function{}}
 raw,err:=json.Marshal(s);if err!=nil{panic(err)}
 env,_:=json.Marshal(map[string]any{"version":1,"fingerprint":fmt.Sprintf("%x",sha256.Sum256(raw)),"snapshot":json.RawMessage(raw)})
 c,err:=dataapi.Parse(env);if err!=nil{panic(err)};return c
}
func col(name string,nullable,optional bool)dataapi.Column{return dataapi.Column{Name:name,Type:"string",Nullable:nullable,Optional:optional,Insertable:true,Updatable:true}}
func table(name string,cols ...dataapi.Column)dataapi.Table{return dataapi.Table{Schema:"api",Name:name,Columns:cols,Relationships:json.RawMessage(`[]`)}}
func main(){
 out:=os.Args[1]
 base:=makeContract([]dataapi.Table{table("notes",col("id",false,false),col("title",true,true)),table("base_url",col("id",false,false)),table("odd?name",col("id",false,false))},map[string][]string{},[]dataapi.Function{{Schema:"api",Name:"set_note",Args:[]dataapi.Argument{{Name:"title",Type:"string",Optional:true}},Returns:"string | null"}})
 variants:=[]struct{name string;c dataapi.Contract}{
 {"base",base},
 {"collision",makeContract([]dataapi.Table{table("notes",col("id",false,false))},map[string][]string{"api.notes_row":{"draft","final"}},nil)},
 {"special",makeContract([]dataapi.Table{table("names",col("a,b",false,false),col("-",false,false))},map[string][]string{},nil)},
 {"backtick",makeContract([]dataapi.Table{table("names",col("tick`mark",false,false))},map[string][]string{},nil)},
 }
 for _,v:=range variants{
  dir:=filepath.Join(out,v.name);if err:=os.MkdirAll(dir,0700);err!=nil{panic(err)}
  goCode,goErr:=clientgen.RenderGo(v.c,"sdk")
  pyCode,pyErr:=clientgen.RenderPython(v.c)
  if goErr==nil{if err:=os.WriteFile(filepath.Join(dir,"client.go"),goCode,0600);err!=nil{panic(err)}}
  if pyErr==nil{if err:=os.WriteFile(filepath.Join(dir,"client.py"),pyCode,0600);err!=nil{panic(err)}}
  result:=map[string]any{"case":v.name,"go_render_ok":goErr==nil,"python_render_ok":pyErr==nil}
  if goErr!=nil{result["go_error"]=goErr.Error()}
  raw,_:=json.Marshal(result);fmt.Println(string(raw))
 }
}
'''

GO_PROBE = r'''package main
import (
 "context"
 "encoding/json"
 "fmt"
 "net/http"
 "net/http/httptest"
 "net/url"
 sdk "example.com/data-api-review/base"
 special "example.com/data-api-review/special"
)
func emit(id string,detail any){raw,_:=json.Marshal(map[string]any{"id":id,"evidence":detail});fmt.Println(string(raw))}
func main(){
 var queries []url.Values;var paths []string
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){queries=append(queries,r.URL.Query());paths=append(paths,r.URL.Path);w.Header().Set("Content-Type","application/json");fmt.Fprint(w,`[{"id":"1","title":null}]`)}));defer server.Close()
 client:=&sdk.Client{BaseURL:server.URL,AccessToken:func(context.Context)(string,error){return "fake-user-token",nil}}
 order:=[]sdk.CursorOrder{{Column:"id",Unique:true}}
 first,err:=client.Notes().Or("title.eq.private,title.eq.team").Page(order,2,nil,nil);if err!=nil{panic(err)}
 if _,err=first.Get(context.Background());err!=nil{panic(err)}
 next,err:=client.Notes().Or("title.eq.private,title.eq.team").Page(order,2,sdk.Cursor{"1"},nil);if err!=nil{panic(err)}
 if _,err=next.Get(context.Background());err!=nil{panic(err)}
 if queries[0].Get("or")!=""{panic("first-page OR unexpectedly retained")}
 emit("DAPI016",map[string]any{"first_page_or":queries[0].Get("or"),"next_page_or":queries[1]["or"]})
 raw,err:=json.Marshal(sdk.NotesInsert{Id:"1",Title:nil});if err!=nil{panic(err)}
 emit("DAPI019",string(raw))
 raw,err=json.Marshal(special.NamesInsert{AB:"comma",X:"dash"});if err!=nil{panic(err)}
 emit("DAPI020",string(raw))
 if _,err=client.OddName().Get(context.Background());err!=nil{panic(err)}
 emit("DAPI021",map[string]any{"requested_table":"odd?name","actual_path":paths[len(paths)-1],"actual_query":queries[len(queries)-1]})
 var leaked string;var sourceWrites,sinkWrites int
 sink:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){leaked=r.Header.Get("Authorization");if r.Method=="POST"{sinkWrites++};w.Header().Set("Content-Type","application/json");fmt.Fprint(w,`[{"id":"1","title":null}]`)}));defer sink.Close()
 redirect:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){if r.Method=="POST"{sourceWrites++};w.Header().Set("Location",sink.URL+"/capture");w.WriteHeader(http.StatusTemporaryRedirect)}));defer redirect.Close()
 redirected:=&sdk.Client{BaseURL:redirect.URL,AccessToken:func(context.Context)(string,error){return "fake-user-token",nil}}
 if _,err=redirected.Notes().Insert(context.Background(),sdk.NotesInsert{Id:"1"});err!=nil{panic(err)}
 if leaked!="Bearer fake-user-token"||sourceWrites!=1||sinkWrites!=1{panic("redirect reproduction changed")}
 emit("DAPI017",map[string]any{"authorization_forwarded_to_other_origin":leaked!="","source_post":sourceWrites,"redirect_target_post":sinkWrites})
}
'''

PYTHON_PROBE = r'''import importlib.util,json,threading
from http.server import BaseHTTPRequestHandler,HTTPServer
from urllib.parse import urlsplit
spec=importlib.util.spec_from_file_location('review_data',str(generated / 'base/client.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
client=m.DataClient('http://127.0.0.1',access_token='fake-token')
order=[{'column':'id','unique':True}]
q=client.notes().or_('title.eq.private,title.eq.team').page(order,2)
assert not any(k=='or' for k,v in q._params)
q2=client.notes().or_('title.eq.private,title.eq.team').page(order,2,after=['1'])
assert all('title.eq' not in v for k,v in q2._params if k=='or')
print(json.dumps({'id':'DAPI016','first_page_params':q._params,'next_page_params':q2._params}))
assert isinstance(client.base_url,str) and not callable(client.base_url)
print(json.dumps({'id':'DAPI022','generated_method':callable(type(client).base_url),'instance_attribute_type':type(client.base_url).__name__}))
paths=[];sink_auth=[]
class Sink(BaseHTTPRequestHandler):
 def do_GET(self):
  paths.append(self.path);sink_auth.append(self.headers.get('Authorization'));self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(b'[{"id":"1","title":null}]')
 def log_message(self,*args):pass
sink=HTTPServer(('127.0.0.1',0),Sink)
class Redirect(BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(302);self.send_header('Location',f'http://localhost:{sink.server_port}/capture');self.end_headers()
 def log_message(self,*args):pass
redirect=HTTPServer(('127.0.0.1',0),Redirect)
servers=[sink,redirect]
for s in servers:threading.Thread(target=s.serve_forever,daemon=True).start()
try:
 client=m.DataClient(f'http://127.0.0.1:{redirect.server_port}',access_token='fake-token')
 client.notes().get()
 assert sink_auth[-1]=='Bearer fake-token'
 print(json.dumps({'id':'DAPI017','python_forwarded_auth_to_different_hostname':True}))
 client=m.DataClient(f'http://127.0.0.1:{sink.server_port}',access_token='fake-token')
 client.odd_name().get()
 assert urlsplit(paths[-1]).path=='/rest/v1/odd'
 print(json.dumps({'id':'DAPI021','python_actual_request':paths[-1]}))
finally:
 for s in servers:s.shutdown();s.server_close()
'''

NODE_PROBE = r'''import assert from 'node:assert/strict'
import {mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
import {pathToFileURL} from 'node:url'
const root=process.argv[2]
const {loadDevProject}=await import(pathToFileURL(join(root,'cmd/gregale/templates/data-api/dev-project.mjs')))
const {replayRequest}=await import(pathToFileURL(join(root,'cmd/gregale/templates/data-api/request-replay.mjs')))
const {requestStore}=await import(pathToFileURL(join(root,'cmd/gregale/templates/data-api/dev-requests.mjs')))
const tmp=await mkdtemp(join(tmpdir(),'dapi-project-review-'))
try {
 await mkdir(join(tmp,'migrations/sql'),{recursive:true})
 const sql=join(tmp,'migrations/sql/0001_notes.sql')
 await writeFile(sql,'CREATE TABLE notes(id integer);\n')
 const request=identity=>({name:identity,method:'GET',path:'/rest/v1/notes',identity,expect:{status:200,json:[]}})
 await writeFile(join(tmp,'requests.json'),JSON.stringify({version:2,scenarios:[{name:'required',requests:[request('alice'),request('bob')]}]}))
 await writeFile(join(tmp,'data-api.dev.json'),JSON.stringify({version:1,output:'migrations/sql/0001_notes.sql',identities:{alice:{subject:'alice'},bob:{subject:'bob'}},verify:{collection:'requests.json',scenarios:['required']}}))
 const project=await loadDevProject({directory:tmp,output:join(tmp,'database.types.ts')})
 assert.equal(project.output,sql)
 console.log(JSON.stringify({id:'DAPI023',accepted:true,outputIsMigration:true,level:'loader and write-path review; full development flow not run'}))
 const numeric=join(tmp,'numeric.json')
 await writeFile(numeric,'{"version":1,"requests":[{"name":"exact id","method":"GET","path":"/rest/v1/notes","identity":"alice","expect":{"status":200,"json":[{"id":9007199254740993}]},"capture":{"id":"/0/id"}}]}')
 const req=(await requestStore(numeric).read(true)).requests[0]
 const variables=new Map()
 const result=await replayRequest(req,{url:'https://data.example',identities:{alice:{token:'fake-token'}},variables,fetcher:async()=>new Response('[{"id":9007199254740992}]',{status:200,headers:{'content-type':'application/json'}})})
 assert.equal(result.passed,true);assert.equal(variables.get('id'),9007199254740992)
 console.log(JSON.stringify({id:'DAPI024',expectedLiteral:'9007199254740993',actualLiteral:'9007199254740992',passed:result.passed,captured:variables.get('id')}))
} finally {await rm(tmp,{recursive:true,force:true})}
'''


def command(args, *, cwd, source=None, succeeds=True):
    result = subprocess.run(args, cwd=cwd, input=source, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            timeout=300)
    if succeeds and result.returncode:
        raise RuntimeError("Diagnostic command failed: " + result.stderr)
    return result


def main():
    repo = Path(__file__).resolve().parents[2]
    go = os.environ.get("GREGALE_GO") or shutil.which("go")
    if not go:
        raise RuntimeError("Install the repository Go toolchain or set GREGALE_GO")
    version = command([go, "version"], cwd=repo)
    if not version.stdout.startswith("go version go"):
        raise RuntimeError("GREGALE_GO must identify the Go compiler")
    with tempfile.TemporaryDirectory(prefix="gregale-data-api-codegen-") as temporary:
        generated = Path(temporary)
        generator = generated / "generate.go"
        generator.write_text(GENERATOR)
        rendering = command([go, "run", str(generator), str(generated)], cwd=repo)
        metadata = {row["case"]: row for row in map(json.loads, rendering.stdout.splitlines())}
        assert metadata["base"]["go_render_ok"] and metadata["base"]["python_render_ok"]
        assert metadata["special"]["go_render_ok"]
        assert not metadata["backtick"]["go_render_ok"]
        print(json.dumps({"id": "DAPI020", "backtick_column_rejected_by_renderer": True}), flush=True)
        generator.unlink()
        (generated / "go.mod").write_text("module example.com/data-api-review\n\ngo 1.26.10\n")
        (generated / "main.go").write_text(GO_PROBE)
        observed = command([go, "run", "."], cwd=generated)
        events = [json.loads(line) for line in observed.stdout.splitlines()]
        by_id = {row["id"]: row["evidence"] for row in events}
        assert by_id["DAPI016"]["first_page_or"] == ""
        assert all("title.eq" not in item for item in by_id["DAPI016"]["next_page_or"])
        assert "title" not in json.loads(by_id["DAPI019"])
        assert json.loads(by_id["DAPI020"]) == {"a": "comma"}
        assert by_id["DAPI021"]["actual_path"] == "/rest/v1/odd"
        for line in observed.stdout.splitlines():
            print(line, flush=True)
        collision = command([go, "test", "./collision"], cwd=generated, succeeds=False)
        assert collision.returncode and "NotesRow redeclared" in collision.stdout + collision.stderr
        print(json.dumps({"id": "DAPI018", "rendered_successfully": True,
                          "compile_error": "NotesRow redeclared"}), flush=True)
        exec(PYTHON_PROBE, {"generated": generated})
        node = command(["node", "--input-type=module", "-", str(repo)], cwd=repo, source=NODE_PROBE)
        print(node.stdout, end="", flush=True)


if __name__ == "__main__":
    main()
