package apidserver_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appstandards"
)

func TestApplicationStandardSpecContracts(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fields := flagJSONFields(reflect.TypeOf(appstandards.Rule{}))
	for _, name := range []string{"LogDestination", "Signature", "SecurityPolicy", "Publisher", "CIDR", "ExtraPort"} {
		properties := flagSchemaProperties(spec.Schemas["ApplicationStandard"+name+"Rule"], spec)
		if !reflect.DeepEqual(fields, properties) {
			t.Fatalf("%s rule JSON/schema fields differ: %v %v", name, fields, properties)
		}
	}
	definitionFields := map[string]bool{}
	for _, field := range []appstandards.Field{appstandards.LogDestinations, appstandards.RequireSigned, appstandards.SecurityPolicy, appstandards.TrustedPublishers, appstandards.EgressCIDRs, appstandards.EgressExtraPorts} {
		definitionFields[string(field)] = true
	}
	for name, wire := range map[string]reflect.Type{
		"ApplicationStandardAdoption":  reflect.TypeOf(appstandards.Adoption{}),
		"ApplicationStandardSource":    reflect.TypeOf(appstandards.Source{}),
		"ApplicationStandardViolation": reflect.TypeOf(appstandards.Violation{}),
		"ApplicationStandardEffective": reflect.TypeOf(appstandards.Effective{}),
	} {
		if got := flagSchemaProperties(spec.Schemas[name], spec); !reflect.DeepEqual(got, flagJSONFields(wire)) {
			t.Fatalf("%s JSON/schema fields differ: %v", name, got)
		}
	}
	if got := flagSchemaProperties(spec.Schemas["ApplicationStandardSettings"], spec); !reflect.DeepEqual(got, definitionFields) {
		t.Fatalf("local settings vocabulary differs: %v", got)
	}
	properties := flagSchemaProperties(spec.Schemas["ApplicationStandardDefinition"], spec)
	if !reflect.DeepEqual(definitionFields, properties) {
		t.Fatalf("definition vocabulary differs: %v %v", definitionFields, properties)
	}
}

func TestApplicationStandardPublishedGoSDKContracts(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	authoritative, err := scanApplicationStandardDTOs([]string{filepath.Join(root, "pkg/api/application_standards.go"), filepath.Join(root, "pkg/api/application_standard_resources.go"), filepath.Join(root, "pkg/api/application_standard_mutations.go"), filepath.Join(root, "pkg/api/application_standard_operation_mutations.go"), filepath.Join(root, "pkg/api/application_standard_assignments.go")})
	if err != nil {
		t.Fatal(err)
	}
	published, err := scanApplicationStandardDTOs([]string{filepath.Join(root, "sdk/go/internal/api/application_standards.go")})
	if err != nil {
		t.Fatal(err)
	}
	for name, fields := range authoritative {
		if !reflect.DeepEqual(fields, published[name]) {
			t.Fatalf("published Go SDK %s wire fields differ: %v %v", name, fields, published[name])
		}
	}
	for name, wire := range map[string]reflect.Type{
		"ApplicationStandardAdoption":  reflect.TypeOf(appstandards.Adoption{}),
		"ApplicationStandardSource":    reflect.TypeOf(appstandards.Source{}),
		"ApplicationStandardViolation": reflect.TypeOf(appstandards.Violation{}),
		"ApplicationStandardEffective": reflect.TypeOf(appstandards.Effective{}),
		"ApplicationStandardRule":      reflect.TypeOf(appstandards.Rule{}),
	} {
		if !reflect.DeepEqual(flagJSONFields(wire), published[name]) {
			t.Fatalf("published Go SDK %s differs from authoritative wire type", name)
		}
	}
}

func scanApplicationStandardDTOs(paths []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, declaration := range file.Decls {
			group, ok := declaration.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, item := range group.Specs {
				typeSpec, ok := item.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				fields := map[string]bool{}
				for _, field := range structure.Fields.List {
					if field.Tag == nil {
						continue
					}
					tag, err := strconv.Unquote(field.Tag.Value)
					if err != nil {
						return nil, err
					}
					name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
					if name != "" && name != "-" {
						fields[name] = true
					}
				}
				out[typeSpec.Name.Name] = fields
			}
		}
	}
	return out, nil
}
