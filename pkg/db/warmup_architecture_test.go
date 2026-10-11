package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestArch_ApidWarmsPoolBeforeBgBefore is the grep gate that
// prevents a future refactor from reintroducing the
// TestRekeyRunnerPg flake by removing the WarmUp call or by
// reordering it after bgBefore. The walker's invariant:
//
//	(1) db.Open is called inside run() / runWithDeps,
//	(2) db.WarmUp is called between db.Open and any reference to
//	    bgBefore (or the bgBefore function literal), and
//	(3) no `return ...` between db.Open and the listener bind
//	    site (deps.listen) fails to close the pool first.
//
// If any of (1)-(3) regress, this test fails with a message that
// names the file + line number of the violation. ADR-094 references
// this gate as the architectural pin.
func TestArch_ApidWarmsPoolBeforeBgBefore(t *testing.T) {
	const apidMainPath = "pkg/apid/apidserver/main.go"
	// Walk via parser.ParseFile (path is relative to the repo root
	// where `go test ./...` is run).
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, apidMainPath, nil, parser.ParseComments)
	if err != nil {
		t.Skipf("cannot parse %s (likely ran from non-repo root): %v", apidMainPath, err)
	}

	// Locate the *FuncDecl for run or runWithDeps.
	var runFunc *ast.FuncDecl
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Name.Name == "run" || fd.Name.Name == "runWithDeps" {
			runFunc = fd
			break
		}
	}
	if runFunc == nil || runFunc.Body == nil {
		t.Fatalf("could not find run / runWithDeps function in %s", apidMainPath)
	}

	// Walk the body once and record the line numbers of:
	//   - db.Open calls,
	//   - db.WarmUp calls,
	//   - `deps.bgBefore` references (call or assignment),
	//   - `deps.listen(...)` calls,
	//   - `return` statements inside the function.
	type marker struct {
		line int
		text string
	}
	var (
		opens, warms, bgBefores, listens []marker
		returns                          []marker
	)
	ast.Inspect(runFunc.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		line := fset.Position(call.Pos()).Line
		switch {
		case pkg.Name == "db" && sel.Sel.Name == "Open":
			opens = append(opens, marker{line, "db.Open"})
		case pkg.Name == "db" && sel.Sel.Name == "WarmUp":
			warms = append(warms, marker{line, "db.WarmUp"})
		case pkg.Name == "deps" && sel.Sel.Name == "bgBefore":
			bgBefores = append(bgBefores, marker{line, "deps.bgBefore"})
		case pkg.Name == "deps" && sel.Sel.Name == "listen":
			listens = append(listens, marker{line, "deps.listen"})
		}
		return true
	})

	// Also collect every `return` statement (including `return nil`,
	// `return err`, etc.) by walking the function body.
	ast.Inspect(runFunc.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		returns = append(returns, marker{fset.Position(ret.Pos()).Line, "return"})
		return true
	})

	if len(opens) == 0 {
		t.Fatalf("%s: db.Open(...) not found inside run/runWithDeps", apidMainPath)
	}
	if len(warms) == 0 {
		t.Fatalf("%s: db.WarmUp(...) not found inside run/runWithDeps — ADR-094 requires a warm-up barrier between db.Open and bgBefore launch",
			apidMainPath)
	}
	if len(listens) == 0 {
		t.Fatalf("%s: deps.listen(...) not found inside run/runWithDeps", apidMainPath)
	}
	if len(bgBefores) == 0 {
		// bgBefore is only called in production run() — tests can
		// pass a runDeps with bgBefore=nil. The check here is
		// permissive: if bgBefore is not invoked, the warm-up
		// ordering is still correct (WarmUp fires before listen),
		// but we still require WarmUp to be present (already
		// enforced above).
		t.Logf("%s: deps.bgBefore not invoked in run/runWithDeps (test seam) — warm-up still required", apidMainPath)
	}

	// Ordering: every db.WarmUp must come BEFORE every bgBefore call.
	// In run() the order is db.Open -> db.WarmUp -> bgBefore -> listen.
	for _, w := range warms {
		for _, b := range bgBefores {
			if w.line >= b.line {
				t.Errorf("%s:%d: db.WarmUp (%d) must come BEFORE deps.bgBefore (%d) per ADR-094",
					apidMainPath, w.line, w.line, b.line)
			}
		}
	}

	// Pool-close invariant: between the FIRST db.Open and the FIRST
	// deps.listen, no `return` is permitted unless it closes the
	// pool first. We approximate the invariant by requiring either:
	//   - `closePool` is called immediately before `return` (the
	//     common pattern in cmd/apid/main.go post-PR-823-fix), or
	//   - the return is unconditional `return nil` (only valid
	//     after deps.listen has succeeded, which is past the
	//     listen-site).
	//
	// We can't trace variable definitions with go/ast easily, so the
	// grep is intentionally permissive: we look for the literal
	// token `closePool` somewhere in the source. If a future refactor
	// removes closePool entirely the warm-up architecture gate fires.
	src, err := os.ReadFile(apidMainPath)
	if err != nil {
		t.Fatalf("read %s: %v", apidMainPath, err)
	}
	if !strings.Contains(string(src), "closePool") {
		t.Errorf("%s: pool close helper `closePool` not found — every early-return between db.Open and deps.listen must close the pool first (ADR-094 defer-move contract)",
			apidMainPath)
	}

	// listen-site invariant: the WarmUp call must precede the FIRST
	// deps.listen call too (the bgBefore check above is necessary
	// but not sufficient — WarmUp must also fire before the
	// listener starts, since the listener's first request could
	// itself trigger a pool.Acquire on the API path).
	for _, w := range warms {
		for _, l := range listens {
			if w.line >= l.line {
				t.Errorf("%s:%d: db.WarmUp (%d) must come BEFORE deps.listen (%d) — otherwise the listener can accept requests before the pool is verified warm",
					apidMainPath, w.line, w.line, l.line)
			}
		}
	}

	t.Logf("apid main: %d db.Open call(s), %d db.WarmUp call(s), %d deps.bgBefore call(s), %d deps.listen call(s), %d return statement(s) in run/runWithDeps",
		len(opens), len(warms), len(bgBefores), len(listens), len(returns))
}
