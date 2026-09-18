package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestDeploymentDataSourceSchemaIsValid(t *testing.T) {
	var resp datasource.SchemaResponse
	newDeploymentDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("deployment data source schema diagnostics: %v", diags)
	}
}
