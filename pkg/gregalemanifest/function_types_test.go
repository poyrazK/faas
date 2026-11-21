package gregalemanifest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func typedTestFunctions() []TypedFunction {
	users := json.RawMessage(`{"type":"object","required":["email"],"additionalProperties":false,"properties":{
		"email":{"type":"string"},"age":{"type":"integer"},"role":{"enum":["admin","member"]},
		"address":{"type":"object","required":["city"],"properties":{"city":{"type":"string"},"zip-code":{"type":["string","null"]}}},
		"tags":{"type":"array","items":{"type":"string"}},"class":{"type":"boolean"}}}`)
	return []TypedFunction{
		{Name: "web", Root: "functions/web", Runtime: "node24", Calls: FunctionCalls{Names: map[string]string{"users": "shop-users", "billing-api": "billing-api", "report": "shop-report"}}},
		{Name: "users", Root: "functions/users", Runtime: "python312", Input: users, Output: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
			Calls: FunctionCalls{Names: map[string]string{"web": "shop-web"}}},
		{Name: "report", Root: "functions/report", Runtime: "python312", Input: json.RawMessage(`{"type":"array","items":{"type":"number"}}`),
			Calls: FunctionCalls{Names: map[string]string{"users": "shop-users", "billing-api": "billing-api"}}},
		{Name: "worker", Root: "functions/worker", Runtime: "go124", GoPackage: "main", Calls: FunctionCalls{Names: map[string]string{"users": "shop-users"}}},
	}
}

func generatedFile(t *testing.T, files []FunctionTypesFile, path string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("no generated %s in %v", path, files)
	return ""
}

// ADR-20261111150000 §1: each Node, Python and Go function gets types for
// exactly the functions it may call; siblings are typed from their schemas,
// calls: apps and schema-less siblings are unknown.
func TestGenerateFunctionTypes(t *testing.T) {
	files, err := GenerateFunctionTypes(typedTestFunctions())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %v, want web, users, report and worker", files)
	}
	ts := generatedFile(t, files, "functions/web/gregale-functions.d.ts")
	for _, want := range []string{
		"import '@gregale/sdk-node/functions/runtime';",
		"export type UsersInput = {\n",
		"  email: string;\n",
		"  age?: number;\n",
		`  role?: "admin" | "member";`,
		`    "zip-code"?: string | null;`,
		"  tags?: string[];\n",
		"export type UsersOutput = {\n  id?: string;\n};",
		"export type BillingApiInput = unknown;",
		"export type ReportInput = number[];",
		"export type ReportOutput = unknown;",
		`    "billing-api": { input: BillingApiInput; output: BillingApiOutput };`,
		"    users: { input: UsersInput; output: UsersOutput };",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("d.ts missing %q:\n%s", want, ts)
		}
	}
	py := generatedFile(t, files, "functions/report/gregale_functions.py")
	for _, want := range []string{
		`UsersInputAddress = TypedDict("UsersInputAddress", {`,
		`    "zip-code": NotRequired[str | None],`,
		`UsersInput = TypedDict("UsersInput", {`,
		`    "email": str,`,
		`    "class": NotRequired[bool],`,
		`    "role": NotRequired[Literal["admin", "member"]],`,
		"BillingApiInput = Any",
		"    async def call(self, input: UsersInput, *, path: str = ..., headers: Mapping[str, str] | None = ...) -> UsersOutput: ...",
		"    users: UsersClient\n",
		"functions: GregaleFunctions = cast(\"GregaleFunctions\", _functions)",
	} {
		if !strings.Contains(py, want) {
			t.Errorf("python missing %q:\n%s", want, py)
		}
	}
	if strings.Contains(py, "    billing-api:") {
		t.Errorf("a non-identifier name became a Protocol attribute:\n%s", py)
	}
	if strings.Index(py, "UsersInputAddress = ") > strings.Index(py, "UsersInput = ") {
		t.Errorf("nested TypedDict defined after its use:\n%s", py)
	}
}

// The generated declaration file type-checks and its augmentation types the
// SDK's GregaleFunctions: an input missing a required field fails to compile.
func TestGeneratedNodeFunctionTypesCompile(t *testing.T) {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("tsc is not installed")
	}
	files, err := GenerateFunctionTypes(typedTestFunctions())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("sdk.d.ts", "declare module '@gregale/sdk-node/functions/runtime' {\n  export interface GregaleFunctions {}\n}\n")
	write(NodeFunctionTypesFile, generatedFile(t, files, "functions/web/gregale-functions.d.ts"))
	check := func(use string) ([]byte, error) {
		write("use.ts", "import type { GregaleFunctions } from '@gregale/sdk-node/functions/runtime';\nexport {};\n"+use)
		cmd := exec.Command(tsc, "--noEmit", "--strict", "--target", "es2022", "--module", "nodenext", "sdk.d.ts", NodeFunctionTypesFile, "use.ts")
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := check(`const input: GregaleFunctions['users']['input'] = { email: 'a@b', address: { city: 'x', 'zip-code': null } };
const tags: GregaleFunctions['report']['input'] = [1, 2];
const anything: GregaleFunctions['billing-api']['input'] = 42;
void input; void tags; void anything;
`); err != nil {
		t.Fatalf("valid use failed to compile: %v\n%s", err, out)
	}
	if _, err := check("const input: GregaleFunctions['users']['input'] = { age: 3 };\nvoid input;\n"); err == nil {
		t.Fatal("an input missing the required email compiled")
	}
}

// The generated Python imports cleanly against the SDK and its TypedDicts
// carry the schema's keys.
func TestGeneratedPythonFunctionTypesImport(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	sdk, err := filepath.Abs("../../sdk/python")
	if err != nil {
		t.Fatal(err)
	}
	if exec.Command(python, "-c", "import httpx").Run() != nil {
		t.Skip("httpx is not installed for python3")
	}
	files, err := GenerateFunctionTypes(typedTestFunctions())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PythonFunctionTypesFile), []byte(generatedFile(t, files, "functions/report/gregale_functions.py")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-c", `import gregale_functions as g
assert set(g.UsersInput.__required_keys__) == {"email"}, g.UsersInput.__required_keys__
assert "zip-code" in g.UsersInputAddress.__optional_keys__
assert g.functions is not None`)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir+string(os.PathListSeparator)+sdk)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import generated types: %v\n%s", err, out)
	}
}

// ADR-20261112160000 §4: a Go function gets structs from its callees' schemas
// and a typed method pair per callee over the SDK's generic client; the file
// compiles against the SDK and a wrong input type does not.
func TestGeneratedGoFunctionTypesCompile(t *testing.T) {
	files, err := GenerateFunctionTypes(typedTestFunctions())
	if err != nil {
		t.Fatal(err)
	}
	src := generatedFile(t, files, "functions/worker/gregale_functions.go")
	for _, want := range []string{
		"// Code generated by `gregale function types`; DO NOT EDIT.",
		"package main",
		"type UsersInput struct {",
		"Email   string             `json:\"email\"`",
		"Age     *int64             `json:\"age,omitempty\"`",
		"Address *UsersInputAddress `json:\"address,omitempty\"`",
		"ZipCode *string `json:\"zip-code,omitempty\"`",
		"Tags    []string           `json:\"tags,omitempty\"`",
		"func (gregaleFunctions) Users(ctx context.Context, in UsersInput, opts ...gregalefunctions.Option) (UsersOutput, error)",
		"func (gregaleFunctions) SendUsers(ctx context.Context, in UsersInput",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Go missing %q:\n%s", want, src)
		}
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not installed")
	}
	sdk, err := filepath.Abs("../../sdk/go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module worker\n\ngo 1.23\n\nrequire github.com/gregale-dev/gregale-go v0.0.0\n\nreplace github.com/gregale-dev/gregale-go => "+sdk+"\n")
	write(GoFunctionTypesFile, src)
	vet := func(use string) ([]byte, error) {
		write("handler.go", "package main\n\nimport \"context\"\n\nfunc main() {\n\tctx := context.Background()\n"+use+"}\n")
		cmd := exec.Command(goBin, "vet", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
		return cmd.CombinedOutput()
	}
	if out, err := vet("\tout, err := functions.Users(ctx, UsersInput{Email: \"a@b\"})\n\t_, _ = out.ID, err\n" +
		"\tsent, _ := functions.SendUsers(ctx, UsersInput{Email: \"a@b\"})\n\t_ = sent\n"); err != nil {
		t.Fatalf("valid use failed to compile: %v\n%s", err, out)
	}
	if _, err := vet("\t_, _ = functions.Users(ctx, 42)\n"); err == nil {
		t.Fatal("a wrong input type compiled")
	}
}
