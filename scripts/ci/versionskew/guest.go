package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// defaultGuestTypes are the JSON documents host and guest exchange through
// drive1 (pkg/api): app.json is written by vmmd and read by a guest-init that
// may be a release older (restored from a snapshot or an older base image);
// build.json and build-done.json cross the builder VM boundary both ways.
var defaultGuestTypes = []string{"AppManifest", "BuildManifest", "BuildDone"}

// guestSchema maps a JSON path ("AppManifest.healthcheck.interval_s",
// "[]" for slice elements, "{}" for map values) to its wire shape.
type guestSchema map[string]guestField

type guestField struct {
	Shape     string // JSON shape: string, integer, object, array, map, bytes, custom:<T>, ...
	OmitEmpty bool
}

// loadGuest describes the root types' JSON encoding by reading the package's
// Go source at ref ("" = working tree). It parses rather than type-checks, so
// an old release tag needs no build: struct fields, json tags and the package's
// own named types are enough to know what encoding/json writes.
func loadGuest(repo, pkgDir, ref string, roots []string) (guestSchema, error) {
	dir := strings.TrimSuffix(pkgDir, "/")
	files, err := readTree(repo, ref, dir, func(name string) bool {
		return path.Dir(name) == dir && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	})
	if err != nil {
		return nil, err
	}
	return describeGuest(files, roots)
}

func describeGuest(files map[string][]byte, roots []string) (guestSchema, error) {
	fset := token.NewFileSet()
	types := map[string]ast.Expr{}
	codec := map[string]bool{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, files[name], parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						if _, dup := types[ts.Name.Name]; !dup {
							types[ts.Name.Name] = ts.Type
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					continue
				}
				switch d.Name.Name {
				case "MarshalJSON", "UnmarshalJSON", "MarshalText", "UnmarshalText":
					codec[receiverName(d.Recv.List[0].Type)] = true
				}
			}
		}
	}
	d := &guestDescriber{types: types, codec: codec, schema: guestSchema{}}
	for _, root := range roots {
		if _, ok := types[root]; !ok {
			return nil, fmt.Errorf("guest protocol root type %s not found", root)
		}
		d.describe(root, &ast.Ident{Name: root}, false, false, map[string]bool{})
	}
	return d.schema, nil
}

type guestDescriber struct {
	types  map[string]ast.Expr
	codec  map[string]bool
	schema guestSchema
}

// describe records the shape at p and recurses into objects, slice elements
// and map values. visiting holds the named types on the current path, so a
// recursive type is recorded once instead of expanding forever.
func (d *guestDescriber) describe(p string, expr ast.Expr, omit, stringOpt bool, visiting map[string]bool) {
	node, named, leaf := d.resolve(expr)
	if leaf != "" {
		if stringOpt {
			leaf += ",string"
		}
		d.schema[p] = guestField{Shape: leaf, OmitEmpty: omit}
		return
	}
	for _, n := range named {
		if visiting[n] {
			d.schema[p] = guestField{Shape: "recursive:" + n, OmitEmpty: omit}
			return
		}
	}
	for _, n := range named {
		visiting[n] = true
		defer delete(visiting, n)
	}
	switch t := node.(type) {
	case *ast.StructType:
		d.schema[p] = guestField{Shape: "object", OmitEmpty: omit}
		d.fields(p, t, visiting)
	case *ast.ArrayType:
		d.schema[p] = guestField{Shape: "array", OmitEmpty: omit}
		d.describe(p+"[]", t.Elt, false, false, visiting)
	case *ast.MapType:
		d.schema[p] = guestField{Shape: "map", OmitEmpty: omit}
		d.describe(p+"{}", t.Value, false, false, visiting)
	}
}

// resolve follows pointers and the package's own named types to the node
// that decides the JSON shape. It returns either that composite node (a
// struct, slice or map) with the named types it passed through, or the
// shape of a leaf.
func (d *guestDescriber) resolve(expr ast.Expr) (node ast.Expr, named []string, leaf string) {
	for range 64 {
		switch t := expr.(type) {
		case *ast.StarExpr:
			// A pointer encodes as its target, or null, which decodes to zero.
			expr = t.X
		case *ast.ParenExpr:
			expr = t.X
		case *ast.Ident:
			if s, ok := basicShape(t.Name); ok {
				return nil, nil, s
			}
			if d.codec[t.Name] {
				return nil, nil, "custom:" + t.Name
			}
			under, ok := d.types[t.Name]
			if !ok {
				return nil, nil, "named:" + t.Name
			}
			named = append(named, t.Name)
			expr = under
		case *ast.SelectorExpr:
			return nil, nil, selectorShape(t)
		case *ast.ArrayType:
			if id, ok := t.Elt.(*ast.Ident); ok && t.Len == nil && (id.Name == "byte" || id.Name == "uint8") {
				return nil, nil, "bytes" // base64 string
			}
			return t, named, ""
		case *ast.MapType, *ast.StructType:
			return t, named, ""
		case *ast.InterfaceType:
			return nil, nil, "any"
		default:
			return nil, nil, fmt.Sprintf("unsupported:%T", expr)
		}
	}
	return nil, nil, "unsupported:named-type-chain"
}

func (d *guestDescriber) fields(p string, st *ast.StructType, visiting map[string]bool) {
	for _, field := range st.Fields.List {
		key, opts, skip := jsonTag(field)
		if skip {
			continue
		}
		omit := hasOpt(opts, "omitempty") || hasOpt(opts, "omitzero")
		stringOpt := hasOpt(opts, "string")
		if len(field.Names) == 0 {
			if key == "" && d.promote(p, field.Type, visiting) {
				continue
			}
			if key == "" {
				key = embeddedName(field.Type)
			}
			d.describe(p+"."+key, field.Type, omit, stringOpt, visiting)
			continue
		}
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			k := key
			if k == "" {
				k = name.Name
			}
			d.describe(p+"."+k, field.Type, omit, stringOpt, visiting)
		}
	}
}

// promote inlines an untagged embedded struct's fields into p, as
// encoding/json does. It reports false when the embedded type is not a
// struct, which encoding/json treats as an ordinary field.
func (d *guestDescriber) promote(p string, expr ast.Expr, visiting map[string]bool) bool {
	node, named, _ := d.resolve(expr)
	st, ok := node.(*ast.StructType)
	if !ok {
		return false
	}
	for _, n := range named {
		if visiting[n] {
			return true
		}
	}
	for _, n := range named {
		visiting[n] = true
		defer delete(visiting, n)
	}
	d.fields(p, st, visiting)
	return true
}

func hasOpt(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == want {
			return true
		}
	}
	return false
}

func basicShape(name string) (string, bool) {
	switch name {
	case "string":
		return "string", true
	case "bool":
		return "bool", true
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
		return "integer", true
	case "float32", "float64":
		return "number", true
	case "any", "error":
		return "any", true
	}
	return "", false
}

func selectorShape(t *ast.SelectorExpr) string {
	pkg, _ := t.X.(*ast.Ident)
	name := t.Sel.Name
	if pkg == nil {
		return "external:" + name
	}
	switch pkg.Name + "." + name {
	case "time.Time":
		return "string(time)"
	case "time.Duration":
		return "integer"
	case "json.RawMessage":
		return "any"
	case "json.Number":
		return "number"
	}
	return "external:" + pkg.Name + "." + name
}

func jsonTag(field *ast.Field) (key, opts string, skip bool) {
	if field.Tag == nil {
		return "", "", false
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return "", "", false
	}
	tag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return "", "", false
	}
	if tag == "-" {
		return "", "", true
	}
	key, opts, _ = strings.Cut(tag, ",")
	return key, opts, false
}

func embeddedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return embeddedName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return "?"
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return ""
}

// compareGuest reports keys an N-1 peer reads that cur no longer writes in
// the same shape, and added keys that change the bytes an N-1 peer sees even
// when the feature is unused.
func compareGuest(old, cur guestSchema) []finding {
	var out []finding
	reported := map[string]bool{}
	paths := make([]string, 0, len(old))
	for p := range old {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if underReported(p, reported) {
			continue
		}
		o := old[p]
		n, ok := cur[p]
		switch {
		case !ok:
			reported[p] = true
			out = append(out, finding{Key: "guest:" + p + ":removed", Message: fmt.Sprintf("JSON key %s was removed or renamed; a peer on the previous release still reads it. Keep the key (stop reading it first), and add new keys instead.", p)})
		case n.Shape != o.Shape:
			reported[p] = true
			out = append(out, finding{Key: "guest:" + p + ":type-changed", Message: fmt.Sprintf("JSON key %s changed shape from %s to %s; a peer on the previous release cannot decode it. Add a new key instead.", p, o.Shape, n.Shape)})
		}
	}
	added := make([]string, 0)
	for p := range cur {
		if _, ok := old[p]; !ok {
			added = append(added, p)
		}
	}
	sort.Strings(added)
	for _, p := range added {
		parent := parentPath(p)
		if po, ok := old[parent]; !ok || po.Shape != cur[parent].Shape {
			continue // inside a new object: the parent's own rule covers it
		}
		if !cur[p].OmitEmpty {
			out = append(out, finding{Key: "guest:" + p + ":not-omitempty", Message: fmt.Sprintf("new JSON key %s must be tagged omitempty (or omitzero) so documents that do not use it stay byte-identical for the previous release.", p)})
		}
	}
	return out
}

func underReported(p string, reported map[string]bool) bool {
	for q := parentPath(p); q != ""; q = parentPath(q) {
		if reported[q] {
			return true
		}
	}
	return false
}

// parentPath strips the last segment: "A.b.c" -> "A.b", "A.b[]" -> "A.b",
// "A.m{}.x" -> "A.m{}".
func parentPath(p string) string {
	if strings.HasSuffix(p, "[]") || strings.HasSuffix(p, "{}") {
		return p[:len(p)-2]
	}
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[:i]
	}
	return ""
}
