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
