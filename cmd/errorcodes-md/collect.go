package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// sourceRoots are the trees whose non-test Go files build problems.
var sourceRoots = []string{"pkg", "cmd"}

// part is one piece of a string expression: a literal, or a reference to a
// constant resolved once every file has been read.
type part struct {
	lit  string
	ref  bool
	pkg  string // import path of the constant's package
	name string
}

// strExpr is a concatenation of parts; nil means the expression is not a
// constant string (a call, a variable, a format).
type strExpr []part

// site is one place a problem is built: an api.NewProblem call or an
// api.Problem literal, with the WithHint and WithDocs calls chained onto it.
type site struct {
	code   strExpr
	status int
	title  strExpr
	hints  []strExpr
	docs   []strExpr
}

// helper is a function that builds a problem from its own parameters, such
// as func bindingPromotionProblem(code, title, hint string, …). Each call
// with a constant code becomes a site.
type helper struct {
	codeArg  int
	titleArg int // -1 when the title is fixed
	hintArg  int // -1 when the hint is fixed or absent
	site     site
}

// call is a call that passes a code-shaped argument, kept until every helper
// is known.
type call struct {
	pkg, name string
	args      []strExpr
}

// collection is what a walk of the source trees found.
type collection struct {
	module  string
	consts  map[string]map[string]strExpr // import path → name → value
	sites   []*site
	helpers map[string]map[string]helper // import path → function → helper
	calls   []call
	// apiCodes are the Code* constants of pkg/api: every code the API
	// declares, whether or not a call site could be resolved.
	apiCodes []string
}

func (c *collection) apiPath() string { return c.module + "/pkg/api" }

// collect parses the non-test Go files under root's source trees that
// declare constants or build problems.
func collect(root string) (*collection, error) {
	module, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	c := &collection{module: module, consts: map[string]map[string]strExpr{}, helpers: map[string]map[string]helper{}}
	for _, tree := range sourceRoots {
		err := filepath.WalkDir(filepath.Join(root, tree), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if !bytes.Contains(src, []byte("const")) && !bytes.Contains(src, []byte("Problem")) {
				return nil
			}
			rel, err := filepath.Rel(root, filepath.Dir(p))
			if err != nil {
				return err
			}
			return c.file(p, module+"/"+filepath.ToSlash(rel), src)
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", tree, err)
		}
	}
	c.applyHelpers()
	return c, nil
}

// applyHelpers turns each call to a helper into a site.
func (c *collection) applyHelpers() {
	for _, cl := range c.calls {
		h, ok := c.helpers[cl.pkg][cl.name]
		if !ok || h.codeArg >= len(cl.args) || cl.args[h.codeArg] == nil {
			continue
		}
		s := h.site
		s.code = cl.args[h.codeArg]
		if h.titleArg >= 0 && h.titleArg < len(cl.args) {
			s.title = cl.args[h.titleArg]
		}
		if h.hintArg >= 0 && h.hintArg < len(cl.args) {
			s.hints = append(append([]strExpr{}, s.hints...), cl.args[h.hintArg])
		}
		c.sites = append(c.sites, &s)
	}
	c.calls = nil
}

func modulePath(goMod string) (string, error) {
	b, err := os.ReadFile(goMod)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", fmt.Errorf("%s: no module line", goMod)
}

// fileScope resolves names as one file sees them.
type fileScope struct {
	c       *collection
	pkg     string            // the file's import path
	imports map[string]string // local name → import path
}

func (c *collection) file(name, pkg string, src []byte) error {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	sc := fileScope{c: c, pkg: pkg, imports: map[string]string{}}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path.Base(p)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		sc.imports[local] = p
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if v := sc.str(vs.Values[i]); v != nil {
					if c.consts[pkg] == nil {
						c.consts[pkg] = map[string]strExpr{}
					}
					c.consts[pkg][n.Name] = v
					if pkg == c.apiPath() && strings.HasPrefix(n.Name, "Code") && n.IsExported() {
						c.apiCodes = append(c.apiCodes, n.Name)
					}
				}
			}
		}
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Body != nil {
			sc.helper(fd)
		}
	}
	sites := map[ast.Node]*site{}
	get := func(n ast.Node) *site {
		s := sites[n]
		if s == nil {
			s = &site{}
			sites[n] = s
			c.sites = append(c.sites, s)
		}
		return s
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sc.isNewProblem(n) && len(n.Args) == 4 {
				s := get(n)
				s.status = sc.status(n.Args[0])
				s.code = sc.str(n.Args[1])
				s.title = sc.str(n.Args[2])
				return true
			}
			sc.recordCall(n)
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || len(n.Args) != 1 || (sel.Sel.Name != "WithHint" && sel.Sel.Name != "WithDocs") {
				return true
			}
			root := sc.chainRoot(sel.X)
			if root == nil {
				return true
			}
			s := get(root)
			if sel.Sel.Name == "WithHint" {
				s.hints = append(s.hints, sc.str(n.Args[0]))
			} else {
				s.docs = append(s.docs, sc.str(n.Args[0]))
			}
		case *ast.CompositeLit:
			if !sc.isProblemType(n.Type) {
				return true
			}
			s := get(n)
			for _, elt := range n.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Status":
					s.status = sc.status(kv.Value)
				case "Code":
					s.code = sc.str(kv.Value)
				case "Title":
					s.title = sc.str(kv.Value)
				case "Hint":
					s.hints = append(s.hints, sc.str(kv.Value))
				case "DocsURL", "Type":
					s.docs = append(s.docs, sc.str(kv.Value))
				}
			}
		}
		return true
	})
	return nil
}

// helper records fd when it builds a problem whose code is one of its
// parameters.
func (sc fileScope) helper(fd *ast.FuncDecl) {
	params := map[string]int{}
	i := 0
	for _, field := range fd.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, n := range field.Names {
			params[n.Name] = i
			i++
		}
	}
	paramOf := func(e ast.Expr) int {
		if id, ok := e.(*ast.Ident); ok {
			if i, ok := params[id.Name]; ok {
				return i
			}
		}
		return -1
	}
	var found *helper
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			if sc.isNewProblem(n) && len(n.Args) == 4 {
				if ci := paramOf(n.Args[1]); ci >= 0 {
					found = &helper{codeArg: ci, titleArg: paramOf(n.Args[2]), hintArg: -1}
					found.site.status = sc.status(n.Args[0])
					if found.titleArg < 0 {
						found.site.title = sc.str(n.Args[2])
					}
				}
				return true
			}
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || len(n.Args) != 1 || sel.Sel.Name != "WithHint" {
				return true
			}
			if root, ok := sc.chainRoot(sel.X).(*ast.CallExpr); ok && sc.isNewProblem(root) && len(root.Args) == 4 {
				if ci := paramOf(root.Args[1]); ci >= 0 {
					found = &helper{codeArg: ci, titleArg: paramOf(root.Args[2]), hintArg: paramOf(n.Args[0])}
					found.site.status = sc.status(root.Args[0])
					if found.titleArg < 0 {
						found.site.title = sc.str(root.Args[2])
					}
					if found.hintArg < 0 {
						found.site.hints = []strExpr{sc.str(n.Args[0])}
					}
					return false
				}
			}
		}
		return true
	})
	if found == nil {
		return
	}
	if sc.c.helpers[sc.pkg] == nil {
		sc.c.helpers[sc.pkg] = map[string]helper{}
	}
	sc.c.helpers[sc.pkg][fd.Name.Name] = *found
}

// recordCall keeps a call that passes a code-shaped argument (a snake_case
// literal or a Code* constant), in case its callee is a helper.
func (sc fileScope) recordCall(n *ast.CallExpr) {
	var pkg, name string
	switch fun := n.Fun.(type) {
	case *ast.Ident:
		pkg, name = sc.pkg, fun.Name
	case *ast.SelectorExpr:
		id, ok := fun.X.(*ast.Ident)
		if !ok || sc.imports[id.Name] == "" {
			return
		}
		pkg, name = sc.imports[id.Name], fun.Sel.Name
	default:
		return
	}
	codeLike := false
	args := make([]strExpr, len(n.Args))
	for i, a := range n.Args {
		args[i] = sc.str(a)
		switch a := a.(type) {
		case *ast.BasicLit:
			if v, err := strconv.Unquote(a.Value); err == nil && strings.Contains(v, "_") && codeShape(v) {
				codeLike = true
			}
		case *ast.Ident:
			codeLike = codeLike || strings.HasPrefix(a.Name, "Code")
		case *ast.SelectorExpr:
			codeLike = codeLike || strings.HasPrefix(a.Sel.Name, "Code")
		}
	}
	if codeLike {
		sc.c.calls = append(sc.c.calls, call{pkg: pkg, name: name, args: args})
	}
}

func codeShape(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// isAPI reports whether x names the api package from this file.
func (sc fileScope) isAPI(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && sc.imports[id.Name] == sc.c.apiPath()
}

func (sc fileScope) isNewProblem(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "NewProblem" && sc.pkg == sc.c.apiPath()
	case *ast.SelectorExpr:
		return fun.Sel.Name == "NewProblem" && sc.isAPI(fun.X)
	}
	return false
}

func (sc fileScope) isProblemType(t ast.Expr) bool {
	switch t := t.(type) {
	case *ast.Ident:
		return t.Name == "Problem" && sc.pkg == sc.c.apiPath()
	case *ast.SelectorExpr:
		return t.Sel.Name == "Problem" && sc.isAPI(t.X)
	}
	return false
}

// chainRoot walks down a method chain (x.WithA(…).WithB(…)) to the problem it
// decorates, or returns nil when the chain starts anywhere else.
func (sc fileScope) chainRoot(x ast.Expr) ast.Node {
	for {
		switch n := x.(type) {
		case *ast.ParenExpr:
			x = n.X
		case *ast.UnaryExpr:
			x = n.X
		case *ast.CompositeLit:
			if sc.isProblemType(n.Type) {
				return n
			}
			return nil
		case *ast.CallExpr:
			if sc.isNewProblem(n) {
				return n
			}
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return nil
			}
			x = sel.X
		default:
			return nil
		}
	}
}

// str turns a constant string expression into parts, or nil.
func (sc fileScope) str(e ast.Expr) strExpr {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return nil
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			return nil
		}
		return strExpr{{lit: v}}
	case *ast.Ident:
		return strExpr{{ref: true, pkg: sc.pkg, name: e.Name}}
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		if !ok || sc.imports[id.Name] == "" {
			return nil
		}
		return strExpr{{ref: true, pkg: sc.imports[id.Name], name: e.Sel.Name}}
	case *ast.ParenExpr:
		return sc.str(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return nil
		}
		l, r := sc.str(e.X), sc.str(e.Y)
		if l == nil || r == nil {
			return nil
		}
		return append(append(strExpr{}, l...), r...)
	}
	return nil
}

// status reads an HTTP status: a literal or a net/http Status constant.
func (sc fileScope) status(e ast.Expr) int {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT {
			if n, err := strconv.Atoi(e.Value); err == nil {
				return n
			}
		}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok && sc.imports[id.Name] == "net/http" {
			return httpStatus[e.Sel.Name]
		}
	}
	return 0
}

// resolve evaluates a string expression against the collected constants.
func (c *collection) resolve(e strExpr) (string, bool) {
	return c.resolveDepth(e, 0)
}

func (c *collection) resolveDepth(e strExpr, depth int) (string, bool) {
	if e == nil || depth > 8 {
		return "", false
	}
	var b strings.Builder
	for _, p := range e {
		if !p.ref {
			b.WriteString(p.lit)
			continue
		}
		v, ok := c.consts[p.pkg][p.name]
		if !ok {
			return "", false
		}
		s, ok := c.resolveDepth(v, depth+1)
		if !ok {
			return "", false
		}
		b.WriteString(s)
	}
	return b.String(), true
}

// httpStatus maps net/http's Status constant names to their codes.
var httpStatus = map[string]int{
	"StatusContinue": 100, "StatusSwitchingProtocols": 101, "StatusProcessing": 102, "StatusEarlyHints": 103,
	"StatusOK": 200, "StatusCreated": 201, "StatusAccepted": 202, "StatusNonAuthoritativeInfo": 203,
	"StatusNoContent": 204, "StatusResetContent": 205, "StatusPartialContent": 206, "StatusMultiStatus": 207,
	"StatusAlreadyReported": 208, "StatusIMUsed": 226,
	"StatusMultipleChoices": 300, "StatusMovedPermanently": 301, "StatusFound": 302, "StatusSeeOther": 303,
	"StatusNotModified": 304, "StatusUseProxy": 305, "StatusTemporaryRedirect": 307, "StatusPermanentRedirect": 308,
	"StatusBadRequest": 400, "StatusUnauthorized": 401, "StatusPaymentRequired": 402, "StatusForbidden": 403,
	"StatusNotFound": 404, "StatusMethodNotAllowed": 405, "StatusNotAcceptable": 406, "StatusProxyAuthRequired": 407,
	"StatusRequestTimeout": 408, "StatusConflict": 409, "StatusGone": 410, "StatusLengthRequired": 411,
	"StatusPreconditionFailed": 412, "StatusRequestEntityTooLarge": 413, "StatusRequestURITooLong": 414,
	"StatusUnsupportedMediaType": 415, "StatusRequestedRangeNotSatisfiable": 416, "StatusExpectationFailed": 417,
	"StatusTeapot": 418, "StatusMisdirectedRequest": 421, "StatusUnprocessableEntity": 422, "StatusLocked": 423,
	"StatusFailedDependency": 424, "StatusTooEarly": 425, "StatusUpgradeRequired": 426, "StatusPreconditionRequired": 428,
	"StatusTooManyRequests": 429, "StatusRequestHeaderFieldsTooLarge": 431, "StatusUnavailableForLegalReasons": 451,
	"StatusInternalServerError": 500, "StatusNotImplemented": 501, "StatusBadGateway": 502,
	"StatusServiceUnavailable": 503, "StatusGatewayTimeout": 504, "StatusHTTPVersionNotSupported": 505,
	"StatusVariantAlsoNegotiates": 506, "StatusInsufficientStorage": 507, "StatusLoopDetected": 508,
	"StatusNotExtended": 510, "StatusNetworkAuthenticationRequired": 511,
}
