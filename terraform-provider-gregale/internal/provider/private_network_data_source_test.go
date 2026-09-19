package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestPrivateNetworkDataSourceSchemaIsValid(t *testing.T) {
	var resp datasource.SchemaResponse
	newPrivateNetworkDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("private network data source schema diagnostics: %v", diags)
	}
}
