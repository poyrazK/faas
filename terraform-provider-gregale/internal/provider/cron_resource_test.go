package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCronResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newCronResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("cron resource schema diagnostics: %v", diags)
	}
}

func TestCronFailureRulesJSONValidationAndCanonicalization(t *testing.T) {
	valid, diags := cronFailureRulesFromModel(types.StringValue(`{"version":1,"rules":[],"unmatched_failure":"retry","uncertain_outcome":"hold"}`))
	if diags.HasError() {
		t.Fatalf("valid failure rules diagnostics: %v", diags)
	}
	if got := string(valid); got != `{"rules":[],"uncertain_outcome":"hold","unmatched_failure":"retry","version":1}` {
		t.Fatalf("canonical JSON = %s", got)
	}
	_, diags = cronFailureRulesFromModel(types.StringValue(`[]`))
	if !diags.HasError() {
		t.Fatal("expected a JSON array to be rejected")
	}
}
