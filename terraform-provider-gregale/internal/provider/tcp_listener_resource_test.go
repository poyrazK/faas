package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestTCPListenerResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newTCPListenerResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("TCP listener resource schema diagnostics: %v", diags)
	}
}
