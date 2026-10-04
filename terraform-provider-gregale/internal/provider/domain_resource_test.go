package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestDomainResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newDomainResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}
}

// ADR-520: routing records reach Terraform state; the TXT proof does not leave
// the sensitive txt_record attribute.
func TestSetDomainModelRoutingRecords(t *testing.T) {
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	newDomainResource().Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil)}
	out := domainResponse{
		Domain: "shop.example.com", AppID: "app-1", ChallengeToken: "tok",
		DNSRecords: []domainDNSRecord{
			{Type: "TXT", Name: "_faas-verify.shop.example.com", Value: "tok", Purpose: "verification"},
			{Type: "CNAME", Name: "shop.example.com", Value: "edge.gregale.dev", Purpose: "routing"},
			{Type: "A", Name: "shop.example.com", Value: "203.0.113.10", Purpose: "routing", Alternative: true},
		},
	}
	if diags := setDomainModel(ctx, &state, out, domainModel{}); diags.HasError() {
		t.Fatalf("setDomainModel: %v", diags)
	}
	var got domainModel
	if diags := state.Get(ctx, &got); diags.HasError() {
		t.Fatalf("state.Get: %v", diags)
	}
	var records []domainRoutingRecordModel
	if diags := got.RoutingRecords.ElementsAs(ctx, &records, false); diags.HasError() {
		t.Fatalf("ElementsAs: %v", diags)
	}
	if len(records) != 2 {
		t.Fatalf("routing records = %d, want 2 (TXT excluded): %+v", len(records), records)
	}
	if records[0].Type.ValueString() != "CNAME" || records[0].Value.ValueString() != "edge.gregale.dev" || records[0].Alternative.ValueBool() {
		t.Fatalf("first record = %+v", records[0])
	}
	if records[1].Type.ValueString() != "A" || !records[1].Alternative.ValueBool() {
		t.Fatalf("second record = %+v", records[1])
	}
}
