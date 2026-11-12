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
		{Name: "worker", Root: "functions/worker", Runtime: "go1.26", Calls: FunctionCalls{Names: map[string]string{"users": "shop-users"}}},
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

// ADR-20261111150000 §1: each Node and Python function gets types for exactly
// the functions it may call; siblings are typed from their schemas, calls:
// apps and schema-less siblings are unknown; Go functions get no file.
func TestGenerateFunctionTypes(t *testing.T) {
	files, err := GenerateFunctionTypes(typedTestFunctions())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %v, want web, users and report (no Go file)", files)
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
