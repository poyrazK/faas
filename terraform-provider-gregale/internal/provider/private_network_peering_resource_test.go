package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestPrivateNetworkPeeringResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkPeeringResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("private network peering schema diagnostics: %v", diags)
	}
}
