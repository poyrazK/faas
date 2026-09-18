package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestCronResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newCronResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("cron resource schema diagnostics: %v", diags)
	}
}
