package routeimpact

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type goRouteFile struct {
	path         string
	file         *ast.File
	fset         *token.FileSet
	packagePath  string
	imports      map[string]string
	muxVars      map[string]bool
	functions    map[string]string
	ginDotImport bool
	ginFactories map[string]map[string]string
}

type goFunction struct {
	file   *goRouteFile
	decl   *ast.FuncDecl
	name   string
	method string
	id     string
}

func indexGoNetHTTP(snapshot sourceSnapshot) (sourceIndex, error) {
	index := sourceIndex{Routes: []Route{}, Dependencies: map[string][]string{}, Issues: append([]Issue{}, snapshot.issues...), Modules: map[string]moduleSemantics{}, Symbols: map[string]functionSymbol{}}
	files := make([]*goRouteFile, 0, snapshot.meta.GoFiles)
	modulePath := goModulePath(snapshot.files["go.mod"].body)
	go122OrNewer, hasGoVersion := goModuleUsesGo122(snapshot.files["go.mod"].body)
	if modulePath == "" {
		index.Issues = append(index.Issues, Issue{Code: "go_module_unavailable", File: "go.mod", Message: "A Go module path is required to resolve route references across packages."})
	}
	for name, source := range snapshot.files {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || source.body == nil {
			continue
		}
		if goHasBuildConstraint(source.body) {
			index.Issues = append(index.Issues, Issue{Code: "go_build_constraints_unknown", File: name, Message: "Go build constraints are not evaluated; this file may not be part of the selected build."})
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, name, source.body, parser.AllErrors)
		if err != nil {
			issue := Issue{Code: "go_parse_error", File: name, Message: "A Go source file could not be parsed; route analysis is incomplete."}
			index.Issues = append(index.Issues, issue)
			if parsed == nil || parsed.Name == nil {
				continue
			}
		}
		file := &goRouteFile{path: name, file: parsed, fset: fset, imports: map[string]string{}, muxVars: map[string]bool{}, functions: map[string]string{}}
		file.packagePath = goPackagePath(modulePath, name, parsed.Name.Name)
		for _, spec := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			alias := path.Base(importPath)
			if goIsChiImport(importPath) {
				alias = "chi"
			} else if goIsGinImport(importPath) {
				alias = "gin"
			}
			if spec.Name != nil {
				alias = spec.Name.Name
				if alias == "." && goIsGinImport(importPath) {
					file.ginDotImport = true
				}
			}
			if alias != "_" && alias != "." {
				file.imports[alias] = importPath
			}
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	if len(files) == 0 {
		index.Issues = append(index.Issues, Issue{Code: "go_sources_unavailable", Message: "No parsable non-test Go source files were found in the selected root."})
		if len(index.Issues) > api.RouteImpactMaxIssues {
			return sourceIndex{}, errors.New("go source exceeds the route impact issue limit")
		}
		return index, nil
	}

	packageFiles := map[string][]string{}
	packageFuncs := map[string]map[string]string{}
	var functions []goFunction
	for _, file := range files {
		packageFiles[file.packagePath] = append(packageFiles[file.packagePath], file.path)
		if packageFuncs[file.packagePath] == nil {
			packageFuncs[file.packagePath] = map[string]string{}
		}
		for _, decl := range file.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			method := goReceiverName(fn.Recv)
			id := goFunctionID(file.packagePath, method, fn.Name.Name)
			if len(functions) >= api.RouteImpactMaxSymbols {
				return sourceIndex{}, errors.New("go source exceeds the route impact function limit")
			}
			functions = append(functions, goFunction{file: file, decl: fn, name: fn.Name.Name, method: method, id: id})
			if method == "" {
				if _, exists := packageFuncs[file.packagePath][fn.Name.Name]; exists {
					index.Issues = append(index.Issues, Issue{Code: "duplicate_go_function", File: file.path, Line: goLine(file, fn.Pos()), Symbol: fn.Name.Name, Message: "A package contains duplicate function declarations; function references are ambiguous."})
					continue
				}
				packageFuncs[file.packagePath][fn.Name.Name] = id
				file.functions[fn.Name.Name] = id
			}
		}
	}
	for _, files := range packageFiles {
		sort.Strings(files)
	}
	packageMuxVars := goPackageMuxVariables(files)
	for _, file := range files {
		for name := range packageMuxVars[file.packagePath] {
			file.muxVars[name] = true
		}
	}

	methodsByPackage := map[string]map[string][]string{}
	for _, fn := range functions {
		if fn.method != "" {
			if methodsByPackage[fn.file.packagePath] == nil {
				methodsByPackage[fn.file.packagePath] = map[string][]string{}
			}
			methodsByPackage[fn.file.packagePath][fn.name] = append(methodsByPackage[fn.file.packagePath][fn.name], fn.id)
		}
	}
	for _, methods := range methodsByPackage {
		for name := range methods {
			sort.Strings(methods[name])
		}
	}
	symbolEdges, symbolIssues := 0, 0
	for _, fn := range functions {
		body, err := goNodeBytes(fn.file.fset, fn.decl)
		if err != nil {
			return sourceIndex{}, err
		}
		refs := goFunctionReferences(fn, packageFuncs, methodsByPackage[fn.file.packagePath], modulePath)
		issues := goUnknownFunctionCalls(fn.file, fn.decl.Body, fn.id, packageFuncs)
		symbolIssues += len(issues)
		if symbolIssues > api.RouteImpactMaxSymbolIssues {
			return sourceIndex{}, errors.New("go source exceeds the route impact function issue limit")
		}
		symbolEdges += len(refs)
		if symbolEdges > api.RouteImpactMaxSymbolEdges {
			return sourceIndex{}, errors.New("go source exceeds the route impact function reference limit")
		}
		if len(index.Symbols) >= api.RouteImpactMaxSymbols {
			return sourceIndex{}, errors.New("go source exceeds the route impact function limit")
		}
		line := goLine(fn.file, fn.decl.Name.Pos())
		index.Symbols[fn.id] = functionSymbol{Name: fn.id, File: fn.file.path, Line: line, Hash: contentHash(body), References: refs, Issues: issues}
	}

	importEdges := 0
	for _, file := range files {
		whole, err := goNodeBytes(file.fset, file.file)
		if err != nil {
			return sourceIndex{}, err
		}
		initBytes := bytes.Buffer{}
		initNames := map[string]bool{}
		for _, decl := range file.file.Decls {
			switch value := decl.(type) {
			case *ast.GenDecl:
				if value.Tok == token.VAR || value.Tok == token.CONST {
					formatted, formatErr := goNodeBytes(file.fset, value)
					if formatErr != nil {
						return sourceIndex{}, formatErr
					}
					initBytes.Write(formatted)
					for _, name := range goReferencesInNode(file, value, packageFuncs, methodsByPackage[file.packagePath], modulePath) {
						initNames[name] = true
					}
				}
			case *ast.FuncDecl:
				if value.Name.Name == "init" {
					formatted, formatErr := goNodeBytes(file.fset, value)
					if formatErr != nil {
						return sourceIndex{}, formatErr
					}
					initBytes.Write(formatted)
					for _, name := range goReferencesInNode(file, value, packageFuncs, methodsByPackage[file.packagePath], modulePath) {
						initNames[name] = true
					}
				}
			}
		}
		initializers := make([]string, 0, len(initNames))
		for name := range initNames {
			initializers = append(initializers, name)
		}
		sort.Strings(initializers)
		index.Modules[file.path] = moduleSemantics{Hash: contentHash(whole), InitializationHash: contentHash(initBytes.Bytes()), InitializationSymbols: initializers, Issues: []Issue{}}
		for _, importPath := range file.imports {
			packagePath, local := goLocalPackage(modulePath, importPath)
			if !local {
				continue
			}
			if _, exists := packageFiles[packagePath]; !exists {
				index.Issues = append(index.Issues, Issue{Code: "local_go_dependency_unavailable", File: file.path, Message: "A same-module import is outside the selected source root; its route references cannot be resolved."})
				continue
			}
			index.Dependencies[file.path] = append(index.Dependencies[file.path], packageFiles[packagePath]...)
		}
		sort.Strings(index.Dependencies[file.path])
		index.Dependencies[file.path] = uniqueStrings(index.Dependencies[file.path])
		importEdges += len(index.Dependencies[file.path])
		if importEdges > api.RouteImpactMaxImportEdges {
			return sourceIndex{}, errors.New("go source exceeds the route impact import limit")
		}
	}

	duplicates := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			muxVars, shadowed := goLocalMuxVariables(file, function)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") || !goIsServeMuxMethod(file, selector.X, muxVars, shadowed) {
					return true
				}
				line := goLine(file, call.Pos())
				if len(call.Args) < 2 {
					index.Issues = append(index.Issues, Issue{Code: "invalid_go_route_registration", File: file.path, Line: line, Message: "A ServeMux registration does not include a pattern and handler."})
					return true
				}
				pattern, literal := goStringLiteral(call.Args[0])
				if !literal {
					index.Issues = append(index.Issues, Issue{Code: "dynamic_go_route_pattern", File: file.path, Line: line, Message: "A dynamic ServeMux pattern cannot be mapped to a stable route."})
					return true
				}
				methods, routePath, valid := goRoutePattern(pattern)
				if !valid {
					index.Issues = append(index.Issues, Issue{Code: "unsupported_go_route_pattern", File: file.path, Line: line, Message: "A ServeMux pattern uses a host or syntax outside the static route model."})
					return true
				}
				patternParts := strings.Fields(pattern)
				if len(patternParts) == 1 {
					index.Issues = append(index.Issues, Issue{Code: "unbounded_go_route_methods", File: file.path, Line: line, Message: "A path-only ServeMux pattern also matches non-standard HTTP methods that the report cannot enumerate."})
				}
				if strings.ContainsAny(routePath, "{}") || len(patternParts) == 2 {
					if !hasGoVersion || !go122OrNewer {
						index.Issues = append(index.Issues, Issue{Code: "go_route_semantics_unknown", File: file.path, Line: line, Message: "The module Go version does not establish Go 1.22 ServeMux pattern semantics."})
					}
				}
				handler, handlerName, handlerLocation, handlerOK := goResolveHandler(file, call.Args[1], packageFuncs, methodsByPackage[file.packagePath], modulePath, index.Symbols, shadowed)
				if !handlerOK {
					index.Issues = append(index.Issues, Issue{Code: "dynamic_go_route_handler", File: file.path, Line: line, Message: "A ServeMux handler is dynamic or does not resolve to a local function."})
				}
				registration, _ := goNodeBytes(file.fset, call)
				for _, method := range methods {
					key := method + "\x00" + routePath
					if duplicates[key] {
						index.Issues = append(index.Issues, Issue{Code: "duplicate_go_route", File: file.path, Line: line, Message: "A method and path pair is registered more than once; route attribution is ambiguous."})
						continue
					}
					duplicates[key] = true
					loc := Location{File: file.path, Line: line}
					route := Route{Method: method, Path: routePath, Handler: handlerName, Source: handlerLocation, Registration: loc,
						RegistrationHash: contentHash(registration), ContextFiles: []string{file.path}, DependencyFiles: []string{},
						HandlerSymbol: handler, DependencySymbols: []string{}, FallbackFiles: []string{}}
					if route.Source.File == "" {
						route.Source = loc
					}
					index.Routes = append(index.Routes, route)
					if len(index.Routes) > api.RouteImpactMaxRoutes {
						return false
					}
				}
				return true
			})
		}
	}
	packageChiVars := goPackageChiVariables(files)
	if err := indexGoChiRoutes(&index, files, packageChiVars, packageFuncs, methodsByPackage, modulePath, duplicates); err != nil {
		return sourceIndex{}, err
	}
	if err := indexGoGinRoutes(&index, files, packageFuncs, methodsByPackage, modulePath, duplicates); err != nil {
		return sourceIndex{}, err
	}
	if len(index.Routes) > api.RouteImpactMaxRoutes {
		return sourceIndex{}, errors.New("go source exceeds the route impact route limit")
	}
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return sourceIndex{}, errors.New("go source exceeds the route impact issue limit")
	}
	hasDynamicRoute := false
	for _, issue := range index.Issues {
		if strings.HasPrefix(issue.Code, "dynamic_go_route") || strings.HasPrefix(issue.Code, "dynamic_chi_route") ||
			strings.HasPrefix(issue.Code, "dynamic_chi_mount") || strings.Contains(issue.Code, "chi_mount") ||
			strings.Contains(issue.Code, "chi_route_helper") || strings.Contains(issue.Code, "gin_route_helper") ||
			strings.HasPrefix(issue.Code, "unresolved_chi_route") ||
			issue.Code == "unsupported_go_route_pattern" || strings.HasPrefix(issue.Code, "unsupported_chi_route") || issue.Code == "unmodeled_chi_mount" ||
			strings.HasPrefix(issue.Code, "dynamic_gin_") || strings.HasPrefix(issue.Code, "unsupported_gin_") || strings.HasPrefix(issue.Code, "unresolved_gin_") {
			hasDynamicRoute = true
			break
		}
	}
	if len(index.Routes) == 0 && !hasDynamicRoute {
		index.Issues = append(index.Issues, Issue{Code: "no_static_nethttp_routes", Message: "No supported Go HTTP route registrations were found; dynamic or third-party routers may be outside the selected model."})
	}
	symbolIssues = goSymbolIssueCount(index.Symbols)
	if symbolIssues > api.RouteImpactMaxSymbolIssues {
		return sourceIndex{}, errors.New("go source exceeds the route impact function issue limit")
	}
	if len(index.Symbols) > api.RouteImpactMaxSymbols {
		return sourceIndex{}, errors.New("go source exceeds the route impact function limit")
	}
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return sourceIndex{}, errors.New("go source exceeds the route impact issue limit")
	}
	sort.Slice(index.Routes, func(i, j int) bool {
		if index.Routes[i].Method != index.Routes[j].Method {
			return index.Routes[i].Method < index.Routes[j].Method
		}
		return index.Routes[i].Path < index.Routes[j].Path
	})
	return index, nil
}

func goModulePath(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"`")
		}
	}
	return ""
}

func goModuleUsesGo122(body []byte) (newSemantics, found bool) {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "go" {
			continue
		}
		parts := strings.Split(fields[1], ".")
		if len(parts) < 2 {
			return false, true
		}
		major, majorErr := strconv.Atoi(parts[0])
		minor, minorErr := strconv.Atoi(parts[1])
		if majorErr != nil || minorErr != nil {
			return false, true
		}
		return major > 1 || (major == 1 && minor >= 22), true
	}
	return false, false
}

func goHasBuildConstraint(body []byte) bool {
	lines := strings.SplitN(string(body), "\n", 100)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		if strings.HasPrefix(trimmed, "//go:build ") || strings.HasPrefix(trimmed, "// +build ") {
			return true
		}
	}
	return false
}

func goPackagePath(modulePath, file, packageName string) string {
	dir := path.Dir(file)
	if modulePath != "" {
		if dir == "." {
			return modulePath
		}
		return path.Join(modulePath, dir)
	}
	if dir == "." {
		return "local:" + packageName
	}
	return "local:" + path.Join(dir, packageName)
}

func goLocalPackage(modulePath, importPath string) (string, bool) {
	if modulePath == "" || (importPath != modulePath && !strings.HasPrefix(importPath, modulePath+"/")) {
		return "", false
	}
	return importPath, true
}

func goFunctionID(packagePath, receiver, name string) string {
	if receiver != "" {
		return "go:" + packagePath + ".(" + receiver + ")." + name
	}
	return "go:" + packagePath + "." + name
}

func goReceiverName(receivers *ast.FieldList) string {
	if receivers == nil || len(receivers.List) == 0 || len(receivers.List[0].Names) == 0 {
		return ""
	}
	typ := receivers.List[0].Type
	for {
		switch value := typ.(type) {
		case *ast.StarExpr:
			typ = value.X
		case *ast.IndexExpr:
			typ = value.X
		case *ast.IndexListExpr:
			typ = value.X
		default:
			if ident, ok := typ.(*ast.Ident); ok {
				return ident.Name
			}
			return ""
		}
	}
}

func goNodeBytes(fset *token.FileSet, node ast.Node) ([]byte, error) {
	var out bytes.Buffer
	if err := ast.Fprint(&out, fset, node, func(name string, value reflect.Value) bool {
		if !value.IsValid() || value.Type() == reflect.TypeOf(token.Pos(0)) {
			return false
		}
		return name != "Obj" && name != "Scope" && name != "Unresolved"
	}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func goLine(file *goRouteFile, position token.Pos) int {
	return file.fset.Position(position).Line
}

func goReferencesInNode(file *goRouteFile, node ast.Node, packages map[string]map[string]string, methods map[string][]string, modulePath string) []string {
	refs := map[string]bool{}
	var resolve func(ast.Expr) string
	resolve = func(expr ast.Expr) string {
		switch value := expr.(type) {
		case *ast.ParenExpr:
			return resolve(value.X)
		case *ast.UnaryExpr:
			return resolve(value.X)
		case *ast.IndexExpr:
			return resolve(value.X)
		case *ast.IndexListExpr:
			return resolve(value.X)
		case *ast.Ident:
			return packages[file.packagePath][value.Name]
		case *ast.SelectorExpr:
			if base, ok := value.X.(*ast.Ident); ok {
				if importPath := file.imports[base.Name]; importPath != "" {
					if pkg, local := goLocalPackage(modulePath, importPath); local {
						return packages[pkg][value.Sel.Name]
					}
				} else if candidates := methods[value.Sel.Name]; len(candidates) == 1 && strings.HasPrefix(candidates[0], "go:"+file.packagePath+".") {
					return candidates[0]
				}
			}
		}
		return ""
	}
	ast.Inspect(node, func(child ast.Node) bool {
		if call, ok := child.(*ast.CallExpr); ok {
			if symbol := resolve(call.Fun); symbol != "" {
				refs[symbol] = true
			}
			for _, arg := range call.Args {
				if symbol := resolve(arg); symbol != "" {
					refs[symbol] = true
				}
			}
		}
		return true
	})
	out := make([]string, 0, len(refs))
	for name := range refs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func goUnknownFunctionCalls(file *goRouteFile, node ast.Node, symbol string, packages map[string]map[string]string) []Issue {
	issues := []Issue{}
	ast.Inspect(node, func(child ast.Node) bool {
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident := goCalledIdentifier(call.Fun)
		if ident == nil || packages[file.packagePath][ident.Name] != "" || goBuiltinOrConversion(ident.Name) {
			return true
		}
		issues = append(issues, Issue{Code: "unresolved_go_call", File: file.path, Line: goLine(file, call.Pos()), Symbol: symbol, Message: "A local call target is not statically indexed; route impact uses module fallback."})
		return true
	})
	return issues
}

func goCalledIdentifier(expr ast.Expr) *ast.Ident {
	switch value := expr.(type) {
	case *ast.Ident:
		return value
	case *ast.ParenExpr:
		return goCalledIdentifier(value.X)
	case *ast.IndexExpr:
		return goCalledIdentifier(value.X)
	case *ast.IndexListExpr:
		return goCalledIdentifier(value.X)
	default:
		return nil
	}
}

func goBuiltinOrConversion(name string) bool {
	switch name {
	case "append", "bool", "byte", "cap", "clear", "close", "complex", "complex64", "complex128", "copy", "delete", "error", "float32", "float64", "imag", "int", "int8", "int16", "int32", "int64", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	}
	return name != "" && name[0] >= 'A' && name[0] <= 'Z' // Unindexed exported names are normally type conversions.
}

func goFunctionReferences(function goFunction, packages map[string]map[string]string, methods map[string][]string, modulePath string) []string {
	var node ast.Node = function.decl
	return goReferencesInNode(function.file, node, packages, methods, modulePath)
}

func goLocalMuxVariables(file *goRouteFile, scope ast.Node) (map[string]bool, map[string]bool) {
	known := map[string]bool{}
	for name := range file.muxVars {
		known[name] = true
	}
	// A package-level mux name is not authoritative inside a function that
	// declares or reassigns the same name. Re-add only local values whose
	// ServeMux origin can be established statically.
	shadowed := map[string]bool{}
	ast.Inspect(scope, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				goAddAssignedName(shadowed, target)
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				shadowed[name.Name] = true
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				goAddAssignedName(shadowed, value.Key)
				goAddAssignedName(shadowed, value.Value)
			}
		case *ast.FuncLit:
			goAddFieldNames(shadowed, value.Type.Params)
			goAddFieldNames(shadowed, value.Type.Results)
		}
		return true
	})
	if function, ok := scope.(*ast.FuncDecl); ok {
		goAddFieldNames(shadowed, function.Type.Params)
		goAddFieldNames(shadowed, function.Type.Results)
		goAddFieldNames(shadowed, function.Recv)
	}
	for name := range shadowed {
		delete(known, name)
	}
	// Registration helpers commonly receive the mux from their caller instead
	// of constructing it locally. A parameter is a reliable ServeMux origin
	// only when its declared type names net/http.ServeMux.
	if function, ok := scope.(*ast.FuncDecl); ok && function.Type.Params != nil {
		muxParameters := map[string]bool{}
		for _, field := range function.Type.Params.List {
			if !goIsServeMuxType(file, field.Type) {
				continue
			}
			for _, name := range field.Names {
				muxParameters[name.Name] = true
			}
		}
		bodyBindings := map[string]bool{}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.ValueSpec:
				for _, name := range value.Names {
					bodyBindings[name.Name] = true
				}
			case *ast.AssignStmt:
				if value.Tok == token.DEFINE {
					for _, name := range value.Lhs {
						goAddAssignedName(bodyBindings, name)
					}
				}
			case *ast.RangeStmt:
				if value.Tok == token.DEFINE {
					goAddAssignedName(bodyBindings, value.Key)
					goAddAssignedName(bodyBindings, value.Value)
				}
			case *ast.FuncLit:
				goAddFieldNames(bodyBindings, value.Type.Params)
				goAddFieldNames(bodyBindings, value.Type.Results)
			}
			return true
		})
		for name := range muxParameters {
			if bodyBindings[name] {
				continue
			}
			known[name] = true
			delete(shadowed, name)
		}
	}
	for changed := true; changed; {
		changed = false
		ast.Inspect(scope, func(node ast.Node) bool {
			var lhs []ast.Expr
			var rhs []ast.Expr
			switch value := node.(type) {
			case *ast.AssignStmt:
				lhs, rhs = value.Lhs, value.Rhs
			case *ast.ValueSpec:
				for _, name := range value.Names {
					lhs = append(lhs, name)
				}
				rhs = value.Values
			default:
				return true
			}
			for i, left := range lhs {
				ident, ok := left.(*ast.Ident)
				if !ok || ident.Name == "_" {
					continue
				}
				var right ast.Expr
				if len(rhs) == len(lhs) {
					right = rhs[i]
				} else if len(rhs) == 1 && len(lhs) == 1 {
					right = rhs[0]
				}
				var typeExpr ast.Expr
				if value, ok := node.(*ast.ValueSpec); ok {
					typeExpr = value.Type
				}
				if goIsServeMuxValue(file, right) || goIsServeMuxType(file, typeExpr) || goIsKnownMux(known, right) {
					if !known[ident.Name] {
						known[ident.Name] = true
						changed = true
					}
				}
			}
			return true
		})
	}
	return known, shadowed
}

func goPackageMuxVariables(files []*goRouteFile) map[string]map[string]bool {
	known := map[string]map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, file := range files {
			if known[file.packagePath] == nil {
				known[file.packagePath] = map[string]bool{}
			}
			for _, decl := range file.file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range value.Names {
						var right ast.Expr
						if len(value.Values) == len(value.Names) {
							right = value.Values[i]
						} else if len(value.Values) == 1 && len(value.Names) == 1 {
							right = value.Values[0]
						}
						ident, isIdent := right.(*ast.Ident)
						if goIsServeMuxValue(file, right) || goIsServeMuxType(file, value.Type) || (isIdent && known[file.packagePath][ident.Name]) {
							if !known[file.packagePath][name.Name] {
								known[file.packagePath][name.Name] = true
								changed = true
							}
						}
					}
				}
			}
		}
	}
	return known
}

func goIsServeMuxValue(file *goRouteFile, expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goIsServeMuxValue(file, value.X)
	case *ast.UnaryExpr:
		return value.Op == token.AND && goIsServeMuxValue(file, value.X)
	case *ast.CompositeLit:
		return goIsServeMuxType(file, value.Type)
	case *ast.CallExpr:
		if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "NewServeMux" {
			alias, ok := selector.X.(*ast.Ident)
			return ok && file.imports[alias.Name] == "net/http"
		}
		if ident, ok := value.Fun.(*ast.Ident); ok && ident.Name == "new" && len(value.Args) == 1 {
			return goIsServeMuxType(file, value.Args[0])
		}
	}
	return false
}

func goIsServeMuxType(file *goRouteFile, expr ast.Expr) bool {
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = pointer.X
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ServeMux" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && file.imports[alias.Name] == "net/http"
}

func goAddFieldNames(names map[string]bool, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			names[name.Name] = true
		}
	}
}

func goAddAssignedName(names map[string]bool, target ast.Expr) {
	switch value := target.(type) {
	case *ast.Ident:
		if value.Name != "_" {
			names[value.Name] = true
		}
	case *ast.ParenExpr:
		goAddAssignedName(names, value.X)
	}
}

func goSymbolIssueCount(symbols map[string]functionSymbol) int {
	total := 0
	for _, symbol := range symbols {
		total += len(symbol.Issues)
	}
	return total
}

func goIsKnownMux(muxVars map[string]bool, expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && muxVars[ident.Name]
}

func goIsServeMuxMethod(file *goRouteFile, expr ast.Expr, muxVars, shadowed map[string]bool) bool {
	if ident, ok := expr.(*ast.Ident); ok && !shadowed[ident.Name] && file.imports[ident.Name] == "net/http" {
		return true // http.Handle and http.HandleFunc use DefaultServeMux.
	}
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if base, ok := selector.X.(*ast.Ident); ok && !shadowed[base.Name] && file.imports[base.Name] == "net/http" && selector.Sel.Name == "DefaultServeMux" {
			return true
		}
	}
	return goIsKnownMux(muxVars, expr)
}

func goStringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func goRoutePattern(pattern string) ([]string, string, bool) {
	fields := strings.Fields(pattern)
	if len(fields) == 0 || len(fields) > 2 {
		return nil, "", false
	}
	method, routePath := "", fields[0]
	if len(fields) == 2 {
		method, routePath = fields[0], fields[1]
		if !validMethod(method) {
			return nil, "", false
		}
	}
	if !goValidPathPattern(routePath) || strings.ContainsAny(routePath, "?#") {
		return nil, "", false
	}
	if method == "" {
		return []string{"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"}, routePath, true
	}
	methods := []string{method}
	if method == "GET" {
		methods = append(methods, "HEAD")
	}
	sort.Strings(methods)
	return methods, routePath, true
}

func goValidPathPattern(routePath string) bool {
	if !strings.HasPrefix(routePath, "/") {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(routePath, "/"), "/")
	wildcards := map[string]bool{}
	for i, segment := range segments {
		if !strings.ContainsAny(segment, "{}") {
			continue
		}
		if segment == "{$}" {
			if i != len(segments)-1 {
				return false
			}
			continue
		}
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			return false
		}
		name := segment[1 : len(segment)-1]
		remainder := strings.HasSuffix(name, "...")
		if remainder {
			name = strings.TrimSuffix(name, "...")
		}
		if !token.IsIdentifier(name) || name == "_" || (remainder && i != len(segments)-1) || wildcards[name] {
			return false
		}
		wildcards[name] = true
	}
	return true
}

func goResolveHandler(file *goRouteFile, expr ast.Expr, packages map[string]map[string]string, methods map[string][]string, modulePath string, symbols map[string]functionSymbol, shadowed map[string]bool) (symbol, display string, source Location, ok bool) {
	for {
		switch value := expr.(type) {
		case *ast.ParenExpr:
			expr = value.X
		case *ast.CallExpr:
			if selector, isSelector := value.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "HandlerFunc" {
				alias, isAlias := selector.X.(*ast.Ident)
				if !isAlias || shadowed[alias.Name] || (file.imports[alias.Name] != "net/http" && !goIsGinImport(file.imports[alias.Name])) {
					return "", "unresolved handler", Location{}, false
				}
				if len(value.Args) != 1 {
					return "", "unresolved handler", Location{}, false
				}
				expr = value.Args[0]
				continue
			}
			return "", "unresolved handler", Location{}, false
		case *ast.UnaryExpr:
			if value.Op.String() == "&" {
				expr = value.X
				continue
			}
			return "", "unresolved handler", Location{}, false
		case *ast.Ident:
			id := packages[file.packagePath][value.Name]
			if fn, exists := symbols[id]; exists {
				return id, value.Name, Location{File: fn.File, Line: fn.Line}, true
			}
			return "", "unresolved handler", Location{}, false
		case *ast.SelectorExpr:
			if base, isIdent := value.X.(*ast.Ident); isIdent {
				if importPath := file.imports[base.Name]; importPath != "" {
					if packagePath, local := goLocalPackage(modulePath, importPath); local {
						id := packages[packagePath][value.Sel.Name]
						if fn, exists := symbols[id]; exists {
							return id, value.Sel.Name, Location{File: fn.File, Line: fn.Line}, true
						}
					}
				} else if candidates := methods[value.Sel.Name]; len(candidates) == 1 && strings.HasPrefix(candidates[0], "go:"+file.packagePath+".") {
					if fn, exists := symbols[candidates[0]]; exists {
						return candidates[0], value.Sel.Name, Location{File: fn.File, Line: fn.Line}, true
					}
				}
			}
			return "", "unresolved handler", Location{}, false
		case *ast.FuncLit:
			line := goLine(file, value.Pos())
			position := file.fset.Position(value.Pos())
			id := "go:" + file.packagePath + ".<handler@" + strconv.Itoa(position.Line) + ":" + strconv.Itoa(position.Column) + ">"
			body, err := goNodeBytes(file.fset, value)
			if err != nil {
				return "", "unresolved handler", Location{}, false
			}
			issues := goUnknownFunctionCalls(file, value.Body, id, packages)
			if goSymbolIssueCount(symbols)+len(issues) > api.RouteImpactMaxSymbolIssues {
				return "", "unresolved handler", Location{}, false
			}
			symbols[id] = functionSymbol{Name: id, File: file.path, Line: line, Hash: contentHash(body), References: goReferencesInNode(file, value, packages, methods, modulePath), Issues: issues}
			return id, "anonymous", Location{File: file.path, Line: line}, true
		default:
			return "", "unresolved handler", Location{}, false
		}
	}
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
