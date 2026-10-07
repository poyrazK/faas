package routeimpact

import (
	"errors"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const goGinImportPath = "github.com/gin-gonic/gin"

type goGinContext struct {
	identity             string
	prefix               string
	prefixKnown          bool
	middleware           []string
	middlewareUnresolved bool
}

type goGinIssue struct {
	code    string
	message string
	pos     token.Pos
}

func goIsGinImport(importPath string) bool {
	return importPath == goGinImportPath
}

func goGinTypeKind(file *goRouteFile, expr ast.Expr) string {
	for {
		switch value := expr.(type) {
		case *ast.ParenExpr:
			expr = value.X
		case *ast.StarExpr:
			expr = value.X
		default:
			selector, ok := expr.(*ast.SelectorExpr)
			if !ok {
				return ""
			}
			alias, ok := selector.X.(*ast.Ident)
			if !ok || !goIsGinImport(file.imports[alias.Name]) {
				return ""
			}
			switch selector.Sel.Name {
			case "Engine":
				return "engine"
			case "RouterGroup", "IRouter", "IRoutes", "IQueryRoutes":
				return "group"
			default:
				return ""
			}
		}
	}
}

func goGinTypeContext(kind string) (goGinContext, bool) {
	switch kind {
	case "engine":
		return goGinContext{prefixKnown: true, middleware: []string{}}, true
	case "group":
		return goGinContext{prefixKnown: false, middleware: []string{}}, true
	default:
		return goGinContext{}, false
	}
}

func goGinConstructor(file *goRouteFile, expr ast.Expr, shadowed map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "New" && selector.Sel.Name != "Default") {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && !shadowed[alias.Name] && goIsGinImport(file.imports[alias.Name])
}

func goGinStaticExpression(file *goRouteFile, expr ast.Expr, known map[string]bool, shadowed map[string]bool) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goGinStaticExpression(file, value.X, known, shadowed)
	case *ast.UnaryExpr:
		return (value.Op == token.AND || value.Op == token.MUL) && goGinStaticExpression(file, value.X, known, shadowed)
	case *ast.Ident:
		return !shadowed[value.Name] && known[value.Name]
	case *ast.CallExpr:
		if goGinConstructor(file, value, shadowed) {
			return true
		}
		if goGinFactoryReturnKind(file, value) != "" {
			return true
		}
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if selector.Sel.Name != "Group" && selector.Sel.Name != "Use" {
			return false
		}
		return goGinStaticExpression(file, selector.X, known, shadowed)
	default:
		return false
	}
}

func goGinFactoryReturnKind(file *goRouteFile, call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return file.ginFactories[file.packagePath][function.Name]
	case *ast.SelectorExpr:
		alias, ok := function.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return file.ginFactories[file.imports[alias.Name]][function.Sel.Name]
	default:
		return ""
	}
}

func goGinIndexFactoryKinds(files []*goRouteFile) map[string]map[string]string {
	factories := map[string]map[string]string{}
	for _, file := range files {
		if factories[file.packagePath] == nil {
			factories[file.packagePath] = map[string]string{}
		}
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name == nil || function.Type.Results == nil || len(function.Type.Results.List) != 1 || len(function.Type.Results.List[0].Names) > 1 {
				continue
			}
			kind := goGinTypeKind(file, function.Type.Results.List[0].Type)
			if kind == "" {
				continue
			}
			if previous := factories[file.packagePath][function.Name.Name]; previous != "" && previous != kind {
				delete(factories[file.packagePath], function.Name.Name)
				continue
			}
			factories[file.packagePath][function.Name.Name] = kind
		}
	}
	return factories
}

func goGinResolveHandlers(index *sourceIndex, file *goRouteFile, args []ast.Expr, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, shadowed map[string]bool) ([]string, bool) {
	symbols := []string{}
	unresolved := false
	for _, expression := range args {
		if len(symbols) > api.RouteImpactMaxSymbols {
			return symbols, true
		}
		symbol, _, _, ok := goGinResolveHandler(file, expression, packageFuncs, methods, modulePath, index.Symbols, shadowed)
		if !ok {
			unresolved = true
			continue
		}
		symbols = append(symbols, symbol)
	}
	return symbols, unresolved
}

func goGinResolveHandler(file *goRouteFile, expr ast.Expr, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, symbols map[string]functionSymbol, shadowed map[string]bool) (symbol, display string, source Location, ok bool) {
	if symbol, display, source, ok = goResolveHandler(file, expr, packageFuncs, methods, modulePath, symbols, shadowed); ok {
		return symbol, display, source, true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", "unresolved handler", Location{}, false
	}
	// Middleware factories commonly return a Gin HandlerFunc. Tracking their
	// local function body links later changes without executing customer code.
	symbol, display, source, ok = goResolveHandler(file, call.Fun, packageFuncs, methods, modulePath, symbols, shadowed)
	if !ok {
		return "", "unresolved handler", Location{}, false
	}
	return symbol, display + "()", source, true
}

func goGinJoinMiddleware(groups ...[]string) []string {
	joined := []string{}
	for _, group := range groups {
		remaining := api.RouteImpactMaxSymbols + 1 - len(joined)
		if remaining <= 0 {
			return joined
		}
		if len(group) > remaining {
			return append(joined, group[:remaining]...)
		}
		joined = append(joined, group...)
	}
	return joined
}

func goGinResolveExpression(file *goRouteFile, expr ast.Expr, contexts map[string]goGinContext, index *sourceIndex,
	packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, shadowed map[string]bool,
) (goGinContext, bool, []goGinIssue) {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goGinResolveExpression(file, value.X, contexts, index, packageFuncs, methods, modulePath, shadowed)
	case *ast.UnaryExpr:
		if value.Op == token.AND || value.Op == token.MUL {
			return goGinResolveExpression(file, value.X, contexts, index, packageFuncs, methods, modulePath, shadowed)
		}
	case *ast.Ident:
		if context, ok := contexts[value.Name]; ok {
			return context, true, nil
		}
	case *ast.CallExpr:
		if goGinConstructor(file, value, shadowed) {
			return goGinContext{prefixKnown: true, middleware: []string{}}, true, nil
		}
		if kind := goGinFactoryReturnKind(file, value); kind != "" {
			context, _ := goGinTypeContext(kind)
			context.middlewareUnresolved = true
			return context, true, []goGinIssue{{code: "dynamic_gin_router_factory", message: "A local Gin router factory's registrations and middleware are not expanded into the route model.", pos: value.Pos()}}
		}
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}
		base, recognized, issues := goGinResolveExpression(file, selector.X, contexts, index, packageFuncs, methods, modulePath, shadowed)
		if !recognized {
			return goGinContext{}, false, nil
		}
		switch selector.Sel.Name {
		case "Group":
			child := base
			child.identity = ""
			if len(value.Args) == 0 {
				child.prefixKnown = false
				issues = append(issues, goGinIssue{code: "invalid_gin_route_group", message: "A Gin route group does not include a literal prefix.", pos: value.Pos()})
				return child, true, issues
			}
			prefix, literal := goStringLiteral(value.Args[0])
			if !literal {
				child.prefixKnown = false
				issues = append(issues, goGinIssue{code: "dynamic_gin_route_prefix", message: "A dynamic Gin route-group prefix prevents nested paths from being mapped to stable routes.", pos: value.Pos()})
			} else if base.prefixKnown {
				joined, valid := goChiJoinPath(base.prefix, prefix, true)
				if !valid {
					child.prefixKnown = false
					issues = append(issues, goGinIssue{code: "unsupported_gin_route_prefix", message: "A Gin route-group prefix is outside the supported static path model.", pos: value.Pos()})
				} else {
					child.prefix = joined
				}
			} else {
				child.prefixKnown = false
			}
			if len(value.Args) > 1 {
				symbols, unresolved := goGinResolveHandlers(index, file, value.Args[1:], packageFuncs, methods, modulePath, shadowed)
				child.middleware = goGinJoinMiddleware(base.middleware, symbols)
				child.middlewareUnresolved = base.middlewareUnresolved || unresolved
				if unresolved {
					issues = append(issues, goGinIssue{code: "dynamic_gin_middleware", message: "A Gin route-group handler does not resolve to a local function; route impact may miss middleware behavior.", pos: value.Pos()})
				}
			}
			return child, true, issues
		case "Use":
			symbols, unresolved := goGinResolveHandlers(index, file, value.Args, packageFuncs, methods, modulePath, shadowed)
			base.middleware = goGinJoinMiddleware(base.middleware, symbols)
			base.middlewareUnresolved = base.middlewareUnresolved || unresolved
			if unresolved {
				issues = append(issues, goGinIssue{code: "dynamic_gin_middleware", message: "A Gin middleware does not resolve to a local function; route impact may miss middleware behavior.", pos: value.Pos()})
			}
			return base, true, issues
		}
	}
	return goGinContext{}, false, nil
}

func goGinPackageContexts(index *sourceIndex, files []*goRouteFile, packageFuncs map[string]map[string]string, methodsByPackage map[string]map[string][]string, modulePath string) map[string]map[string]goGinContext {
	contexts := map[string]map[string]goGinContext{}
	seenIssues := map[string]bool{}
	for _, file := range files {
		if contexts[file.packagePath] == nil {
			contexts[file.packagePath] = map[string]goGinContext{}
		}
	}
	for iteration := 0; iteration < api.RouteImpactMaxGraphDepth; iteration++ {
		changed := false
		for _, file := range files {
			for _, declaration := range file.file.Decls {
				gen, ok := declaration.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, specNode := range gen.Specs {
					spec, ok := specNode.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range spec.Names {
						var value ast.Expr
						if len(spec.Values) == len(spec.Names) {
							value = spec.Values[i]
						} else if len(spec.Names) == 1 && len(spec.Values) == 1 {
							value = spec.Values[0]
						}
						context, recognized := goGinTypeContext(goGinTypeKind(file, spec.Type))
						if value != nil {
							resolved, ok, issues := goGinResolveExpression(file, value, contexts[file.packagePath], index, packageFuncs, methodsByPackage[file.packagePath], modulePath, nil)
							for _, issue := range issues {
								goGinAddIssue(index, file, issue, seenIssues)
							}
							if ok {
								context, recognized = resolved, true
							}
						}
						if !recognized {
							continue
						}
						if context.identity == "" {
							context.identity = goGinContextID(file, spec.Pos(), name.Name)
						}
						old, exists := contexts[file.packagePath][name.Name]
						if !exists || !goGinSameContext(old, context) {
							contexts[file.packagePath][name.Name] = context
							changed = true
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	return contexts
}

func goGinSameContext(left, right goGinContext) bool {
	return left.identity == right.identity && left.prefix == right.prefix && left.prefixKnown == right.prefixKnown &&
		left.middlewareUnresolved == right.middlewareUnresolved && sameStringSequence(left.middleware, right.middleware)
}

func goGinContextID(file *goRouteFile, position token.Pos, name string) string {
	return "gingroup:" + file.path + ":" + strconv.Itoa(file.fset.Position(position).Offset) + ":" + name
}

func goGinUpdateAliases(contexts map[string]goGinContext, context goGinContext) {
	if context.identity == "" {
		return
	}
	for name, current := range contexts {
		if current.identity == context.identity {
			contexts[name] = context
		}
	}
}

func goGinFunctionBindings(function *ast.FuncDecl) map[string]bool {
	bindings := map[string]bool{}
	goAddFieldNames(bindings, function.Type.Params)
	goAddFieldNames(bindings, function.Type.Results)
	goAddFieldNames(bindings, function.Recv)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				goAddAssignedName(bindings, target)
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				bindings[name.Name] = true
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				goAddAssignedName(bindings, value.Key)
				goAddAssignedName(bindings, value.Value)
			}
		}
		return true
	})
	return bindings
}

func goGinLocalState(file *goRouteFile, function *ast.FuncDecl, packageContexts map[string]goGinContext) (map[string]goGinContext, map[string]bool, map[string]bool) {
	bindings := goGinFunctionBindings(function)
	contexts := map[string]goGinContext{}
	known := map[string]bool{}
	for name, context := range packageContexts {
		if !bindings[name] {
			contexts[name] = context
			known[name] = true
		}
	}
	if function.Type.Params != nil {
		for _, field := range function.Type.Params.List {
			context, ok := goGinTypeContext(goGinTypeKind(file, field.Type))
			if !ok {
				continue
			}
			for _, name := range field.Names {
				context.identity = goGinContextID(file, function.Pos(), name.Name)
				contexts[name.Name] = context
				known[name.Name] = true
			}
		}
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if value, ok := node.(*ast.ValueSpec); ok {
			context, isGin := goGinTypeContext(goGinTypeKind(file, value.Type))
			if isGin {
				for _, name := range value.Names {
					if !known[name.Name] {
						context.identity = goGinContextID(file, value.Pos(), name.Name)
						contexts[name.Name] = context
						goGinUpdateAliases(contexts, context)
						known[name.Name] = true
					}
				}
			}
		}
		return true
	})
	for iteration := 0; iteration < api.RouteImpactMaxGraphDepth; iteration++ {
		changed := false
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			var left, right []ast.Expr
			switch value := node.(type) {
			case *ast.AssignStmt:
				left, right = value.Lhs, value.Rhs
			case *ast.ValueSpec:
				for _, name := range value.Names {
					left = append(left, name)
				}
				right = value.Values
			default:
				return true
			}
			for i, target := range left {
				name, ok := target.(*ast.Ident)
				if !ok || name.Name == "_" {
					continue
				}
				var expression ast.Expr
				if len(left) == len(right) {
					expression = right[i]
				} else if len(left) == 1 && len(right) == 1 {
					expression = right[0]
				}
				if expression != nil && goGinStaticExpression(file, expression, known, nil) && !known[name.Name] {
					known[name.Name] = true
					changed = true
				}
			}
			return true
		})
		if !changed {
			break
		}
	}
	return contexts, known, bindings
}

func goGinBaseName(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goGinBaseName(value.X)
	case *ast.Ident:
		return value.Name, true
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Group" || selector.Sel.Name == "Use") {
			return goGinBaseName(selector.X)
		}
	}
	return "", false
}

func goGinMethods() []string {
	return []string{"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"}
}

func goGinRegistration(name string, args []ast.Expr, file *goRouteFile, shadowed map[string]bool) (methods []string, pathArg, handlerStart int, static bool, catchAll bool, valid bool, issues []goGinIssue) {
	switch name {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "QUERY":
		method := name
		if method == "QUERY" {
			issues = append(issues, goGinIssue{code: "unsupported_gin_method", message: "Gin QUERY routes are not represented by Gregale route telemetry methods.", pos: token.NoPos})
			return nil, 0, 0, false, false, false, issues
		}
		methods, pathArg, handlerStart = []string{method}, 0, 1
	case "Handle":
		if len(args) < 3 {
			return nil, 0, 0, false, false, false, nil
		}
		method, literal := goStringLiteral(args[0])
		if !literal {
			method, literal = goChiMethodValue(file, args[0], shadowed)
		}
		if !literal || !validMethod(method) {
			return nil, 0, 0, false, false, false, []goGinIssue{{code: "unsupported_gin_method", message: "A Gin Handle method is dynamic or is outside the route telemetry method set.", pos: token.NoPos}}
		}
		methods, pathArg, handlerStart = []string{method}, 1, 2
	case "Any":
		methods, pathArg, handlerStart = goGinMethods(), 0, 1
	case "Match":
		if len(args) < 3 {
			return nil, 0, 0, false, false, false, nil
		}
		methodExpr, ok := args[0].(*ast.CompositeLit)
		if !ok {
			return nil, 0, 0, false, false, false, []goGinIssue{{code: "dynamic_gin_route_methods", message: "A Gin Match method list must be a literal string slice to map all registered methods.", pos: token.NoPos}}
		}
		for _, element := range methodExpr.Elts {
			method, literal := goStringLiteral(element)
			if !literal {
				method, literal = goChiMethodValue(file, element, shadowed)
			}
			if !literal || !validMethod(method) {
				issues = append(issues, goGinIssue{code: "unsupported_gin_method", message: "A Gin Match method is dynamic or is outside the route telemetry method set.", pos: token.NoPos})
				continue
			}
			methods = append(methods, method)
		}
		methodSet := map[string]bool{}
		for _, method := range methods {
			methodSet[method] = true
		}
		methods = methods[:0]
		for method := range methodSet {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		pathArg, handlerStart = 1, 2
	case "StaticFile", "StaticFileFS":
		if len(args) < 2 {
			return nil, 0, 0, false, false, false, nil
		}
		methods, pathArg, static = []string{"GET", "HEAD"}, 0, true
		if name == "StaticFileFS" && len(args) < 3 {
			return nil, 0, 0, false, false, false, nil
		}
	case "Static", "StaticFS":
		if len(args) < 2 {
			return nil, 0, 0, false, false, false, nil
		}
		methods, pathArg, static, catchAll = []string{"GET", "HEAD"}, 0, true, true
	}
	if len(methods) == 0 || pathArg >= len(args) || (!static && handlerStart >= len(args)) {
		return methods, pathArg, handlerStart, static, catchAll, false, issues
	}
	return methods, pathArg, handlerStart, static, catchAll, true, issues
}

func goGinPath(prefix, routePath string) (string, bool, bool) {
	if !strings.HasPrefix(routePath, "/") || !validMetadata(prefix) || !validMetadata(routePath) || strings.ContainsAny(prefix+routePath, "?#\\") {
		return "", false, false
	}
	joined := path.Join("/", prefix, routePath)
	if strings.HasSuffix(routePath, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	segments := strings.Split(strings.TrimPrefix(joined, "/"), "/")
	params := map[string]bool{}
	wildcard := false
	for i, segment := range segments {
		if segment == "." || segment == ".." {
			return "", false, false
		}
		if strings.HasPrefix(segment, ":") {
			name := strings.TrimPrefix(segment, ":")
			if !goGinIdentifier(name) || params[name] {
				return "", false, false
			}
			params[name] = true
			segments[i] = "{" + name + "}"
			continue
		}
		if strings.HasPrefix(segment, "*") {
			name := strings.TrimPrefix(segment, "*")
			if !goGinIdentifier(name) || i != len(segments)-1 || params[name] {
				return "", false, false
			}
			wildcard, params[name] = true, true
			continue
		}
		if strings.ContainsAny(segment, ":*") {
			return "", false, false
		}
	}
	return "/" + strings.Join(segments, "/"), true, wildcard
}

func goGinIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, char := range value {
		if char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (i == 0 || char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func goGinCallName(selector *ast.SelectorExpr) bool {
	switch selector.Sel.Name {
	case "Group", "Use", "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "QUERY", "Handle", "Any", "Match", "Static", "StaticFS", "StaticFile", "StaticFileFS":
		return true
	default:
		return false
	}
}

func goGinAddIssue(index *sourceIndex, file *goRouteFile, issue goGinIssue, seen map[string]bool) {
	line := 0
	if issue.pos.IsValid() {
		line = goLine(file, issue.pos)
	}
	key := issue.code + "\x00" + file.path + "\x00" + strconv.Itoa(line) + "\x00" + issue.message
	if seen[key] {
		return
	}
	seen[key] = true
	index.Issues = append(index.Issues, Issue{Code: issue.code, File: file.path, Line: line, Message: issue.message})
}

func goGinMarkContextCalls(expr ast.Expr, handled map[*ast.CallExpr]bool) {
	ast.Inspect(expr, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Group" || selector.Sel.Name == "Use") {
			handled[call] = true
		}
		return true
	})
}

func goGinRouterParameters(file *goRouteFile, function *ast.FuncDecl) []goRouteParameter {
	parameters, _ := goRouteParameters(function)
	routers := []goRouteParameter{}
	for _, parameter := range parameters {
		if goGinTypeKind(file, parameter.typeExpr) != "" {
			routers = append(routers, parameter)
		}
	}
	return routers
}

func goGinCalledRouterHelpers(files []*goRouteFile, functions map[string]map[string]goRouteFunction, modulePath string) map[string]bool {
	called := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			shadowed := goGinFunctionBindings(function)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				target, ok := goRouteCallFunction(file, call.Fun, functions, modulePath, shadowed)
				if ok && len(goGinRouterParameters(target.file, target.decl)) > 0 {
					called[target.id] = true
				}
				return true
			})
		}
	}
	return called
}

func goGinControlFlowOperation(file *goRouteFile, node ast.Node, known map[string]bool, functions map[string]map[string]goRouteFunction, modulePath string, shadowed map[string]bool) bool {
	found := false
	ast.Inspect(node, func(child ast.Node) bool {
		if _, ok := child.(*ast.FuncLit); ok {
			return false
		}
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && goGinCallName(selector) && goGinStaticExpression(file, selector.X, known, nil) {
			found = true
			return false
		}
		if target, ok := goRouteCallFunction(file, call.Fun, functions, modulePath, shadowed); ok && len(goGinRouterParameters(target.file, target.decl)) > 0 {
			found = true
			return false
		}
		return true
	})
	return found
}

func goGinControlFlowAffectedNames(file *goRouteFile, node ast.Node, known map[string]bool, functions map[string]map[string]goRouteFunction, modulePath string, shadowed map[string]bool) map[string]bool {
	affected := map[string]bool{}
	ast.Inspect(node, func(child ast.Node) bool {
		if _, ok := child.(*ast.FuncLit); ok {
			return false
		}
		switch value := child.(type) {
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				if name, ok := target.(*ast.Ident); ok && known[name.Name] {
					affected[name.Name] = true
				}
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				if known[name.Name] {
					affected[name.Name] = true
				}
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Use" && goGinStaticExpression(file, selector.X, known, nil) {
				if name, ok := goGinBaseName(selector.X); ok && known[name] {
					affected[name] = true
				}
			}
			if target, ok := goRouteCallFunction(file, value.Fun, functions, modulePath, shadowed); ok {
				parameters, _ := goRouteParameters(target.decl)
				for _, parameter := range goGinRouterParameters(target.file, target.decl) {
					if parameter.name == "" || parameter.position >= len(value.Args) || parameter.position >= len(parameters) {
						continue
					}
					if name, ok := goGinBaseName(value.Args[parameter.position]); ok && known[name] {
						affected[name] = true
					}
				}
			}
		}
		return true
	})
	return affected
}

func indexGoGinRoutes(index *sourceIndex, files []*goRouteFile, packageFuncs map[string]map[string]string, methodsByPackage map[string]map[string][]string, modulePath string, duplicates map[string]bool) error {
	factories := goGinIndexFactoryKinds(files)
	for _, file := range files {
		file.ginFactories = factories
	}
	packageContexts := goGinPackageContexts(index, files, packageFuncs, methodsByPackage, modulePath)
	seenIssues := map[string]bool{}
	functions := goRouteFunctionIndex(files)
	calledHelpers := goGinCalledRouterHelpers(files, functions, modulePath)
	visitedHelpers := map[string]bool{}
	var analyzeFunction func(*goRouteFile, *ast.FuncDecl, map[string]goGinContext, int, map[string]bool) map[string]goGinContext
	analyzeFunction = func(file *goRouteFile, function *ast.FuncDecl, parameterContexts map[string]goGinContext, helperDepth int, callStack map[string]bool) map[string]goGinContext {
		contexts, known, shadowed := goGinLocalState(file, function, packageContexts[file.packagePath])
		for name, context := range parameterContexts {
			contexts[name] = context
			known[name] = true
		}
		handled := map[*ast.CallExpr]bool{}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if len(index.Routes) > api.RouteImpactMaxRoutes || len(index.Issues) > api.RouteImpactMaxIssues {
				return false
			}
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
			switch value := node.(type) {
			case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				if goGinControlFlowOperation(file, value, known, functions, modulePath, shadowed) {
					goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_control_flow", message: "A Gin router operation inside conditional or repeated control flow is not expanded into routes.", pos: value.Pos()}, seenIssues)
					affected := goGinControlFlowAffectedNames(file, value, known, functions, modulePath, shadowed)
					identities := map[string]bool{}
					for name := range affected {
						identities[contexts[name].identity] = true
					}
					for name, context := range contexts {
						if !affected[name] && !identities[context.identity] {
							continue
						}
						context.prefixKnown = false
						context.middlewareUnresolved = true
						contexts[name] = context
					}
					return false
				}
			case *ast.AssignStmt:
				for i, target := range value.Lhs {
					name, ok := target.(*ast.Ident)
					if !ok || !known[name.Name] {
						continue
					}
					var expression ast.Expr
					if len(value.Lhs) == len(value.Rhs) {
						expression = value.Rhs[i]
					} else if len(value.Lhs) == 1 && len(value.Rhs) == 1 {
						expression = value.Rhs[0]
					}
					if expression == nil {
						continue
					}
					goGinMarkContextCalls(expression, handled)
					context, recognized, issues := goGinResolveExpression(file, expression, contexts, index, packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
					for _, issue := range issues {
						goGinAddIssue(index, file, issue, seenIssues)
					}
					if !recognized {
						context = goGinContext{identity: goGinContextID(file, value.Pos(), name.Name), prefixKnown: false, middlewareUnresolved: true}
						goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_router", message: "A Gin router value is reassigned from an expression that cannot be traced.", pos: value.Pos()}, seenIssues)
					}
					if context.identity == "" {
						context.identity = goGinContextID(file, value.Pos(), name.Name)
					}
					contexts[name.Name] = context
					goGinUpdateAliases(contexts, context)
				}
			case *ast.ValueSpec:
				for i, name := range value.Names {
					if _, ok := known[name.Name]; !ok {
						continue
					}
					var expression ast.Expr
					if len(value.Names) == len(value.Values) {
						expression = value.Values[i]
					} else if len(value.Names) == 1 && len(value.Values) == 1 {
						expression = value.Values[0]
					}
					if expression != nil {
						goGinMarkContextCalls(expression, handled)
						context, recognized, issues := goGinResolveExpression(file, expression, contexts, index, packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
						for _, issue := range issues {
							goGinAddIssue(index, file, issue, seenIssues)
						}
						if recognized {
							if context.identity == "" {
								context.identity = goGinContextID(file, value.Pos(), name.Name)
							}
							contexts[name.Name] = context
							goGinUpdateAliases(contexts, context)
						}
					}
				}
			case *ast.CallExpr:
				if handled[value] {
					return true
				}
				if target, ok := goRouteCallFunction(file, value.Fun, functions, modulePath, shadowed); ok {
					parameters, variadic := goRouteParameters(target.decl)
					routerParameters := goGinRouterParameters(target.file, target.decl)
					if len(routerParameters) > 0 {
						calledHelpers[target.id] = true
						visitedHelpers[target.id] = true
						if helperDepth >= api.RouteImpactMaxGraphDepth {
							goGinAddIssue(index, file, goGinIssue{code: "gin_route_helper_depth_exceeded", message: "Nested Gin route registration helpers exceed the static analysis depth limit.", pos: value.Pos()}, seenIssues)
							return false
						}
						if callStack[target.id] {
							goGinAddIssue(index, file, goGinIssue{code: "gin_route_helper_cycle", message: "A recursive Gin route registration helper cannot be expanded safely.", pos: value.Pos()}, seenIssues)
							return false
						}
						if variadic || len(value.Args) != len(parameters) {
							goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_route_helper", message: "A Gin route helper call has variadic or mismatched arguments and cannot be bound to router parameters.", pos: value.Pos()}, seenIssues)
							return false
						}
						overrides := map[string]goGinContext{}
						bindingFailed := false
						for _, parameter := range routerParameters {
							if parameter.name == "" || parameter.position >= len(value.Args) {
								bindingFailed = true
								break
							}
							context, recognized, issues := goGinResolveExpression(file, value.Args[parameter.position], contexts, index, packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
							for _, issue := range issues {
								goGinAddIssue(index, file, issue, seenIssues)
							}
							if !recognized || !context.prefixKnown {
								bindingFailed = true
								break
							}
							if context.identity == "" {
								context.identity = goGinContextID(file, value.Pos(), parameter.name)
							}
							overrides[parameter.name] = context
						}
						if bindingFailed {
							goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_route_helper", message: "A Gin route helper argument has an unknown router origin or prefix; its routes were not emitted.", pos: value.Pos()}, seenIssues)
							return false
						}
						childStack := make(map[string]bool, len(callStack)+1)
						for id := range callStack {
							childStack[id] = true
						}
						childStack[target.id] = true
						updates := analyzeFunction(target.file, target.decl, overrides, helperDepth+1, childStack)
						for identity, updated := range updates {
							updated.identity = identity
							goGinUpdateAliases(contexts, updated)
						}
						return false
					}
				}
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				context, recognized, contextIssues := goGinResolveExpression(file, selector.X, contexts, index, packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
				for _, issue := range contextIssues {
					goGinAddIssue(index, file, issue, seenIssues)
				}
				if !recognized {
					if goGinCallName(selector) && goGinFileImports(file) {
						goGinAddIssue(index, file, goGinIssue{code: "unresolved_gin_router", message: "A Gin route operation uses a receiver whose router origin cannot be resolved statically.", pos: value.Pos()}, seenIssues)
					}
					return true
				}
				line := goLine(file, value.Pos())
				switch selector.Sel.Name {
				case "Group":
					return true
				case "Use":
					name, simple := goGinBaseName(selector.X)
					updated, _, issues := goGinResolveExpression(file, value, contexts, index, packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
					for _, issue := range issues {
						goGinAddIssue(index, file, issue, seenIssues)
					}
					if simple && contexts[name].prefixKnown {
						goGinUpdateAliases(contexts, updated)
					}
					if !context.prefixKnown {
						goGinAddIssue(index, file, goGinIssue{code: "gin_router_group_prefix_unknown", message: "A Gin router group parameter has no statically known prefix; its routes were not emitted.", pos: value.Pos()}, seenIssues)
					}
					return true
				}
				methods, pathArg, handlerStart, static, catchAll, valid, registrationIssues := goGinRegistration(selector.Sel.Name, value.Args, file, shadowed)
				for _, issue := range registrationIssues {
					if !issue.pos.IsValid() {
						issue.pos = value.Pos()
					}
					goGinAddIssue(index, file, issue, seenIssues)
				}
				if !valid {
					return true
				}
				if !context.prefixKnown {
					goGinAddIssue(index, file, goGinIssue{code: "gin_router_group_prefix_unknown", message: "A Gin router group parameter has no statically known prefix; its routes were not emitted.", pos: value.Pos()}, seenIssues)
					return true
				}
				pattern, literal := goStringLiteral(value.Args[pathArg])
				if !literal {
					goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_route_pattern", message: "A dynamic Gin route pattern cannot be mapped to a stable route.", pos: value.Pos()}, seenIssues)
					return true
				}
				if static && catchAll {
					pattern = strings.TrimSuffix(pattern, "/") + "/*filepath"
				}
				fullPath, pathOK, pathCatchAll := goGinPath(context.prefix, pattern)
				if !pathOK {
					goGinAddIssue(index, file, goGinIssue{code: "unsupported_gin_route_pattern", message: "A Gin route pattern is outside the supported static path and parameter model.", pos: value.Pos()}, seenIssues)
					return true
				}
				if catchAll || pathCatchAll {
					goGinAddIssue(index, file, goGinIssue{code: "unsupported_gin_catch_all_route", message: "A Gin catch-all route can consume multiple path segments and cannot be mapped to an exact Gregale route selector.", pos: value.Pos()}, seenIssues)
				}
				if context.middlewareUnresolved {
					goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_middleware", message: "A Gin router group's middleware chain is incomplete; route impact may miss middleware behavior.", pos: value.Pos()}, seenIssues)
				}
				if !static && handlerStart < len(value.Args) {
					handlers := value.Args[handlerStart:]
					if len(handlers) == 0 {
						goGinAddIssue(index, file, goGinIssue{code: "invalid_gin_route_registration", message: "A Gin route registration does not include a handler.", pos: value.Pos()}, seenIssues)
						return true
					}
					var unresolvedMiddleware bool
					var routeMiddleware []string
					if len(handlers) > 1 {
						routeMiddleware, unresolvedMiddleware = goGinResolveHandlers(index, file, handlers[:len(handlers)-1], packageFuncs, methodsByPackage[file.packagePath], modulePath, shadowed)
					}
					if unresolvedMiddleware {
						goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_middleware", message: "A Gin route middleware handler does not resolve to a local function; route impact may miss middleware behavior.", pos: value.Pos()}, seenIssues)
					}
					handler, handlerName, handlerLocation, handlerOK := goGinResolveHandler(file, handlers[len(handlers)-1], packageFuncs, methodsByPackage[file.packagePath], modulePath, index.Symbols, shadowed)
					if !handlerOK {
						goGinAddIssue(index, file, goGinIssue{code: "dynamic_gin_route_handler", message: "A Gin route handler is dynamic or does not resolve to a local function.", pos: value.Pos()}, seenIssues)
					}
					registration, _ := goNodeBytes(file.fset, value)
					for _, method := range methods {
						key := method + "\x00" + fullPath
						if duplicates[key] {
							goGinAddIssue(index, file, goGinIssue{code: "duplicate_go_route", message: "A method and path pair is registered more than once; route attribution is ambiguous.", pos: value.Pos()}, seenIssues)
							continue
						}
						duplicates[key] = true
						loc := Location{File: file.path, Line: line}
						route := Route{Method: method, Path: fullPath, Handler: handlerName, Source: handlerLocation, Registration: loc,
							RegistrationHash: contentHash(registration), ContextFiles: []string{file.path}, DependencyFiles: []string{},
							HandlerSymbol: handler, DependencySymbols: goGinJoinMiddleware(context.middleware, routeMiddleware), FallbackFiles: []string{}}
						if static {
							route.Handler = "Gin static handler"
							route.Source = loc
						}
						if route.Source.File == "" {
							route.Source = loc
						}
						index.Routes = append(index.Routes, route)
						if len(index.Routes) > api.RouteImpactMaxRoutes {
							return false
						}
					}
				} else {
					registration, _ := goNodeBytes(file.fset, value)
					for _, method := range methods {
						key := method + "\x00" + fullPath
						if duplicates[key] {
							goGinAddIssue(index, file, goGinIssue{code: "duplicate_go_route", message: "A method and path pair is registered more than once; route attribution is ambiguous.", pos: value.Pos()}, seenIssues)
							continue
						}
						duplicates[key] = true
						loc := Location{File: file.path, Line: line}
						index.Routes = append(index.Routes, Route{Method: method, Path: fullPath, Handler: "Gin static handler", Source: loc,
							Registration: loc, RegistrationHash: contentHash(registration), ContextFiles: []string{file.path}, DependencyFiles: []string{},
							DependencySymbols: append([]string{}, context.middleware...), FallbackFiles: []string{}})
					}
				}
			}
			return true
		})
		updates := map[string]goGinContext{}
		for _, context := range parameterContexts {
			if context.identity == "" {
				continue
			}
			for _, current := range contexts {
				if current.identity == context.identity {
					updates[context.identity] = current
					break
				}
			}
		}
		return updates
	}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			functionID := goFunctionID(file.packagePath, "", function.Name.Name)
			if calledHelpers[functionID] && len(goGinRouterParameters(file, function)) > 0 {
				continue
			}
			analyzeFunction(file, function, nil, 0, map[string]bool{functionID: true})
			if len(index.Routes) > api.RouteImpactMaxRoutes {
				return errors.New("go source exceeds the route impact route limit")
			}
			if len(index.Issues) > api.RouteImpactMaxIssues {
				return errors.New("go source exceeds the route impact issue limit")
			}
		}
	}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			functionID := goFunctionID(file.packagePath, "", function.Name.Name)
			if calledHelpers[functionID] && len(goGinRouterParameters(file, function)) > 0 && !visitedHelpers[functionID] {
				goGinAddIssue(index, file, goGinIssue{code: "unresolved_gin_route_helper", message: "A Gin route helper has no reachable static callsite with a resolvable router context.", pos: function.Pos()}, seenIssues)
			}
		}
	}
	return nil
}

func goGinFileImports(file *goRouteFile) bool {
	for _, importPath := range file.imports {
		if goIsGinImport(importPath) {
			return true
		}
	}
	return false
}
