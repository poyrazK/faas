package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestSecretResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newSecretResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("secret resource schema diagnostics: %v", diags)
	}
}
