package routeimpact

import (
	"go/ast"
)

type goRouteFunction struct {
	file *goRouteFile
	decl *ast.FuncDecl
	id   string
}

type goRouteParameter struct {
	position int
	name     string
	typeExpr ast.Expr
}

func goRouteParameters(function *ast.FuncDecl) ([]goRouteParameter, bool) {
	parameters := []goRouteParameter{}
	if function.Type.Params == nil {
		return parameters, false
	}
	variadic := false
	for fieldIndex, field := range function.Type.Params.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok && fieldIndex == len(function.Type.Params.List)-1 {
			variadic = true
		}
		if len(field.Names) == 0 {
			parameters = append(parameters, goRouteParameter{position: len(parameters), typeExpr: field.Type})
			continue
		}
		for _, name := range field.Names {
			parameters = append(parameters, goRouteParameter{position: len(parameters), name: name.Name, typeExpr: field.Type})
		}
	}
	return parameters, variadic
}

func goRouteFunctionIndex(files []*goRouteFile) map[string]map[string]goRouteFunction {
	functions := map[string]map[string]goRouteFunction{}
	duplicates := map[string]bool{}
	for _, file := range files {
		if functions[file.packagePath] == nil {
			functions[file.packagePath] = map[string]goRouteFunction{}
		}
		for _, declaration := range file.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name == nil || function.Body == nil || function.Recv != nil {
				continue
			}
			name := function.Name.Name
			if _, exists := functions[file.packagePath][name]; exists {
				delete(functions[file.packagePath], name)
				duplicates[file.packagePath+"\x00"+name] = true
				continue
			}
			if duplicates[file.packagePath+"\x00"+name] {
				continue
			}
			functions[file.packagePath][name] = goRouteFunction{
				file: file, decl: function, id: goFunctionID(file.packagePath, "", name),
			}
		}
	}
	return functions
}

func goRouteCallFunction(file *goRouteFile, expression ast.Expr, functions map[string]map[string]goRouteFunction, modulePath string, shadowed map[string]bool) (goRouteFunction, bool) {
	for {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.IndexExpr:
			expression = value.X
		case *ast.IndexListExpr:
			expression = value.X
		default:
			goto resolve
		}
	}

resolve:
	switch value := expression.(type) {
	case *ast.Ident:
		if shadowed[value.Name] {
			return goRouteFunction{}, false
		}
		function, ok := functions[file.packagePath][value.Name]
		return function, ok
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok || shadowed[alias.Name] {
			return goRouteFunction{}, false
		}
		importPath := file.imports[alias.Name]
		packagePath, local := goLocalPackage(modulePath, importPath)
		if !local {
			return goRouteFunction{}, false
		}
		function, ok := functions[packagePath][value.Sel.Name]
		return function, ok
	default:
		return goRouteFunction{}, false
	}
}
