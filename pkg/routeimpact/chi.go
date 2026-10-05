package routeimpact

import (
	"errors"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func goIsChiImport(importPath string) bool {
	return importPath == "github.com/go-chi/chi/v5" || importPath == "github.com/go-chi/chi/v4"
}

func goIsChiType(file *goRouteFile, expr ast.Expr, shadowed map[string]bool) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goIsChiType(file, value.X, shadowed)
	case *ast.StarExpr:
		return goIsChiType(file, value.X, shadowed)
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		return ok && !shadowed[alias.Name] && (value.Sel.Name == "Router" || value.Sel.Name == "Mux") && goIsChiImport(file.imports[alias.Name])
	}
	return false
}

func goIsChiValue(file *goRouteFile, expr ast.Expr, shadowed map[string]bool) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "NewRouter" || len(call.Args) != 0 {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok && !shadowed[alias.Name] && goIsChiImport(file.imports[alias.Name])
}

func goPackageChiVariables(files []*goRouteFile) map[string]map[string]bool {
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
						if goIsChiValue(file, right, nil) || goIsChiType(file, value.Type, nil) || goIsKnownChi(known[file.packagePath], right) || goIsChiRouterExpr(file, right, known[file.packagePath], nil) {
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

func goLocalChiVariables(file *goRouteFile, scope ast.Node, inherited map[string]bool) (map[string]bool, map[string]bool) {
	known := map[string]bool{}
	for name := range inherited {
		known[name] = true
	}
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
	var params *ast.FieldList
	var body *ast.BlockStmt
	switch function := scope.(type) {
	case *ast.FuncDecl:
		params, body = function.Type.Params, function.Body
		goAddFieldNames(shadowed, function.Type.Params)
		goAddFieldNames(shadowed, function.Type.Results)
		goAddFieldNames(shadowed, function.Recv)
	case *ast.FuncLit:
		params, body = function.Type.Params, function.Body
		goAddFieldNames(shadowed, function.Type.Params)
		goAddFieldNames(shadowed, function.Type.Results)
	}
	for name := range shadowed {
		delete(known, name)
	}
	if params != nil && body != nil {
		routerParameters := map[string]bool{}
		for _, field := range params.List {
			if !goIsChiType(file, field.Type, nil) {
				continue
			}
			for _, name := range field.Names {
				routerParameters[name.Name] = true
			}
		}
		bodyBindings := map[string]bool{}
		ast.Inspect(body, func(node ast.Node) bool {
			if _, ok := node.(*ast.FuncLit); ok {
				return false
			}
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
			}
			return true
		})
		for name := range routerParameters {
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
			var lhs, rhs []ast.Expr
			var typeExpr ast.Expr
			switch value := node.(type) {
			case *ast.AssignStmt:
				lhs, rhs = value.Lhs, value.Rhs
			case *ast.ValueSpec:
				for _, name := range value.Names {
					lhs = append(lhs, name)
				}
				rhs, typeExpr = value.Values, value.Type
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
				if goIsChiValue(file, right, shadowed) || goIsChiType(file, typeExpr, shadowed) || goIsKnownChi(known, right) || goIsChiRouterExpr(file, right, known, shadowed) {
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

func goIsKnownChi(known map[string]bool, expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && known[ident.Name]
}

func goIsChiRouterExpr(file *goRouteFile, expr ast.Expr, known, shadowed map[string]bool) bool {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goIsChiRouterExpr(file, value.X, known, shadowed)
	case *ast.Ident:
		return known[value.Name]
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "With" && len(value.Args) > 0 {
			return goIsChiRouterExpr(file, selector.X, known, shadowed)
		}
		return goIsChiValue(file, value, shadowed)
	}
	return false
}

func indexGoChiRoutes(index *sourceIndex, files []*goRouteFile, packageChiVars map[string]map[string]bool, packageFuncs map[string]map[string]string, methodsByPackage map[string]map[string][]string, modulePath string, duplicates map[string]bool) error {
	routerParents := map[string]string{}
	mounts := []goChiMount{}
	routeStart := len(index.Routes)
	routeOwners := []string{}
	functions := goRouteFunctionIndex(files)
	calledHelpers := goChiCalledRouterHelpers(files, functions, packageChiVars, modulePath)
	visitedHelpers := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			functionID := goFunctionID(file.packagePath, "", function.Name.Name)
			if calledHelpers[functionID] && len(goChiRouterParameters(file, function)) > 0 {
				continue
			}
			routers, shadowed := goLocalChiVariables(file, function, packageChiVars[file.packagePath])
			middleware := goChiInitialMiddlewareState(file, function, routers, packageChiVars[file.packagePath], shadowed)
			walk := goChiRouteWalkState{
				functions: functions, packageRouters: packageChiVars, methodsByPackage: methodsByPackage, modulePath: modulePath,
				called: calledHelpers, visited: visitedHelpers, stack: map[string]bool{functionID: true},
			}
			goWalkChiRoutes(index, file, function.Body, "", nil, 0, routers, shadowed, packageFuncs, methodsByPackage[file.packagePath], modulePath, duplicates, middleware, routerParents, &mounts, &routeOwners, walk)
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
			if calledHelpers[functionID] && len(goChiRouterParameters(file, function)) > 0 && !visitedHelpers[functionID] {
				index.Issues = append(index.Issues, Issue{Code: "unresolved_chi_route_helper", File: file.path, Line: goLine(file, function.Pos()), Symbol: function.Name.Name, Message: "A Chi route helper has no reachable static callsite with a resolvable router context."})
			}
		}
	}
	if err := goChiExpandMountedRoutes(index, mounts, routerParents, routeStart, routeOwners); err != nil {
		return err
	}
	if len(index.Routes) > api.RouteImpactMaxRoutes {
		return errors.New("go source exceeds the route impact route limit")
	}
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return errors.New("go source exceeds the route impact issue limit")
	}
	return nil
}

type goChiMiddlewareState struct {
	routerIDs  map[string]string
	symbols    map[string][]string
	unresolved map[string]bool
}

type goChiRouteWalkState struct {
	functions        map[string]map[string]goRouteFunction
	packageRouters   map[string]map[string]bool
	methodsByPackage map[string]map[string][]string
	modulePath       string
	called           map[string]bool
	visited          map[string]bool
	stack            map[string]bool
	helperDepth      int
	scopeRouterID    string
}

func goChiRouterParameters(file *goRouteFile, function *ast.FuncDecl) []goRouteParameter {
	parameters, _ := goRouteParameters(function)
	routers := []goRouteParameter{}
	for _, parameter := range parameters {
		if goIsChiType(file, parameter.typeExpr, nil) {
			routers = append(routers, parameter)
		}
	}
	return routers
}

func goChiCalledRouterHelpers(files []*goRouteFile, functions map[string]map[string]goRouteFunction, packageRouters map[string]map[string]bool, modulePath string) map[string]bool {
	called := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			_, shadowed := goLocalChiVariables(file, function, packageRouters[file.packagePath])
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				target, ok := goRouteCallFunction(file, call.Fun, functions, modulePath, shadowed)
				if ok && len(goChiRouterParameters(target.file, target.decl)) > 0 {
					called[target.id] = true
				}
				return true
			})
		}
	}
	return called
}

type goChiMount struct {
	parentID         string
	targetID         string
	prefix           string
	file             string
	line             int
	registrationHash string
	parentMiddleware []string
}

func goChiInitialMiddlewareState(file *goRouteFile, function *ast.FuncDecl, routers, packageRouters, shadowed map[string]bool) goChiMiddlewareState {
	state := goChiMiddlewareState{routerIDs: map[string]string{}, symbols: map[string][]string{}, unresolved: map[string]bool{}}
	for name := range routers {
		id := goChiRouterIdentity(file, function.Pos(), name)
		if packageRouters[name] && !shadowed[name] {
			id = goChiPackageRouterIdentity(file.packagePath, name)
		}
		state.routerIDs[name] = id
		state.symbols[id] = []string{}
	}
	return state
}

func goChiPackageRouterIdentity(packagePath, name string) string {
	return "gochi:" + packagePath + ":package:" + name
}

func (state goChiMiddlewareState) clone() goChiMiddlewareState {
	copy := goChiMiddlewareState{routerIDs: map[string]string{}, symbols: map[string][]string{}, unresolved: map[string]bool{}}
	for name, id := range state.routerIDs {
		copy.routerIDs[name] = id
	}
	for id, symbols := range state.symbols {
		copy.symbols[id] = append([]string{}, symbols...)
	}
	for id, unresolved := range state.unresolved {
		copy.unresolved[id] = unresolved
	}
	return copy
}

func goChiRouterIdentity(file *goRouteFile, position token.Pos, name string) string {
	return "gochi:" + file.path + ":" + strconv.Itoa(file.fset.Position(position).Offset) + ":" + name
}

func goChiBaseRouterName(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return goChiBaseRouterName(value.X)
	case *ast.Ident:
		return value.Name, true
	case *ast.CallExpr:
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "With" {
			return goChiBaseRouterName(selector.X)
		}
	}
	return "", false
}

func goChiWithArguments(expr ast.Expr) []ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			return goChiWithArguments(paren.X)
		}
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "With" {
		return nil
	}
	return append(goChiWithArguments(selector.X), call.Args...)
}

func goChiResolveMiddleware(index *sourceIndex, file *goRouteFile, args []ast.Expr, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, shadowed map[string]bool) ([]string, bool) {
	symbols := []string{}
	unresolved := false
	for _, expression := range args {
		if len(symbols) > api.RouteImpactMaxSymbols {
			unresolved = true
			break
		}
		symbol, _, _, ok := goResolveHandler(file, expression, packageFuncs, methods, modulePath, index.Symbols, shadowed)
		if !ok {
			unresolved = true
			continue
		}
		symbols = append(symbols, symbol)
	}
	return symbols, unresolved
}

func goChiJoinMiddleware(values ...[]string) []string {
	joined := []string{}
	for _, group := range values {
		remaining := api.RouteImpactMaxSymbols + 1 - len(joined)
		if remaining <= 0 {
			return joined
		}
		if len(group) > remaining {
			joined = append(joined, group[:remaining]...)
			return joined
		}
		joined = append(joined, group...)
	}
	return joined
}

func goChiMiddlewareForReceiver(state goChiMiddlewareState, receiver ast.Expr, index *sourceIndex, file *goRouteFile, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, shadowed map[string]bool) (symbols []string, unresolved, directUnresolved bool, routerID string) {
	name, ok := goChiBaseRouterName(receiver)
	if !ok {
		return nil, true, true, ""
	}
	routerID = state.routerIDs[name]
	if routerID == "" {
		return nil, true, true, ""
	}
	symbols = append(symbols, state.symbols[routerID]...)
	unresolved = state.unresolved[routerID]
	withSymbols, withUnresolved := goChiResolveMiddleware(index, file, goChiWithArguments(receiver), packageFuncs, methods, modulePath, shadowed)
	return goChiJoinMiddleware(symbols, withSymbols), unresolved || withUnresolved, withUnresolved, routerID
}

func goChiRouterParameterNames(file *goRouteFile, callback *ast.FuncLit, shadowed map[string]bool) []string {
	names := []string{}
	if callback.Type.Params == nil {
		return names
	}
	for _, field := range callback.Type.Params.List {
		if !goIsChiType(file, field.Type, shadowed) {
			continue
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func goChiUpdateRouterAliases(state *goChiMiddlewareState, file *goRouteFile, routers map[string]bool, lhs, rhs []ast.Expr, position token.Pos, index *sourceIndex, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, shadowed map[string]bool) {
	for i, target := range lhs {
		name, ok := target.(*ast.Ident)
		if !ok || name.Name == "_" || !routers[name.Name] {
			continue
		}
		var expression ast.Expr
		if len(lhs) == len(rhs) {
			expression = rhs[i]
		} else if len(lhs) == 1 && len(rhs) == 1 {
			expression = rhs[0]
		}
		if expression == nil {
			continue
		}
		if goIsChiValue(file, expression, shadowed) {
			id := goChiRouterIdentity(file, position, name.Name)
			state.routerIDs[name.Name] = id
			state.symbols[id] = []string{}
			delete(state.unresolved, id)
			continue
		}
		base, hasBase := goChiBaseRouterName(expression)
		if !hasBase || !routers[base] || state.routerIDs[base] == "" {
			id := goChiRouterIdentity(file, position, name.Name)
			state.routerIDs[name.Name] = id
			state.symbols[id] = []string{}
			state.unresolved[id] = true
			index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_router", File: file.path, Line: goLine(file, position), Message: "A Chi router value is reassigned from an expression that cannot be traced; middleware scope may be incomplete."})
			continue
		}
		baseID := state.routerIDs[base]
		withArgs := goChiWithArguments(expression)
		withSymbols, withUnresolved := goChiResolveMiddleware(index, file, withArgs, packageFuncs, methods, modulePath, shadowed)
		if withUnresolved {
			index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: goLine(file, position), Message: "A Chi middleware expression does not resolve to a local function; route impact may miss middleware behavior."})
		}
		if len(withArgs) == 0 {
			state.routerIDs[name.Name] = baseID
			continue
		}
		id := goChiRouterIdentity(file, position, name.Name)
		state.routerIDs[name.Name] = id
		state.symbols[id] = goChiJoinMiddleware(state.symbols[baseID], withSymbols)
		state.unresolved[id] = state.unresolved[baseID] || withUnresolved
	}
}

func goWalkChiRoutes(index *sourceIndex, file *goRouteFile, node ast.Node, prefix string, routeRootPrefixes []string, depth int, routers, shadowed map[string]bool, packageFuncs map[string]map[string]string, methods map[string][]string, modulePath string, duplicates map[string]bool, middleware goChiMiddlewareState, routerParents map[string]string, mounts *[]goChiMount, routeOwners *[]string, walk goChiRouteWalkState) goChiMiddlewareState {
	middleware = middleware.clone()
	ast.Inspect(node, func(child ast.Node) bool {
		if len(index.Routes) > api.RouteImpactMaxRoutes || len(index.Issues) > api.RouteImpactMaxIssues {
			return false
		}
		if _, ok := child.(*ast.FuncLit); ok {
			return false
		}
		switch value := child.(type) {
		case *ast.AssignStmt:
			goChiUpdateRouterAliases(&middleware, file, routers, value.Lhs, value.Rhs, value.Pos(), index, packageFuncs, methods, modulePath, shadowed)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, 0, len(value.Names))
			for _, name := range value.Names {
				lhs = append(lhs, name)
			}
			goChiUpdateRouterAliases(&middleware, file, routers, lhs, value.Values, value.Pos(), index, packageFuncs, methods, modulePath, shadowed)
		}
		call, ok := child.(*ast.CallExpr)
		if !ok {
			return true
		}
		if target, found := goRouteCallFunction(file, call.Fun, walk.functions, modulePath, shadowed); found {
			parameters, variadic := goRouteParameters(target.decl)
			routerParameters := goChiRouterParameters(target.file, target.decl)
			if len(routerParameters) > 0 {
				walk.called[target.id] = true
				walk.visited[target.id] = true
				line := goLine(file, call.Pos())
				if walk.helperDepth >= api.RouteImpactMaxGraphDepth {
					index.Issues = append(index.Issues, Issue{Code: "chi_route_helper_depth_exceeded", File: file.path, Line: line, Message: "Nested Chi route registration helpers exceed the static analysis depth limit."})
					return false
				}
				if walk.stack[target.id] {
					index.Issues = append(index.Issues, Issue{Code: "chi_route_helper_cycle", File: file.path, Line: line, Message: "A recursive Chi route registration helper cannot be expanded safely."})
					return false
				}
				if variadic || len(call.Args) != len(parameters) {
					index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_helper", File: file.path, Line: line, Message: "A Chi route helper call has variadic or mismatched arguments and cannot be bound to router parameters."})
					return false
				}
				childRouters, childShadowed := goLocalChiVariables(target.file, target.decl, walk.packageRouters[target.file.packagePath])
				childMiddleware := middleware.clone()
				type helperRouterBinding struct {
					actualID string
					helperID string
				}
				bindings := []helperRouterBinding{}
				helperPrefix := ""
				helperRootPrefixes := []string{}
				helperScopeID := ""
				pathContextSet := false
				pathContextKey := ""
				bindingFailed := false
				for _, parameter := range routerParameters {
					if parameter.name == "" {
						continue
					}
					argument := call.Args[parameter.position]
					if !goIsChiRouterExpr(file, argument, routers, shadowed) {
						bindingFailed = true
						break
					}
					symbols, unresolved, directUnresolved, actualID := goChiMiddlewareForReceiver(middleware, argument, index, file, packageFuncs, methods, modulePath, shadowed)
					if actualID == "" {
						bindingFailed = true
						break
					}
					helperID := actualID
					if len(goChiWithArguments(argument)) > 0 {
						helperID = goChiRouterIdentity(target.file, call.Pos(), parameter.name+"-helper")
						childMiddleware.symbols[helperID] = append([]string{}, symbols...)
						childMiddleware.unresolved[helperID] = unresolved || directUnresolved
						routerParents[helperID] = actualID
					}
					childMiddleware.routerIDs[parameter.name] = helperID
					childRouters[parameter.name] = true
					bindings = append(bindings, helperRouterBinding{actualID: actualID, helperID: helperID})
					candidatePrefix, candidateRoots, candidateScope := "", []string{}, ""
					if actualID == walk.scopeRouterID {
						candidatePrefix, candidateRoots, candidateScope = prefix, routeRootPrefixes, helperID
					}
					candidateKey := candidatePrefix + "\x00" + strings.Join(candidateRoots, "\x00")
					if !pathContextSet {
						helperPrefix, helperRootPrefixes, helperScopeID = candidatePrefix, candidateRoots, candidateScope
						pathContextKey = candidateKey
						pathContextSet = true
					} else if pathContextKey != candidateKey {
						bindingFailed = true
						break
					}
				}
				if bindingFailed {
					index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_helper", File: file.path, Line: line, Message: "A Chi route helper argument has an unknown router origin or combines groups with different path prefixes."})
					return false
				}
				if helperScopeID == "" && len(bindings) > 0 {
					helperScopeID = bindings[0].helperID
				}
				childWalk := walk
				childWalk.helperDepth++
				childWalk.scopeRouterID = helperScopeID
				childWalk.stack = make(map[string]bool, len(walk.stack)+1)
				for id := range walk.stack {
					childWalk.stack[id] = true
				}
				childWalk.stack[target.id] = true
				updated := goWalkChiRoutes(index, target.file, target.decl.Body, helperPrefix, helperRootPrefixes, depth, childRouters, childShadowed, packageFuncs, walk.methodsByPackage[target.file.packagePath], modulePath, duplicates, childMiddleware, routerParents, mounts, routeOwners, childWalk)
				for _, binding := range bindings {
					if binding.helperID != binding.actualID {
						continue
					}
					if symbols, ok := updated.symbols[binding.actualID]; ok {
						middleware.symbols[binding.actualID] = append([]string{}, symbols...)
					}
					middleware.unresolved[binding.actualID] = updated.unresolved[binding.actualID]
				}
				return false
			}
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !goIsChiRouterExpr(file, selector.X, routers, shadowed) {
			return true
		}
		line := goLine(file, call.Pos())
		receiverMiddleware, receiverUnresolved, directMiddlewareUnresolved, routerID := goChiMiddlewareForReceiver(middleware, selector.X, index, file, packageFuncs, methods, modulePath, shadowed)
		switch selector.Sel.Name {
		case "Route", "Group":
			if depth >= api.RouteImpactMaxGraphDepth {
				index.Issues = append(index.Issues, Issue{Code: "chi_route_group_depth_exceeded", File: file.path, Line: line, Message: "Nested Chi route groups exceed the static route analysis depth limit."})
				return false
			}
			prefixArg, handlerArg := -1, 0
			childPrefix := prefix
			if selector.Sel.Name == "Route" {
				prefixArg, handlerArg = 0, 1
			} else {
				handlerArg = 0
			}
			if handlerArg >= len(call.Args) {
				index.Issues = append(index.Issues, Issue{Code: "invalid_chi_route_group", File: file.path, Line: line, Message: "A Chi route group does not include a static callback."})
				return false
			}
			if prefixArg >= 0 {
				groupPrefix, literal := goStringLiteral(call.Args[prefixArg])
				if !literal {
					index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_prefix", File: file.path, Line: line, Message: "A dynamic Chi route prefix prevents nested paths from being mapped to stable routes."})
					return false
				}
				var valid bool
				childPrefix, valid = goChiJoinPath(prefix, groupPrefix, true)
				if !valid {
					index.Issues = append(index.Issues, Issue{Code: "unsupported_chi_route_prefix", File: file.path, Line: line, Message: "A Chi route prefix is outside the supported static path model."})
					return false
				}
			}
			callback, ok := call.Args[handlerArg].(*ast.FuncLit)
			if !ok {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_group", File: file.path, Line: line, Message: "A Chi route group callback is not an inline function and cannot be resolved statically."})
				return false
			}
			childRouters, childShadowed := goLocalChiVariables(file, callback, routers)
			childRouteRootPrefixes := routeRootPrefixes
			if selector.Sel.Name == "Route" {
				childRouteRootPrefixes = append(append([]string{}, routeRootPrefixes...), childPrefix)
			}
			childMiddleware := middleware.clone()
			childScopeRouterID := ""
			for _, name := range goChiRouterParameterNames(file, callback, childShadowed) {
				id := goChiRouterIdentity(file, call.Pos(), name)
				childMiddleware.routerIDs[name] = id
				childMiddleware.symbols[id] = append([]string{}, receiverMiddleware...)
				childMiddleware.unresolved[id] = receiverUnresolved
				if childScopeRouterID == "" {
					childScopeRouterID = id
				}
				if routerID != "" {
					routerParents[id] = routerID
				}
			}
			if directMiddlewareUnresolved {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: line, Message: "A Chi middleware expression does not resolve to a local function; route impact may miss middleware behavior."})
			}
			childWalk := walk
			childWalk.scopeRouterID = childScopeRouterID
			goWalkChiRoutes(index, file, callback.Body, childPrefix, childRouteRootPrefixes, depth+1, childRouters, childShadowed, packageFuncs, methods, modulePath, duplicates, childMiddleware, routerParents, mounts, routeOwners, childWalk)
			return false
		case "Use":
			useSymbols, useUnresolved := goChiResolveMiddleware(index, file, call.Args, packageFuncs, methods, modulePath, shadowed)
			if len(goChiWithArguments(selector.X)) > 0 {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: line, Message: "Middleware added through a temporary Chi With router cannot be safely scoped to later registrations."})
				return false
			}
			if directMiddlewareUnresolved || useUnresolved || routerID == "" {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: line, Message: "A Chi middleware expression does not resolve to a local function; route impact may miss middleware behavior."})
			}
			if routerID != "" {
				middleware.symbols[routerID] = goChiJoinMiddleware(receiverMiddleware, useSymbols)
				middleware.unresolved[routerID] = middleware.unresolved[routerID] || receiverUnresolved || useUnresolved
				if len(middleware.symbols[routerID]) > api.RouteImpactMaxSymbols {
					middleware.unresolved[routerID] = true
					index.Issues = append(index.Issues, Issue{Code: "chi_middleware_limit_exceeded", File: file.path, Line: line, Message: "A Chi middleware chain exceeds the static dependency report limit."})
				}
			}
			return false
		case "With":
			return false
		case "Mount":
			if len(call.Args) < 2 {
				index.Issues = append(index.Issues, Issue{Code: "invalid_chi_mount", File: file.path, Line: line, Message: "A Chi Mount call is missing its path or handler."})
				return false
			}
			mountPrefix, literal := goStringLiteral(call.Args[0])
			if !literal {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_mount_prefix", File: file.path, Line: line, Message: "A dynamic Chi mount prefix cannot be mapped to a stable route path."})
				return false
			}
			mountPrefix, valid := goChiJoinPath(prefix, mountPrefix, false)
			if !valid {
				index.Issues = append(index.Issues, Issue{Code: "unsupported_chi_mount_prefix", File: file.path, Line: line, Message: "A Chi mount prefix is outside the supported static path model."})
				return false
			}
			target, targetIsIdent := call.Args[1].(*ast.Ident)
			targetID := ""
			if targetIsIdent && routers[target.Name] {
				targetID = middleware.routerIDs[target.Name]
			}
			if targetID == "" || routerID == "" {
				index.Issues = append(index.Issues, Issue{Code: "unmodeled_chi_mount", File: file.path, Line: line, Message: "A mounted Chi handler does not resolve to a local router variable."})
				return false
			}
			if directMiddlewareUnresolved {
				index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: line, Message: "A Chi middleware expression does not resolve to a local function; route impact may miss middleware behavior."})
			}
			registration, _ := goNodeBytes(file.fset, call)
			*mounts = append(*mounts, goChiMount{parentID: routerID, targetID: targetID, prefix: mountPrefix, file: file.path,
				line: line, registrationHash: contentHash(registration), parentMiddleware: append([]string{}, receiverMiddleware...)})
			return false
		}
		if directMiddlewareUnresolved {
			index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_middleware", File: file.path, Line: line, Message: "A Chi middleware expression does not resolve to a local function; route impact may miss middleware behavior."})
		}
		if !goChiEndpointMethod(selector.Sel.Name) {
			return true
		}
		routeMethods, pathArg, handlerArg, unbounded, validCall := goChiRegistration(file, selector.Sel.Name, call.Args, shadowed)
		if !validCall {
			index.Issues = append(index.Issues, Issue{Code: "invalid_chi_route_registration", File: file.path, Line: line, Message: "A Chi route registration has an unsupported method or is missing its path or handler."})
			return false
		}
		pattern, literal := goStringLiteral(call.Args[pathArg])
		if !literal {
			index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_pattern", File: file.path, Line: line, Message: "A dynamic Chi route pattern cannot be mapped to a stable route."})
			return false
		}
		routePath, valid := goChiJoinPath(prefix, pattern, false)
		if !valid {
			index.Issues = append(index.Issues, Issue{Code: "unsupported_chi_route_pattern", File: file.path, Line: line, Message: "A Chi route pattern is outside the supported static path model."})
			return false
		}
		if unbounded {
			index.Issues = append(index.Issues, Issue{Code: "unbounded_chi_route_methods", File: file.path, Line: line, Message: "A Chi Handle registration accepts methods beyond the standard set represented in the report."})
		}
		if len(receiverMiddleware) > api.RouteImpactMaxSymbols {
			index.Issues = append(index.Issues, Issue{Code: "chi_middleware_limit_exceeded", File: file.path, Line: line, Message: "A Chi middleware chain exceeds the static dependency report limit."})
			return false
		}
		handler, handlerName, handlerLocation, handlerOK := goResolveHandler(file, call.Args[handlerArg], packageFuncs, methods, modulePath, index.Symbols, shadowed)
		if !handlerOK {
			index.Issues = append(index.Issues, Issue{Code: "dynamic_chi_route_handler", File: file.path, Line: line, Message: "A Chi route handler is dynamic or does not resolve to a local function."})
		}
		registration, _ := goNodeBytes(file.fset, call)
		routePaths := goChiRoutePathVariants(routePath, pattern, routeRootPrefixes)
		for _, registeredPath := range routePaths {
			for _, method := range routeMethods {
				key := method + "\x00" + registeredPath
				if duplicates[key] {
					index.Issues = append(index.Issues, Issue{Code: "duplicate_go_route", File: file.path, Line: line, Message: "A method and path pair is registered more than once; route attribution is ambiguous."})
					continue
				}
				duplicates[key] = true
				loc := Location{File: file.path, Line: line}
				route := Route{Method: method, Path: registeredPath, Handler: handlerName, Source: handlerLocation, Registration: loc,
					RegistrationHash: contentHash(registration), ContextFiles: []string{file.path}, DependencyFiles: []string{},
					HandlerSymbol: handler, DependencySymbols: append([]string{}, receiverMiddleware...), FallbackFiles: []string{}}
				if route.Source.File == "" {
					route.Source = loc
				}
				index.Routes = append(index.Routes, route)
				*routeOwners = append(*routeOwners, routerID)
				if len(index.Routes) > api.RouteImpactMaxRoutes {
					return false
				}
			}
		}
		return false
	})
	return middleware
}

func goChiRoutePathVariants(routePath, pattern string, routeRootPrefixes []string) []string {
	paths := []string{routePath}
	if pattern != "/" || len(routeRootPrefixes) == 0 {
		return paths
	}
	rootPrefix := strings.TrimSuffix(routeRootPrefixes[len(routeRootPrefixes)-1], "/")
	if rootPrefix == "" || rootPrefix == "/" || routePath != rootPrefix+"/" {
		return paths
	}
	return append(paths, rootPrefix)
}

func goChiExpandMountedRoutes(index *sourceIndex, mounts []goChiMount, routerParents map[string]string, routeStart int, routeOwners []string) error {
	if len(mounts) == 0 {
		return nil
	}
	baseRoutes := append([]Route{}, index.Routes[routeStart:]...)
	baseOwners := append([]string{}, routeOwners...)
	routes := append([]Route{}, index.Routes[:routeStart]...)
	routes = append(routes, baseRoutes...)
	seenRoutes := map[string]bool{}
	for _, route := range routes {
		seenRoutes[route.Method+"\x00"+route.Path] = true
	}
	issueSeen := map[string]bool{}
	addIssue := func(code, file string, line int, message string) {
		key := code + "\x00" + file + "\x00" + strconv.Itoa(line)
		if issueSeen[key] {
			return
		}
		issueSeen[key] = true
		if len(index.Issues) > api.RouteImpactMaxIssues {
			return
		}
		index.Issues = append(index.Issues, Issue{Code: code, File: file, Line: line, Message: message})
	}
	for _, mount := range mounts {
		found := false
		for routeIndex := range baseRoutes {
			if goChiRouterWithin(baseOwners[routeIndex], mount.targetID, routerParents) {
				found = true
				break
			}
		}
		if !found {
			addIssue("unmodeled_chi_mount", mount.file, mount.line, "A mounted Chi router has no statically indexed routes in the selected source root.")
		}
	}
	var expand func(Route, string, map[int]bool, int) error
	expand = func(route Route, owner string, used map[int]bool, depth int) error {
		for i, mount := range mounts {
			if !goChiRouterWithin(owner, mount.targetID, routerParents) {
				continue
			}
			if used[i] {
				addIssue("chi_mount_cycle", mount.file, mount.line, "Nested Chi mounts form a cycle that cannot be expanded statically.")
				continue
			}
			if depth >= api.RouteImpactMaxGraphDepth {
				addIssue("chi_mount_depth_exceeded", mount.file, mount.line, "Nested Chi mounts exceed the static route analysis depth limit.")
				continue
			}
			mountedPaths, valid := goChiMountedPathVariants(mount.prefix, route.Path)
			if !valid {
				addIssue("unsupported_chi_mount_path", mount.file, mount.line, "A mounted Chi route exceeds the supported static path model.")
				continue
			}
			mountedRoutes := make([]Route, 0, len(mountedPaths))
			for _, mountedPath := range mountedPaths {
				mounted := route
				mounted.Path = mountedPath
				mounted.DependencySymbols = goChiJoinMiddleware(mount.parentMiddleware, route.DependencySymbols)
				if len(mounted.DependencySymbols) > api.RouteImpactMaxSymbols {
					addIssue("chi_middleware_limit_exceeded", mount.file, mount.line, "A mounted Chi middleware chain exceeds the static dependency report limit.")
					continue
				}
				mounted.ContextFiles = append(append([]string{}, route.ContextFiles...), mount.file)
				mounted.RegistrationHash = contentHash([]byte(route.RegistrationHash + "\x00" + mount.registrationHash))
				key := mounted.Method + "\x00" + mounted.Path
				if seenRoutes[key] {
					addIssue("duplicate_go_route", mount.file, mount.line, "A mounted Chi route duplicates a method and path pair; route attribution is ambiguous.")
					continue
				}
				seenRoutes[key] = true
				routes = append(routes, mounted)
				mountedRoutes = append(mountedRoutes, mounted)
				if len(routes) > api.RouteImpactMaxRoutes {
					return errors.New("go source exceeds the route impact route limit")
				}
			}
			nextUsed := make(map[int]bool, len(used)+1)
			for usedIndex := range used {
				nextUsed[usedIndex] = true
			}
			nextUsed[i] = true
			for _, mounted := range mountedRoutes {
				if err := expand(mounted, mount.parentID, nextUsed, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for routeIndex, route := range baseRoutes {
		if err := expand(route, baseOwners[routeIndex], map[int]bool{}, 0); err != nil {
			return err
		}
	}
	index.Routes = routes
	if len(index.Issues) > api.RouteImpactMaxIssues {
		return errors.New("go source exceeds the route impact issue limit")
	}
	return nil
}

// Chi registers both pattern and pattern+"/" for a mount without a trailing
// slash. A route at the mounted router's root therefore has both paths.
func goChiMountedPathVariants(prefix, routePath string) ([]string, bool) {
	path, valid := goChiJoinPath(prefix, routePath, false)
	if !valid {
		return nil, false
	}
	paths := []string{path}
	if routePath == "/" && prefix != "/" && !strings.HasSuffix(prefix, "/") {
		paths = append(paths, prefix)
	}
	return paths, true
}

func goChiRouterWithin(routerID, targetID string, parents map[string]string) bool {
	seen := map[string]bool{}
	for depth := 0; routerID != "" && depth < api.RouteImpactMaxGraphDepth; depth++ {
		if routerID == targetID {
			return true
		}
		if seen[routerID] {
			return false
		}
		seen[routerID] = true
		routerID = parents[routerID]
	}
	return false
}

func goChiEndpointMethod(name string) bool {
	switch name {
	case "Get", "Head", "Post", "Put", "Patch", "Delete", "Connect", "Options", "Trace", "Method", "MethodFunc", "Handle", "HandleFunc":
		return true
	}
	return false
}

func goChiRegistration(file *goRouteFile, name string, args []ast.Expr, shadowed map[string]bool) (methods []string, pathArg, handlerArg int, unbounded, valid bool) {
	switch name {
	case "Get":
		methods, pathArg, handlerArg = []string{"GET"}, 0, 1
	case "Head":
		methods, pathArg, handlerArg = []string{"HEAD"}, 0, 1
	case "Post":
		methods, pathArg, handlerArg = []string{"POST"}, 0, 1
	case "Put":
		methods, pathArg, handlerArg = []string{"PUT"}, 0, 1
	case "Patch":
		methods, pathArg, handlerArg = []string{"PATCH"}, 0, 1
	case "Delete":
		methods, pathArg, handlerArg = []string{"DELETE"}, 0, 1
	case "Connect":
		methods, pathArg, handlerArg = []string{"CONNECT"}, 0, 1
	case "Options":
		methods, pathArg, handlerArg = []string{"OPTIONS"}, 0, 1
	case "Trace":
		methods, pathArg, handlerArg = []string{"TRACE"}, 0, 1
	case "Method", "MethodFunc":
		if len(args) < 3 {
			return nil, 0, 0, false, false
		}
		method, resolved := goChiMethodValue(file, args[0], shadowed)
		if !resolved || !validMethod(method) {
			return nil, 0, 0, false, false
		}
		methods, pathArg, handlerArg = []string{method}, 1, 2
	case "Handle", "HandleFunc":
		methods, pathArg, handlerArg, unbounded = []string{"CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE"}, 0, 1, true
	default:
		return nil, 0, 0, false, false
	}
	if pathArg >= len(args) || handlerArg >= len(args) {
		return nil, 0, 0, false, false
	}
	return methods, pathArg, handlerArg, unbounded, true
}

func goChiMethodValue(file *goRouteFile, expr ast.Expr, shadowed map[string]bool) (string, bool) {
	if method, literal := goStringLiteral(expr); literal {
		return method, true
	}
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	alias, ok := selector.X.(*ast.Ident)
	if !ok || shadowed[alias.Name] || file.imports[alias.Name] != "net/http" {
		return "", false
	}
	methods := map[string]string{
		"MethodConnect": "CONNECT", "MethodDelete": "DELETE", "MethodGet": "GET", "MethodHead": "HEAD",
		"MethodOptions": "OPTIONS", "MethodPatch": "PATCH", "MethodPost": "POST", "MethodPut": "PUT", "MethodTrace": "TRACE",
	}
	method, exists := methods[selector.Sel.Name]
	return method, exists
}

func goChiJoinPath(prefix, value string, allowEmpty bool) (string, bool) {
	if value == "" && allowEmpty {
		return prefix, validMetadata(prefix)
	}
	if !strings.HasPrefix(value, "/") || !validMetadata(value) || strings.ContainsAny(value, " \t\r\n") {
		return "", false
	}
	if allowEmpty {
		if prefix == "" || prefix == "/" {
			joined := strings.TrimSuffix(value, "/")
			return joined, validMetadata(joined)
		}
		value = strings.TrimSuffix(value, "/")
		if value == "" {
			joined := strings.TrimSuffix(prefix, "/") + "/"
			return joined, validMetadata(joined)
		}
	}
	if prefix == "" || prefix == "/" {
		return value, validMetadata(value)
	}
	joined := strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(value, "/")
	return joined, validMetadata(joined)
}
