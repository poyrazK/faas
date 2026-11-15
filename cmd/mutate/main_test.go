package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `package p

const limit = 1 + 2

func f(a, b int, s, t string, x float64, ok bool) (int, string, bool) {
	if a < b && !ok {
		a += b
	}
	a++
	if s == t || x >= 1.5 {
		return -a, s + t, a%2 == 0
	}
	_ = x / 2
	return a * b, s, a != limit
}
`

func typed(t *testing.T, src string) (*token.FileSet, *ast.File, *types.Info) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	if _, err := (&types.Config{Importer: importer.Default()}).Check("p", fset, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	return fset, f, info
}

func TestMutantsInFile(t *testing.T) {
	fset, f, info := typed(t, sample)
	ms := mutantsInFile(fset, f, info, "p.go", []byte(sample))

	got := map[string]int{}
	for _, m := range ms {
		got[m.Kind+" "+m.From+"->"+orRemoved(m.To)]++
	}
	want := map[string]int{
		"CONDITIONALS_BOUNDARY <-><=":   1,
		"CONDITIONALS_NEGATION <->>=":   1,
		"INVERT_LOGICAL &&->||":         1,
		"INVERT_NOT !->(removed)":       1,
		"INVERT_ASSIGNMENTS +=->-=":     1,
		"INCREMENT_DECREMENT ++->--":    1,
		"CONDITIONALS_NEGATION ==->!=":  2, // s == t, a%2 == 0
		"INVERT_LOGICAL ||->&&":         1,
		"CONDITIONALS_BOUNDARY >=->>":   1,
		"CONDITIONALS_NEGATION >=-><":   1,
		"INVERT_NEGATIVES -->(removed)": 1,
		"ARITHMETIC_BASE %->*":          1, // a%2 (int)
		"ARITHMETIC_BASE /->*":          1, // x / 2 (float)
		"ARITHMETIC_BASE *->/":          1,
		"CONDITIONALS_NEGATION !=->==":  1,
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s: got %d, want %d", k, got[k], n)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected mutant %s", k)
		}
	}
	// s + t is string concatenation and const limit = 1 + 2 is folded: no
	// ARITHMETIC_BASE "+" mutants may appear.
	if got["ARITHMETIC_BASE +->-"] != 0 {
		t.Errorf("string concat or const expression was mutated")
	}
}

func TestApplyEveryMutantParses(t *testing.T) {
	fset, f, info := typed(t, sample)
	for _, m := range mutantsInFile(fset, f, info, "p.go", []byte(sample)) {
		out, err := apply([]byte(sample), m)
		if err != nil {
			t.Fatalf("%+v: %v", m, err)
		}
		if string(out) == sample {
			t.Fatalf("%+v: no change", m)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "p.go", out, 0); err != nil {
			t.Errorf("%s %s->%s at line %d does not parse: %v", m.Kind, m.From, m.To, m.Line, err)
		}
	}
}

func TestApplyRejectsStaleOffset(t *testing.T) {
	m := &mutant{File: "p.go", From: "<", To: "<=", offset: 0}
	if _, err := apply([]byte("package p"), m); err == nil {
		t.Fatal("want error when the operator is not at the offset")
	}
}

func TestShardOfPartitions(t *testing.T) {
	var ms []*mutant
	for i := 0; i < 10; i++ {
		ms = append(ms, &mutant{ID: i})
	}
	seen := map[int]bool{}
	for i := 1; i <= 3; i++ {
		for _, m := range shardOf(ms, i, 3) {
			if seen[m.ID] {
				t.Fatalf("mutant %d in two shards", m.ID)
			}
			seen[m.ID] = true
		}
	}
	if len(seen) != len(ms) {
		t.Fatalf("shards cover %d of %d mutants", len(seen), len(ms))
	}
}

// tinyModule writes a module whose test pins Abs(-3) == 3.
func tinyModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/tiny\n\ngo 1.21\n",
		"p/p.go":          "package p\n\nfunc Abs(x int) int {\n\tif x < 0 {\n\t\treturn -x\n\t}\n\treturn x\n}\n\nfunc Unused(x int) bool { return x > 1 }\n",
		"p/p_test.go":     "package p\n\nimport \"testing\"\n\nfunc TestAbs(t *testing.T) {\n\tif Abs(-3) != 3 {\n\t\tt.Fatal(Abs(-3))\n\t}\n}\n",
		"p/other_test.go": "package p\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunTestsOutcomes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module with the go tool")
	}
	mod := tinyModule(t)
	ctx := context.Background()
	t.Chdir(mod)
	cfg := config{pkg: "./p"}
	pkgs, err := resolvePkgs(ctx, cfg, "^TestAbs$")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(mod, "p", "p.go")
	orig, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	overlay := func(t *testing.T, body string) string {
		d := t.TempDir()
		f := filepath.Join(d, "p.go")
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ov := filepath.Join(d, "overlay.json")
		b, _ := json.Marshal(map[string]map[string]string{"Replace": {src: f}})
		if err := os.WriteFile(ov, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return ov
	}
	cases := []struct {
		name, body, want string
	}{
		{"unmutated", string(orig), statusLived},
		{"killed", strings.Replace(string(orig), "return -x", "return x", 1), statusKilled},
		{"not viable", strings.Replace(string(orig), "return -x", "return -\"x\"", 1), statusNotViable},
		{"timed out", strings.Replace(string(orig), "return -x", "for {\n\t\t}", 1), statusTimedOut},
		{"uncovered change lives", strings.Replace(string(orig), "x > 1", "x >= 1", 1), statusLived},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runTests(ctx, cfg, mod, pkgs, filepath.Join(t.TempDir(), "bin"), overlay(t, c.body), src, "", 2*time.Second)
			if res.err != nil {
				t.Fatal(res.err)
			}
			if res.status != c.want {
				t.Fatalf("status %s, want %s\n%s", res.status, c.want, res.output)
			}
		})
	}

	// End to end: Unused is never called, so its two mutants are not covered;
	// Abs's "<" mutants run.
	var buf bytes.Buffer
	rep, err := run(ctx, config{pkg: "./p", files: []string{"*.go"}, tests: []string{"p_test.go"}, workers: 2, shard: 1, shards: 1, timeoutCoeff: 5}, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if rep.Run != "^(TestAbs)$" {
		t.Errorf("run filter %q", rep.Run)
	}
	if rep.NotCovered != 2 || rep.Killed+rep.Lived+rep.TimedOut == 0 {
		t.Errorf("report %+v\n%s", rep, buf.String())
	}
}

func TestBlamesFile(t *testing.T) {
	mutantErr := "# example.com/m/p\npkg/sched/admission.go:13:69: invalid operation\n"
	cacheErr := "# github.com/onebox-faas/faas/pkg/wire\npkg/wire/debugprofile.go:20:2: could not import regexp (open /c/27/27c1-d: no such file or directory)\n"
	if !blamesFile([]byte(mutantErr), "/src/pkg/sched/admission.go") {
		t.Error("mutant compile error not attributed to the mutated file")
	}
	if blamesFile([]byte(cacheErr), "/src/pkg/sched/admission.go") {
		t.Error("unrelated cache failure attributed to the mutated file")
	}
	if blamesFile([]byte("pkg/sched/admission_cpu_quota.go:3:1: x\n"), "/src/pkg/sched/quota.go") {
		t.Error("matched a file that only shares a suffix")
	}
}

func TestCoverageProfile(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "c.out")
	body := strings.Join([]string{
		"mode: set",
		"example.com/m/p/a.go:3.10,5.2 2 1",
		"example.com/m/p/a.go:7.10,9.2 1 0",
		"example.com/m/p/sub/b.go:1.1,2.2 1 1",
		"example.com/m/other/c.go:1.1,2.2 1 1",
	}, "\n")
	if err := os.WriteFile(prof, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cov := coverage{}
	if err := cov.addProfile(prof, "example.com/m/p", "/src/p"); err != nil {
		t.Fatal(err)
	}
	for line, want := range map[int]bool{3: true, 4: true, 5: true, 7: false, 8: false} {
		if got := cov.has("/src/p/a.go", line); got != want {
			t.Errorf("line %d covered = %v, want %v", line, got, want)
		}
	}
	if cov.has("/src/p/sub/b.go", 1) || len(cov) != 1 {
		t.Errorf("subpackage or other package lines leaked into coverage: %v", cov)
	}
}

func TestTestFuncNames(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x_test.go")
	src := "package p\nfunc TestA(t *testing.T) {}\nfunc TestB(tt *testing.T) {}\nfunc helper(t *testing.T) {}\nfunc BenchmarkC(b *testing.B) {}\nfunc TestA2(t *testing.T) {}\n"
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := testFuncNames([]string{f, f})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "TestA,TestA2,TestB" {
		t.Fatalf("got %v", got)
	}
}

func TestMergeReportsCombinesShards(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, r report) {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Shard 1 alone is 100%, shard 2 alone 0%; together 3 killed of 4 scored.
	write("a.json", report{Package: "example.com/p", Mutants: []*mutant{{Status: statusKilled}, {Status: statusKilled}, {Status: statusNotCovered}}})
	write("b.json", report{Package: "example.com/p", Mutants: []*mutant{{Status: statusTimedOut}, {Status: statusLived}, {Status: statusNotViable}}})
	var buf bytes.Buffer
	rep, err := mergeReports(filepath.Join(dir, "*.json"), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Killed != 2 || rep.TimedOut != 1 || rep.Lived != 1 || rep.NotCovered != 1 || rep.NotViable != 1 || rep.Efficacy != 75 {
		t.Fatalf("merged = %+v", rep)
	}
	write("c.json", report{Package: "example.com/other"})
	if _, err := mergeReports(filepath.Join(dir, "*.json"), &buf); err == nil {
		t.Fatal("merged reports from two packages")
	}
	if _, err := mergeReports(filepath.Join(dir, "none-*.json"), &buf); err == nil {
		t.Fatal("merged an empty glob")
	}
	if _, err := parseFlags([]string{"-merge", "x/*.json"}); err != nil {
		t.Fatalf("-merge without -pkg rejected: %v", err)
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"-pkg", "./pkg/x", "-files", "a*.go, b.go", "-shard", "2/4", "-also", "./t"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.shard != 2 || cfg.shards != 4 || len(cfg.files) != 2 || cfg.also[0] != "./t" {
		t.Fatalf("got %+v", cfg)
	}
	for _, bad := range [][]string{{}, {"-pkg", "x", "-shard", "5/4"}, {"-pkg", "x", "-shard", "0/1"}} {
		if _, err := parseFlags(bad); err == nil {
			t.Errorf("parseFlags(%v) accepted", bad)
		}
	}
}
