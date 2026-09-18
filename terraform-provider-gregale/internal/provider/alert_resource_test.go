package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestAlertResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newAlertResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("alert resource schema diagnostics: %v", diags)
	}
}
