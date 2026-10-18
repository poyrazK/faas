package clientgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

const sampleSnapshot = `{"version":4,"schemas":["api"],
"tables":[
 {"schema":"api","name":"notes","view":false,"primary_key":{"columns":["id"],"filterable":true},"columns":[
  {"name":"id","type":"number","nullable":false,"optional":true,"insertable":true,"updatable":false},
  {"name":"body","type":"string","nullable":false,"optional":false,"insertable":true,"updatable":true},
  {"name":"status","type":"\"draft\" | \"published\"","nullable":false,"optional":true,"insertable":true,"updatable":true},
  {"name":"tags","type":"(string | null)[]","nullable":true,"optional":true,"insertable":true,"updatable":true},
  {"name":"meta","type":"Json","nullable":true,"optional":true,"insertable":true,"updatable":true},
  {"name":"class","type":"string","nullable":true,"optional":true,"insertable":true,"updatable":true},
  {"name":"address","type":"Database[\"api\"][\"CompositeTypes\"][\"address\"]","nullable":true,"optional":true,"insertable":true,"updatable":true}],
  "relationships":[]},
 {"schema":"api","name":"note_summary","view":true,"columns":[
  {"name":"id","type":"number","nullable":true,"optional":true,"insertable":false,"updatable":false}],"relationships":[]}],
"functions":[
 {"schema":"api","name":"create_note","args":[{"name":"body","type":"string","optional":false},{"name":"priority","type":"number","optional":true}],"returns":"Database[\"api\"][\"Tables\"][\"notes\"]['Row'][]"},
 {"schema":"api","name":"note_count","args":[],"returns":"number | null"},
 {"schema":"api","name":"touch","args":[],"returns":"undefined"}],
"enums":{"api.note_status":["draft","published"]},
"composites":[{"schema":"api","name":"address","fields":[{"name":"street","type":"string","nullable":true}]}]}`

func sampleContract(t *testing.T) dataapi.Contract {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(sampleSnapshot)); err != nil {
		t.Fatal(err)
	}
	envelope, _ := json.Marshal(map[string]any{"version": 1, "fingerprint": fmt.Sprintf("%x", sha256.Sum256(compact.Bytes())), "snapshot": json.RawMessage(compact.Bytes())})
	contract, err := dataapi.Parse(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestParseTypeCoversTheContractGrammar(t *testing.T) {
	for expr, want := range map[string]Type{
		"string":               {Kind: KindString},
		"number | null":        {Kind: KindNumber, Nullable: true},
		"boolean":              {Kind: KindBoolean},
		"Json":                 {Kind: KindJSON},
		"undefined":            {Kind: KindVoid},
		"never":                {Kind: KindNever},
		"unknown":              {Kind: KindUnknown},
		`"a" | "b | c" | null`: {Kind: KindEnum, Nullable: true, Literals: []string{"a", "b | c"}},
		`"quote\"d"`:           {Kind: KindEnum, Literals: []string{`quote"d`}},
		"(string | null)[]":    {Kind: KindArray, Elem: &Type{Kind: KindString, Nullable: true}},
		"(Json | null)[][]":    {Kind: KindArray, Elem: &Type{Kind: KindArray, Elem: &Type{Kind: KindJSON, Nullable: true}}},
		`Database["api"]["CompositeTypes"]["addr"]`:        {Kind: KindComposite, Schema: "api", Name: "addr"},
		`Database["api"]["Views"]["v"]['Row'][]`:           {Kind: KindArray, Elem: &Type{Kind: KindRow, Schema: "api", Name: "v", View: true}},
		`Database["api"]["Tables"]["t"]['Row']`:            {Kind: KindRow, Schema: "api", Name: "t"},
		`Database["api"]["Tables"]["t"]`:                   {Kind: KindUnknown},
		`"unterminated`:                                    {Kind: KindUnknown},
		`Database["api"]["CompositeTypes"]["addr"]['Row']`: {Kind: KindUnknown},
		`Database["api"]["Functions"]["f"]['Row']`:         {Kind: KindUnknown},
		`"a" | b`: {Kind: KindUnknown},
	} {
		if got := ParseType(expr); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseType(%q) = %+v, want %+v", expr, got, want)
		}
	}
}

func TestNamesAreIdentifiersAndUnique(t *testing.T) {
	if pascal("note_tags") != "NoteTags" || pascal("2fa codes") != "X2faCodes" || pascal("") != "X" || snake("Order Items") != "order_items" || pyIdentifier("class") != "class_" {
		t.Fatal("identifier mapping changed")
	}
	n := newNames("Client")
	if n.take("Client") != "Client2" || n.take("Notes") != "Notes" || n.take("Notes") != "Notes2" {
		t.Fatal("collisions must get numeric suffixes")
	}
	if !GoPackageValid("shopdata") || GoPackageValid("Shop") || GoPackageValid("func") || GoPackageValid("a-b") {
		t.Fatal("package validation changed")
	}
}

// fakeDataAPI records requests and answers like PostgREST behind the gateway.
type fakeDataAPI struct {
	mu       sync.Mutex
	requests []map[string]string
}

func (f *fakeDataAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, map[string]string{"method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery, "auth": r.Header.Get("Authorization"), "subject": r.Header.Get("X-Gregale-Subject"), "accept_profile": r.Header.Get("Accept-Profile"), "content_profile": r.Header.Get("Content-Profile"), "prefer": r.Header.Get("Prefer"), "origin": r.Header.Get("Origin"), "body": string(body)})
	f.mu.Unlock()
	w.Header().Set("X-Request-Id", "req-1")
	switch {
	case r.URL.Path == "/rest/v1/missing":
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"token_invalid","title":"token_invalid"}`))
	case r.URL.Path == "/rest/v1/rpc/note_count":
		_, _ = w.Write([]byte(`3`))
	case r.URL.Path == "/rest/v1/rpc/touch":
		w.WriteHeader(http.StatusNoContent)
	default:
		_, _ = w.Write([]byte(`[{"id":1,"body":"hi","status":"draft","tags":["a",null],"meta":{"k":1},"class":null,"address":{"street":null}}]`))
	}
}

func (f *fakeDataAPI) recorded() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.requests...)
}

// The same client calls in both languages must produce the same HTTP requests.
var wantRequests = []map[string]string{
	{"method": "GET", "path": "/rest/v1/notes", "query": "select=id%2Cbody&status=eq.draft&id=in.%28%221%22%2C%222%22%29&order=id.desc%2Cbody.asc&limit=5", "auth": "Bearer user-token", "accept_profile": "api"},
	{"method": "POST", "path": "/rest/v1/notes", "query": "", "auth": "Bearer user-token", "content_profile": "api", "prefer": "return=representation", "body": `[{"body":"hi","class":"x"}]`},
	{"method": "PATCH", "path": "/rest/v1/notes", "query": "id=eq.1", "auth": "Bearer user-token", "content_profile": "api", "prefer": "return=representation", "body": `{"body":"edited"}`},
	{"method": "DELETE", "path": "/rest/v1/notes", "query": "id=is.null", "auth": "Bearer user-token", "content_profile": "api", "prefer": "return=representation"},
	{"method": "POST", "path": "/rest/v1/rpc/create_note", "query": "", "auth": "Bearer gdsk_key", "subject": "user-7", "content_profile": "api", "prefer": "return=representation", "body": `{"body":"via rpc"}`},
	{"method": "POST", "path": "/rest/v1/rpc/note_count", "query": "", "auth": "Bearer gdsk_key", "subject": "user-7", "content_profile": "api", "prefer": "return=representation", "body": `{}`},
	{"method": "POST", "path": "/rest/v1/rpc/touch", "query": "", "auth": "Bearer gdsk_key", "subject": "user-7", "content_profile": "api", "prefer": "return=representation", "body": `{}`},
	{"method": "GET", "path": "/rest/v1/missing", "query": "", "auth": "Bearer user-token", "accept_profile": "api"},
}

func compareRequests(t *testing.T, got []map[string]string) {
	t.Helper()
	if len(got) != len(wantRequests) {
		t.Fatalf("requests = %d, want %d: %v", len(got), len(wantRequests), got)
	}
	for i, want := range wantRequests {
		for _, key := range []string{"method", "path", "query", "auth", "subject", "accept_profile", "content_profile", "prefer", "body", "origin"} {
			gotValue := strings.TrimSpace(got[i][key])
			if key == "body" {
				gotValue = compactJSON(gotValue)
			}
			if key == "query" {
				gotQuery, gotErr := url.ParseQuery(gotValue)
				wantQuery, wantErr := url.ParseQuery(want[key])
				if gotErr != nil || wantErr != nil || !reflect.DeepEqual(gotQuery, wantQuery) {
					t.Errorf("request %d query = %v, want %v", i, gotQuery, wantQuery)
				}
				continue
			}
			if gotValue != want[key] {
				t.Errorf("request %d %s = %q, want %q", i, key, gotValue, want[key])
			}
		}
	}
}

func compactJSON(value string) string {
	var out bytes.Buffer
	if json.Compact(&out, []byte(value)) != nil {
		return value
	}
	return out.String()
}

func TestRenderGoTypeChecksAndCallsTheAPI(t *testing.T) {
	source, err := RenderGo(sampleContract(t), "shopdata")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(source)), " ")
	for _, want := range []string{"// Code generated by gregale data-api types. DO NOT EDIT.", "type NoteStatus string", "NoteStatusPublished NoteStatus = \"published\"", "type AddressComposite struct", "Status NoteStatus `json:\"status\"`", "Class *string `json:\"class\"`", "Tags []*string `json:\"tags\"`", "Meta json.RawMessage `json:\"meta\"`", "Status *NoteStatus `json:\"status,omitempty\"`", "Id float64 `json:\"id\"`", "func (c *Client) Notes() Table[NotesRow, NotesInsert, NotesUpdate]", "func (c *Client) NoteSummary() Table[NoteSummaryRow, NoteSummaryInsert, NoteSummaryUpdate]", "func (c *Client) RPCCreateNote(ctx context.Context, args CreateNoteArgs) ([]NotesRow, error)", "func (c *Client) RPCNoteCount(ctx context.Context, args NoteCountArgs) (*float64, error)", "func (c *Client) RPCTouch(ctx context.Context, args TouchArgs) error"} {
		if !strings.Contains(text, want) {
			t.Errorf("generated Go lacks %q", want)
		}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if !ast.IsGenerated(file) {
		t.Fatal("generated marker missing")
	}
	if _, err := (&types.Config{Importer: importer.ForCompiler(fset, "source", nil)}).Check("shopdata", fset, []*ast.File{file}, nil); err != nil {
		t.Fatalf("generated Go does not type-check: %v", err)
	}
	if _, err := RenderGo(sampleContract(t), "Bad-Name"); err == nil {
		t.Fatal("invalid package accepted")
	}
	if testing.Short() {
		t.Skip("skipping go run of the generated client in -short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	fake := &fakeDataAPI{}
	server := httptest.NewServer(fake)
	defer server.Close()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "shopdata"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/smoke\n\ngo 1.21\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "shopdata", "client.go"), source, 0o644))
	must(os.WriteFile(filepath.Join(dir, "main.go"), []byte(goSmoke), 0o644))
	cmd := exec.Command(goBin, "run", ".", server.URL)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "rows=1 status=draft tags=2 inserted=1 count=3 error=401:token_invalid:req-1" {
		t.Fatalf("smoke output = %q", got)
	}
	compareRequests(t, fake.recorded())
}

const goSmoke = `package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"example.com/smoke/shopdata"
)

func main() {
	ctx := context.Background()
	user := &shopdata.Client{BaseURL: os.Args[1], AccessToken: func(context.Context) (string, error) { return "user-token", nil }}
	rows, err := user.Notes().Select("id,body").Eq("status", shopdata.NoteStatusDraft).In("id", 1, "2").Order("id", true).Order("body", false).Limit(5).Get(ctx)
	check(err)
	class := "x"
	inserted, err := user.Notes().Insert(ctx, shopdata.NotesInsert{Body: "hi", Class: &class})
	check(err)
	edited := "edited"
	_, err = user.Notes().Eq("id", 1).Update(ctx, shopdata.NotesUpdate{Body: &edited})
	check(err)
	_, err = user.Notes().Is("id", nil).Delete(ctx)
	check(err)
	if _, err := user.Notes().Update(ctx, shopdata.NotesUpdate{}); err == nil {
		panic("unfiltered update accepted")
	}
	server := &shopdata.Client{BaseURL: os.Args[1], ServerKey: "gdsk_key", Subject: "user-7"}
	_, err = server.RPCCreateNote(ctx, shopdata.CreateNoteArgs{Body: "via rpc"})
	check(err)
	count, err := server.RPCNoteCount(ctx, shopdata.NoteCountArgs{})
	check(err)
	check(server.RPCTouch(ctx, shopdata.TouchArgs{}))
	var apiErr *shopdata.Error
	_, missing := user.Relation("api", "missing").Get(ctx)
	if !errors.As(missing, &apiErr) {
		panic("expected API error")
	}
	fmt.Printf("rows=%d status=%s tags=%d inserted=%d count=%.0f error=%d:%s:%s\n", len(rows), rows[0].Status, len(rows[0].Tags), len(inserted), *count, apiErr.Status, apiErr.Code, apiErr.RequestID)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
`

func TestRenderPythonRunsAgainstTheAPI(t *testing.T) {
	source, err := RenderPython(sampleContract(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, want := range []string{"# Code generated by gregale data-api types. DO NOT EDIT.", `NoteStatus = Literal["draft", "published"]`, `NotesRow = TypedDict("NotesRow", {"id": float, "body": str, "status": NoteStatus, "tags": Optional[List[Optional[str]]], "meta": Json, "class": Optional[str], "address": Optional["AddressComposite"]})`, "class NotesInsert(_NotesInsertRequired, _NotesInsertOptional):", `def notes(self) -> "Table[NotesRow, NotesInsert, NotesUpdate]":`, `def rpc_create_note(self, args: CreateNoteArgs) -> List["NotesRow"]:`, `def rpc_touch(self, args: TouchArgs) -> None:`} {
		if !strings.Contains(text, want) {
			t.Errorf("generated Python lacks %q", want)
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	fake := &fakeDataAPI{}
	server := httptest.NewServer(fake)
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shop_data.py"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "smoke.py"), []byte(pythonSmoke), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join(dir, "smoke.py"), dir, server.URL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python smoke: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "rows=1 status=draft tags=2 inserted=1 count=3 error=401:token_invalid:req-1" {
		t.Fatalf("smoke output = %q", got)
	}
	compareRequests(t, fake.recorded())
}

// The renderer adds no method for the "missing" relation; the smoke test uses
// the untyped table() escape hatch for the error path.
const pythonSmoke = `import sys
sys.path.insert(0, sys.argv[1])
import shop_data

user = shop_data.DataClient(sys.argv[2], access_token=lambda: "user-token")
rows = user.notes().select("id,body").eq("status", "draft").in_("id", [1, "2"]).order("id", descending=True).order("body").limit(5).get()
inserted = user.notes().insert({"body": "hi", "class": "x"})
user.notes().eq("id", 1).update({"body": "edited"})
user.notes().is_("id", None).delete()
try:
    user.notes().update({})
    raise SystemExit("unfiltered update accepted")
except ValueError:
    pass
server = shop_data.DataClient(sys.argv[2], server_key="gdsk_key", subject="user-7")
server.rpc_create_note({"body": "via rpc"})
count = server.rpc_note_count({})
server.rpc_touch({})
try:
    user.table("missing").get()
    raise SystemExit("expected API error")
except shop_data.DataAPIError as error:
    failure = f"{error.status}:{error.code}:{error.request_id}"
print(f"rows={len(rows)} status={rows[0]['status']} tags={len(rows[0]['tags'])} inserted={len(inserted)} count={count} error={failure}")
`
