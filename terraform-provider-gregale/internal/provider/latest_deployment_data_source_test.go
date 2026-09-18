package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestLatestDeploymentDataSourceSchemaIsValid(t *testing.T) {
	var resp datasource.SchemaResponse
	newLatestDeploymentDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("latest deployment data source schema diagnostics: %v", diags)
	}
}
