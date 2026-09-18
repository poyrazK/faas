package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestDeploymentResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newDeploymentResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("deployment resource schema diagnostics: %v", diags)
	}
}
