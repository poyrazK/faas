// Command mutate runs mutation testing over a narrow slice of one package.
//
// It flips one operator at a time in the target files (a < becomes <=, a +
// becomes -, a ! disappears, ...) and re-runs a chosen set of tests against
// each mutant. A mutant the tests still pass on ("lived") marks a line whose
// behaviour no test pins down. That is the gap line coverage cannot see: a
// line can be executed by a dozen tests and still have no assertion that
// would notice it computing the wrong answer.
//
// Off-the-shelf tools did not fit this repo. Gremlins copies the whole module
// per worker and runs a package's entire suite per mutant (pkg/sched is
// ~2 minutes of tests); go-mutesting needs a newer Go than go.mod pins and
// edits files in place. This harness instead hands each mutant to
// `go test -overlay`, so the checkout is never modified, workers share the
// build cache, and each run is limited to the tests that own the code:
//
//	mutate -pkg ./pkg/sched -files 'admission*.go' \
//	    -tests 'admission*_test.go,*ledger*_test.go' -also ./tests/property
//
// Mutants on lines the selected tests never execute are reported as
// "not covered" without being run. Mutants that do not compile (a type-aware
// filter removes most up front) are "not viable" and excluded from scores.
//
// Efficacy = killed / (killed + lived). Survivors are printed with the
// mutated line so they can be triaged: some are equivalent mutants (the
// change cannot alter behaviour) and are expected.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

type config struct {
	pkg          string
	files        []string
	tests        []string
	also         []string
	run          string
	workers      int
	shard        int
	shards       int
	timeoutCoeff float64
	minEfficacy  float64
	jsonOut      string
	list         bool
	tags         string
	testFlags    []string
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(2)
	}
	ctx := context.Background()
	rep, err := run(ctx, cfg, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(2)
	}
	if rep.BuildError > 0 {
		fmt.Fprintf(os.Stderr, "mutate: %d mutants hit build errors unrelated to the mutation; results are incomplete, rerun\n", rep.BuildError)
		os.Exit(1)
	}
	if cfg.minEfficacy > 0 && rep.Efficacy < cfg.minEfficacy {
		fmt.Fprintf(os.Stderr, "mutate: efficacy %.1f%% below -min-efficacy %.1f%%\n", rep.Efficacy, cfg.minEfficacy)
		os.Exit(1)
	}
}

func parseFlags(args []string) (config, error) {
	var cfg config
	var files, tests, also, shard string
	fs := flag.NewFlagSet("mutate", flag.ContinueOnError)
	fs.StringVar(&cfg.pkg, "pkg", "", "package directory to mutate, e.g. ./pkg/billing (required)")
	fs.StringVar(&files, "files", "*.go", "comma-separated globs (in -pkg) of files to mutate; _test.go and generated files are always skipped")
	fs.StringVar(&tests, "tests", "", "comma-separated globs (in -pkg) of _test.go files whose Test funcs form the -run filter; empty runs every test")
	fs.StringVar(&cfg.run, "run", "", "explicit -run regexp; overrides -tests")
	fs.StringVar(&also, "also", "", "comma-separated extra packages whose whole suites also run against each mutant, e.g. ./tests/property")
	fs.IntVar(&cfg.workers, "workers", 2, "concurrent go test processes")
	fs.StringVar(&shard, "shard", "1/1", "run only shard i of n of the mutants (i/n)")
	fs.Float64Var(&cfg.timeoutCoeff, "timeout-coefficient", 5, "per-mutant test timeout = baseline test time x this + 30s")
	fs.Float64Var(&cfg.minEfficacy, "min-efficacy", 0, "exit 1 when efficacy (percent) is below this; 0 disables")
	fs.StringVar(&cfg.jsonOut, "json", "", "write the full report as JSON to this path")
	fs.BoolVar(&cfg.list, "list", false, "list mutants and coverage status without running them")
	fs.StringVar(&cfg.tags, "tags", "", "build tags passed to go test")
	var testFlags string
	fs.StringVar(&testFlags, "testflags", "", "extra space-separated go test flags for every run, e.g. -ldflags=-w")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.pkg == "" {
		return cfg, errors.New("-pkg is required")
	}
	cfg.files = splitList(files)
	cfg.tests = splitList(tests)
	cfg.also = splitList(also)
	cfg.testFlags = strings.Fields(testFlags)
	if cfg.workers < 1 {
		cfg.workers = 1
	}
	if _, err := fmt.Sscanf(shard, "%d/%d", &cfg.shard, &cfg.shards); err != nil || cfg.shards < 1 || cfg.shard < 1 || cfg.shard > cfg.shards {
		return cfg, fmt.Errorf("-shard %q: want i/n with 1 <= i <= n", shard)
	}
	return cfg, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Status values match the vocabulary of common Go mutation tools.
const (
	statusKilled     = "killed"
	statusLived      = "lived"
	statusTimedOut   = "timed out"
	statusNotCovered = "not covered"
	statusNotViable  = "not viable"
	// statusBuildError: the build failed for a reason other than the
	// mutant, even after retries. Not scored; rerun the shard.
	statusBuildError = "build error"
)

type mutant struct {
	ID     int    `json:"id"`
	File   string `json:"file"` // path relative to the module root
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Kind   string `json:"kind"`
	From   string `json:"from"`
	To     string `json:"to"`
	Source string `json:"source"` // the original line, trimmed
	Status string `json:"status"`

	absFile string
	offset  int
}

type report struct {
	Package    string    `json:"package"`
	Run        string    `json:"run,omitempty"`
	Shard      string    `json:"shard"`
	Killed     int       `json:"killed"`
	Lived      int       `json:"lived"`
	TimedOut   int       `json:"timed_out"`
	NotCovered int       `json:"not_covered"`
	NotViable  int       `json:"not_viable"`
	BuildError int       `json:"build_error"`
	Efficacy   float64   `json:"efficacy_percent"`
	MCoverage  float64   `json:"mutant_coverage_percent"`
	Elapsed    string    `json:"elapsed"`
	Mutants    []*mutant `json:"mutants"`
}

func run(ctx context.Context, cfg config, out io.Writer) (*report, error) {
	start := time.Now()
	modRoot, err := goEnv(ctx, "GOMOD")
	if err != nil {
		return nil, err
	}
	if modRoot == "" || modRoot == os.DevNull {
		return nil, errors.New("not inside a Go module")
	}
	modRoot = filepath.Dir(modRoot)

	pkgDir, err := filepath.Abs(cfg.pkg)
	if err != nil {
		return nil, err
	}
	targets, err := globFiles(pkgDir, cfg.files, false)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no files match %v in %s", cfg.files, cfg.pkg)
	}

	runRe := cfg.run
	if runRe == "" && len(cfg.tests) > 0 {
		testFiles, err := globFiles(pkgDir, cfg.tests, true)
		if err != nil {
			return nil, err
		}
		names, err := testFuncNames(testFiles)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("no Test functions in files matching %v", cfg.tests)
		}
		runRe = "^(" + strings.Join(names, "|") + ")$"
	}

	mutants, err := enumerate(ctx, pkgDir, targets, cfg.tags)
	if err != nil {
		return nil, err
	}
	for _, m := range mutants {
		if rel, err := filepath.Rel(modRoot, m.absFile); err == nil {
			m.File = rel
		}
	}
	mutants = shardOf(mutants, cfg.shard, cfg.shards)

	// Baseline: the selected tests must pass unmutated, and their coverage
	// tells us which mutants are worth running at all.
	tmp, err := os.MkdirTemp("", "mutate-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	pkgs, err := resolvePkgs(ctx, cfg, runRe)
	if err != nil {
		return nil, err
	}
	pkgImport := pkgs[0].importPath
	_, _ = fmt.Fprintf(out, "mutate: %s, %d files, %d mutants (shard %d/%d)\n", pkgImport, len(targets), len(mutants), cfg.shard, cfg.shards)
	_, _ = fmt.Fprintf(out, "mutate: baseline build and test run...\n")
	baseDir := filepath.Join(tmp, "baseline")
	base := runTests(ctx, cfg, modRoot, pkgs, baseDir, "", "", pkgImport, 0)
	if base.err != nil {
		return nil, base.err
	}
	if base.status != statusLived {
		return nil, fmt.Errorf("baseline tests do not pass without any mutation (%s); fix them first:\n%s", base.status, tail(base.output, 40))
	}
	covered := coverage{}
	for _, p := range pkgs {
		prof := filepath.Join(baseDir, p.bin+".cover")
		if _, err := os.Stat(prof); err != nil {
			continue
		}
		if err := covered.addProfile(prof, pkgImport, pkgDir); err != nil {
			return nil, err
		}
	}

	var runnable []*mutant
	for _, m := range mutants {
		if covered.has(m.absFile, m.Line) {
			runnable = append(runnable, m)
		} else {
			m.Status = statusNotCovered
		}
	}
	_, _ = fmt.Fprintf(out, "mutate: baseline tests ran in %s; %d of %d mutants on covered lines\n", base.runTime.Round(time.Millisecond), len(runnable), len(mutants))

	if !cfg.list {
		// Only the run phase is bounded: compiling a mutant of a large
		// package can take minutes on a busy machine and says nothing about
		// whether the mutant loops.
		timeout := time.Duration(float64(base.runTime)*cfg.timeoutCoeff) + 30*time.Second
		if err := execute(ctx, cfg, modRoot, tmp, pkgs, runnable, timeout, out); err != nil {
			return nil, err
		}
	}

	rep := summarize(pkgImport, runRe, cfg, mutants)
	rep.Elapsed = time.Since(start).Round(time.Second).String()
	printReport(out, rep, cfg.list)
	if cfg.jsonOut != "" {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.jsonOut, b, 0o644); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// testPkg is one package whose tests run against every mutant: -pkg with
// the -run filter, then each -also package whole.
type testPkg struct {
	path       string // as given on the command line
	importPath string
	dir        string // source dir; test binaries run here, as under go test
	run        string // -test.run filter; empty runs every test
	bin        string // file name go test -c -o <dir>/ writes
}

func resolvePkgs(ctx context.Context, cfg config, runRe string) ([]testPkg, error) {
	paths := append([]string{cfg.pkg}, cfg.also...)
	seen := map[string]string{}
	var out []testPkg
	for i, p := range paths {
		b, err := exec.CommandContext(ctx, "go", "list", "-f", "{{.ImportPath}}\t{{.Dir}}", p).Output()
		if err != nil {
			return nil, fmt.Errorf("go list %s: %w", p, err)
		}
		f := strings.Split(strings.TrimSpace(string(b)), "\t")
		if len(f) != 2 || strings.Contains(f[1], "\n") {
			return nil, fmt.Errorf("go list %s: want exactly one package, got %q", p, b)
		}
		tp := testPkg{path: p, importPath: f[0], dir: f[1], bin: pathBase(f[0]) + ".test"}
		if i == 0 {
			tp.run = runRe
		}
		if other, dup := seen[tp.bin]; dup {
			return nil, fmt.Errorf("%s and %s would both build %s; list them in separate runs", other, p, tp.bin)
		}
		seen[tp.bin] = p
		out = append(out, tp)
	}
	return out, nil
}

func pathBase(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

type testResult struct {
	status  string // statusLived (all passed), statusKilled, statusTimedOut or statusNotViable
	runTime time.Duration
	output  []byte
	err     error // harness failure, not a test outcome
}

// runTests builds every test binary with one untimed go test -c, then runs
// them in order. The first failing package decides the result. coverPkg,
// when set, instruments that package and writes <bin>.cover profiles.
// mutatedFile is the source path the overlay replaces ("" for the baseline).
func runTests(ctx context.Context, cfg config, modRoot string, pkgs []testPkg, dir, overlay, mutatedFile, coverPkg string, timeout time.Duration) testResult {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return testResult{err: err}
	}
	args := append([]string{"test", "-c", "-o", dir + string(filepath.Separator)}, cfg.testFlags...)
	if cfg.tags != "" {
		args = append(args, "-tags", cfg.tags)
	}
	if overlay != "" {
		// vet already passed on the baseline; on a mutant it only costs time
		// and can misreport a real mutant as a build failure.
		args = append(args, "-overlay", overlay, "-vet=off")
	}
	if coverPkg != "" {
		args = append(args, "-cover", "-coverpkg", coverPkg)
	}
	for _, p := range pkgs {
		args = append(args, p.path)
	}
	// A build failure counts as "not viable" only when the compiler blames
	// the mutated file. Anything else is the environment — e.g. a shared
	// GOCACHE entry trimmed mid-build ("could not import io (open
	// .../49/...-d: no such file or directory)") — and is retried, then
	// reported as a build error rather than scored.
	var b []byte
	for attempt := 1; ; attempt++ {
		build := exec.CommandContext(ctx, "go", args...)
		build.Dir = modRoot
		var err error
		if b, err = build.CombinedOutput(); err == nil {
			break
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return testResult{err: fmt.Errorf("go test -c: %w", err)}
		}
		if mutatedFile != "" && blamesFile(b, mutatedFile) {
			return testResult{status: statusNotViable, output: b}
		}
		if attempt == buildAttempts || ctx.Err() != nil {
			return testResult{status: statusBuildError, output: b}
		}
		time.Sleep(time.Duration(attempt) * 5 * time.Second)
	}

	res := testResult{status: statusLived}
	for _, p := range pkgs {
		bin := filepath.Join(dir, p.bin)
		if _, err := os.Stat(bin); err != nil {
			continue // package has no test files
		}
		targs := []string{"-test.count=1"}
		if p.run != "" {
			targs = append(targs, "-test.run", p.run)
		}
		if overlay != "" {
			targs = append(targs, "-test.failfast")
		}
		if timeout > 0 {
			targs = append(targs, "-test.timeout", timeout.String())
		}
		if coverPkg != "" {
			targs = append(targs, "-test.coverprofile", bin+".cover")
		}
		code, out, elapsed, timedOut, err := runBinary(ctx, bin, p.dir, targs, timeout)
		res.runTime += elapsed
		res.output = append(res.output, out...)
		switch {
		case err != nil:
			return testResult{err: err}
		case timedOut || (code != 0 && bytes.Contains(out, []byte("panic: test timed out"))):
			res.status = statusTimedOut
			return res
		case code != 0:
			res.status = statusKilled
			return res
		}
	}
	return res
}

const buildAttempts = 3

// blamesFile reports whether compiler output points at a line of file
// ("path/to/file.go:12:3: ..."), as opposed to an unrelated failure.
func blamesFile(out []byte, file string) bool {
	return regexp.MustCompile(`(^|[\s/])` + regexp.QuoteMeta(filepath.Base(file)) + `:\d+`).Match(out)
}

func runBinary(ctx context.Context, bin, dir string, args []string, timeout time.Duration) (code int, out []byte, elapsed time.Duration, timedOut bool, err error) {
	if timeout > 0 {
		// -test.timeout fires first and prints the stuck goroutines; this is
		// the backstop for a binary that cannot even do that.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout+30*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 10 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	start := time.Now()
	runErr := cmd.Run()
	elapsed = time.Since(start)
	if ctx.Err() == context.DeadlineExceeded {
		return -1, buf.Bytes(), elapsed, true, nil
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		return ee.ExitCode(), buf.Bytes(), elapsed, false, nil
	}
	if runErr != nil {
		return 0, nil, elapsed, false, fmt.Errorf("run %s: %w", bin, runErr)
	}
	return 0, buf.Bytes(), elapsed, false, nil
}

func goEnv(ctx context.Context, key string) (string, error) {
	b, err := exec.CommandContext(ctx, "go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func globFiles(dir string, globs []string, wantTests bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(dir, g))
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
		for _, m := range matches {
			isTest := strings.HasSuffix(m, "_test.go")
			if !strings.HasSuffix(m, ".go") || isTest != wantTests || seen[m] {
				continue
			}
			if !wantTests {
				if gen, err := isGenerated(m); err != nil {
					return nil, err
				} else if gen {
					continue
				}
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out, nil
}

var generatedRe = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

func isGenerated(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return generatedRe.Match(b), nil
}

var testFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(\w+ \*testing\.T\)`)

func testFuncNames(files []string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range testFuncRe.FindAllSubmatch(b, -1) {
			n := string(m[1])
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// enumerate type-checks the package and lists every operator mutation in the
// target files that is expected to compile.
func enumerate(ctx context.Context, pkgDir string, targets []string, tags string) ([]*mutant, error) {
	want := map[string]bool{}
	for _, t := range targets {
		want[t] = true
	}
	pcfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     pkgDir,
	}
	if tags != "" {
		pcfg.BuildFlags = []string{"-tags", tags}
	}
	pkgs, err := packages.Load(pcfg, ".")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("expected one package in %s, got %d", pkgDir, len(pkgs))
	}
	p := pkgs[0]
	if len(p.Errors) > 0 {
		return nil, fmt.Errorf("load %s: %w", pkgDir, p.Errors[0])
	}
	var all []*mutant
	for _, f := range p.Syntax {
		path := p.Fset.File(f.Pos()).Name()
		if !want[path] {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		all = append(all, mutantsInFile(p.Fset, f, p.TypesInfo, path, src)...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].absFile != all[j].absFile {
			return all[i].absFile < all[j].absFile
		}
		return all[i].offset < all[j].offset
	})
	for i, m := range all {
		m.ID = i + 1
	}
	return all, nil
}

// Operator replacement tables. Kinds follow the usual Go mutation operator
// names so reports read the same as other tools'.
var (
	arithmetic = map[token.Token]token.Token{token.ADD: token.SUB, token.SUB: token.ADD, token.MUL: token.QUO, token.QUO: token.MUL, token.REM: token.MUL}
	boundary   = map[token.Token]token.Token{token.LSS: token.LEQ, token.LEQ: token.LSS, token.GTR: token.GEQ, token.GEQ: token.GTR}
	negation   = map[token.Token]token.Token{token.EQL: token.NEQ, token.NEQ: token.EQL, token.LSS: token.GEQ, token.GEQ: token.LSS, token.GTR: token.LEQ, token.LEQ: token.GTR}
	logical    = map[token.Token]token.Token{token.LAND: token.LOR, token.LOR: token.LAND}
	assignArth = map[token.Token]token.Token{token.ADD_ASSIGN: token.SUB_ASSIGN, token.SUB_ASSIGN: token.ADD_ASSIGN, token.MUL_ASSIGN: token.QUO_ASSIGN, token.QUO_ASSIGN: token.MUL_ASSIGN}
)

func mutantsInFile(fset *token.FileSet, f *ast.File, info *types.Info, path string, src []byte) []*mutant {
	var out []*mutant
	add := func(pos token.Pos, kind string, from, to token.Token) {
		p := fset.Position(pos)
		out = append(out, &mutant{
			Kind: kind, From: from.String(), To: to.String(), Line: p.Line, Col: p.Column,
			Source: lineAt(src, p.Line), absFile: path, offset: p.Offset,
		})
	}
	addRemove := func(pos token.Pos, kind string, op token.Token) {
		p := fset.Position(pos)
		out = append(out, &mutant{
			Kind: kind, From: op.String(), To: "", Line: p.Line, Col: p.Column,
			Source: lineAt(src, p.Line), absFile: path, offset: p.Offset,
		})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.GenDecl:
			// Constant expressions are folded at compile time; flipping them
			// mostly yields overflow or duplicate-case errors, not mutants.
			if n.Tok == token.CONST {
				return false
			}
		case *ast.BinaryExpr:
			if isConst(info, n) {
				return true
			}
			xt := underlying(info, n.X)
			if to, ok := arithmetic[n.Op]; ok && isNumeric(xt) && (n.Op != token.REM || !isFloat(xt)) {
				add(n.OpPos, "ARITHMETIC_BASE", n.Op, to)
			}
			if to, ok := boundary[n.Op]; ok {
				add(n.OpPos, "CONDITIONALS_BOUNDARY", n.Op, to)
			}
			if to, ok := negation[n.Op]; ok {
				add(n.OpPos, "CONDITIONALS_NEGATION", n.Op, to)
			}
			if to, ok := logical[n.Op]; ok {
				add(n.OpPos, "INVERT_LOGICAL", n.Op, to)
			}
		case *ast.AssignStmt:
			if to, ok := assignArth[n.Tok]; ok && len(n.Lhs) == 1 && isNumeric(underlying(info, n.Lhs[0])) {
				add(n.TokPos, "INVERT_ASSIGNMENTS", n.Tok, to)
			}
		case *ast.IncDecStmt:
			to := token.DEC
			if n.Tok == token.DEC {
				to = token.INC
			}
			add(n.TokPos, "INCREMENT_DECREMENT", n.Tok, to)
		case *ast.UnaryExpr:
			if isConst(info, n) {
				return true
			}
			switch n.Op {
			case token.NOT:
				addRemove(n.OpPos, "INVERT_NOT", n.Op)
			case token.SUB:
				if isNumeric(underlying(info, n.X)) {
					addRemove(n.OpPos, "INVERT_NEGATIVES", n.Op)
				}
			}
		}
		return true
	})
	return out
}

func isConst(info *types.Info, e ast.Expr) bool {
	if info == nil {
		return false
	}
	tv, ok := info.Types[e]
	return ok && tv.Value != nil
}

func underlying(info *types.Info, e ast.Expr) types.Type {
	if info == nil {
		return nil
	}
	if t := info.TypeOf(e); t != nil {
		return t.Underlying()
	}
	return nil
}

func isNumeric(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Info()&types.IsNumeric != 0
}

func isFloat(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Info()&types.IsFloat != 0
}

func lineAt(src []byte, line int) string {
	for i := 1; i < line; i++ {
		j := bytes.IndexByte(src, '\n')
		if j < 0 {
			return ""
		}
		src = src[j+1:]
	}
	if j := bytes.IndexByte(src, '\n'); j >= 0 {
		src = src[:j]
	}
	return strings.TrimSpace(string(src))
}

// apply returns src with the mutant's operator replaced.
func apply(src []byte, m *mutant) ([]byte, error) {
	end := m.offset + len(m.From)
	if end > len(src) || string(src[m.offset:end]) != m.From {
		return nil, fmt.Errorf("%s:%d: operator %q not found at offset %d", m.File, m.Line, m.From, m.offset)
	}
	out := make([]byte, 0, len(src)+2)
	out = append(out, src[:m.offset]...)
	out = append(out, m.To...)
	return append(out, src[end:]...), nil
}

func shardOf(ms []*mutant, i, n int) []*mutant {
	if n <= 1 {
		return ms
	}
	var out []*mutant
	for k, m := range ms {
		if k%n == i-1 {
			out = append(out, m)
		}
	}
	return out
}

type coverage map[string]map[int]bool

func (c coverage) has(file string, line int) bool { return c[file][line] }

// addProfile adds the covered (count > 0) lines of the package's own files.
// Profile paths are import-path based: <import>/<file>.go.
func (cov coverage) addProfile(path, pkgImport, pkgDir string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// pkg/file.go:12.34,15.2 3 1
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		file, rest := line[:colon], line[colon+1:]
		if !strings.HasPrefix(file, pkgImport+"/") {
			continue
		}
		name := strings.TrimPrefix(file, pkgImport+"/")
		if strings.Contains(name, "/") {
			continue
		}
		var sl, sc, el, ec, stmts, count int
		if _, err := fmt.Sscanf(rest, "%d.%d,%d.%d %d %d", &sl, &sc, &el, &ec, &stmts, &count); err != nil {
			return fmt.Errorf("cover profile line %q: %w", line, err)
		}
		if count == 0 {
			continue
		}
		abs := filepath.Join(pkgDir, name)
		if cov[abs] == nil {
			cov[abs] = map[int]bool{}
		}
		for l := sl; l <= el; l++ {
			cov[abs][l] = true
		}
	}
	return nil
}

func execute(ctx context.Context, cfg config, modRoot, tmp string, pkgs []testPkg, ms []*mutant, timeout time.Duration, out io.Writer) error {
	sources := map[string][]byte{}
	for _, m := range ms {
		if _, ok := sources[m.absFile]; !ok {
			b, err := os.ReadFile(m.absFile)
			if err != nil {
				return err
			}
			sources[m.absFile] = b
		}
	}
	jobs := make(chan *mutant)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	for w := 0; w < min(cfg.workers, max(len(ms), 1)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				status, err := runMutant(ctx, cfg, modRoot, tmp, pkgs, m, sources[m.absFile], timeout)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				m.Status = status
				done++
				_, _ = fmt.Fprintf(out, "[%d/%d] %-11s %s:%d:%d %s %s->%s\n", done, len(ms), status, m.File, m.Line, m.Col, m.Kind, m.From, orRemoved(m.To))
				mu.Unlock()
			}
		}()
	}
	for _, m := range ms {
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func orRemoved(s string) string {
	if s == "" {
		return "(removed)"
	}
	return s
}

func runMutant(ctx context.Context, cfg config, modRoot, tmp string, pkgs []testPkg, m *mutant, src []byte, timeout time.Duration) (string, error) {
	mutated, err := apply(src, m)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(tmp, strconv.Itoa(m.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	mf := filepath.Join(dir, filepath.Base(m.absFile))
	if err := os.WriteFile(mf, mutated, 0o644); err != nil {
		return "", err
	}
	ov, err := json.Marshal(map[string]map[string]string{"Replace": {m.absFile: mf}})
	if err != nil {
		return "", err
	}
	ovf := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(ovf, ov, 0o644); err != nil {
		return "", err
	}
	res := runTests(ctx, cfg, modRoot, pkgs, filepath.Join(dir, "bin"), ovf, m.absFile, "", timeout)
	return res.status, res.err
}

func summarize(pkg, runRe string, cfg config, ms []*mutant) *report {
	rep := &report{Package: pkg, Run: runRe, Shard: fmt.Sprintf("%d/%d", cfg.shard, cfg.shards), Mutants: ms}
	for _, m := range ms {
		switch m.Status {
		case statusKilled:
			rep.Killed++
		case statusLived:
			rep.Lived++
		case statusTimedOut:
			rep.TimedOut++
		case statusNotCovered:
			rep.NotCovered++
		case statusNotViable:
			rep.NotViable++
		case statusBuildError:
			rep.BuildError++
		}
	}
	killed := rep.Killed + rep.TimedOut
	if d := killed + rep.Lived; d > 0 {
		rep.Efficacy = 100 * float64(killed) / float64(d)
	}
	if d := killed + rep.Lived + rep.NotCovered; d > 0 {
		rep.MCoverage = 100 * float64(killed+rep.Lived) / float64(d)
	}
	return rep
}

func printReport(out io.Writer, rep *report, listOnly bool) {
	_, _ = fmt.Fprintln(out)
	if listOnly {
		for _, m := range rep.Mutants {
			st := m.Status
			if st == "" {
				st = "runnable"
			}
			_, _ = fmt.Fprintf(out, "%-11s %s:%d:%d %s %s->%s  %s\n", st, m.File, m.Line, m.Col, m.Kind, m.From, orRemoved(m.To), m.Source)
		}
		_, _ = fmt.Fprintf(out, "\n%d mutants, %d not covered\n", len(rep.Mutants), rep.NotCovered)
		return
	}
	var lived []*mutant
	for _, m := range rep.Mutants {
		if m.Status == statusLived {
			lived = append(lived, m)
		}
	}
	if len(lived) > 0 {
		_, _ = fmt.Fprintf(out, "Survivors (tests still pass with this change):\n")
		for _, m := range lived {
			_, _ = fmt.Fprintf(out, "  %s:%d:%d %s %s->%s\n      %s\n", m.File, m.Line, m.Col, m.Kind, m.From, orRemoved(m.To), m.Source)
		}
		_, _ = fmt.Fprintln(out)
	}
	_, _ = fmt.Fprintf(out, "Killed: %d, Lived: %d, Timed out: %d, Not covered: %d, Not viable: %d, Build errors: %d\n",
		rep.Killed, rep.Lived, rep.TimedOut, rep.NotCovered, rep.NotViable, rep.BuildError)
	_, _ = fmt.Fprintf(out, "Test efficacy: %.2f%%  Mutant coverage: %.2f%%  (%s, %s)\n", rep.Efficacy, rep.MCoverage, rep.Package, rep.Elapsed)
}

func tail(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
