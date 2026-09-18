package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestPrivateNetworkAttachmentResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkAttachmentResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("private-network attachment resource schema diagnostics: %v", diags)
	}
}
