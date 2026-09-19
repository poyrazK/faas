package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestStaticEgressIPResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newStaticEgressIPResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("static egress IP resource schema diagnostics: %v", diags)
	}
}
