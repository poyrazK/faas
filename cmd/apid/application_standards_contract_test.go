package main

import (
	"path/filepath"
	"reflect"
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
	properties := flagSchemaProperties(spec.Schemas["ApplicationStandardDefinition"], spec)
	if !reflect.DeepEqual(definitionFields, properties) {
		t.Fatalf("definition vocabulary differs: %v %v", definitionFields, properties)
	}
}
