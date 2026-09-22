package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestProjectEnvironmentResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newProjectEnvironmentResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("project environment resource schema diagnostics: %v", diags)
	}
}
