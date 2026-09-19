package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestPrivateNetworkResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("private network resource schema diagnostics: %v", diags)
	}
}

func TestPrivateNetworkResourceSchemaIncludesMutablePolicy(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attribute, ok := resp.Schema.Attributes["allowed_cidrs"]
	if !ok {
		t.Fatal("private network schema is missing allowed_cidrs")
	}
	if !attribute.IsOptional() || !attribute.IsComputed() {
		t.Fatalf("allowed_cidrs attribute = %#v, want optional and computed", attribute)
	}
}
