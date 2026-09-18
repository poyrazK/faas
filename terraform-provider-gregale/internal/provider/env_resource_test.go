package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestEnvResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newEnvResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("env resource schema diagnostics: %v", diags)
	}
}
