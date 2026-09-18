package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestAppDataSourceSchemaIsValid(t *testing.T) {
	var resp datasource.SchemaResponse
	newAppDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("app data source schema diagnostics: %v", diags)
	}
}
