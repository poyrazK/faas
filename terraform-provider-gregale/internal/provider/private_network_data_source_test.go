package provider

import (
	"context"
	"reflect"
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

func TestPrivateNetworkDataSourceSchemaIncludesFirewallRules(t *testing.T) {
	var resp datasource.SchemaResponse
	newPrivateNetworkDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	attribute, ok := resp.Schema.Attributes["firewall_rules"]
	if !ok {
		t.Fatal("private network data source schema is missing firewall_rules")
	}
	if !attribute.IsComputed() {
		t.Fatalf("firewall_rules attribute = %#v, want computed", attribute)
	}
}

func TestPrivateNetworkDataSourceStateIncludesNetworkPolicy(t *testing.T) {
	ctx := context.Background()
	createdAt := "2026-09-19T10:00:00Z"
	updatedAt := "2026-09-19T10:01:00Z"
	out := privateNetworkResponse{
		ID:           "prod-vpc",
		Name:         "production",
		Region:       "fra1",
		CIDR:         "10.20.0.0/16",
		AllowedCIDRs: []string{"10.20.0.0/24"},
		FirewallRules: []privateNetworkFirewallRule{{
			Direction: "ingress",
			Protocol:  "tcp",
			CIDRs:     []string{"10.20.0.0/24"},
			Ports:     []string{"443"},
		}},
		Status:       "ready",
		StatusDetail: "network is ready",
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}

	state, diags := privateNetworkDataSourceState(ctx, out, "fallback-id")
	if diags.HasError() {
		t.Fatalf("private network data source state diagnostics: %v", diags)
	}
	if state.ID.ValueString() != "prod-vpc" || state.NetworkID.ValueString() != "fallback-id" || state.Name.ValueString() != "production" || state.Region.ValueString() != "fra1" || state.CIDR.ValueString() != "10.20.0.0/16" {
		t.Fatalf("private network data source state identity = %+v", state)
	}
	var allowed []string
	if diags := state.AllowedCIDRs.ElementsAs(ctx, &allowed, false); diags.HasError() {
		t.Fatalf("private network data source allowed CIDRs diagnostics: %v", diags)
	}
	if !reflect.DeepEqual(allowed, []string{"10.20.0.0/24"}) {
		t.Fatalf("private network data source allowed CIDRs = %#v", allowed)
	}
	rules, diags := firewallRulesFromModel(ctx, state.FirewallRules)
	if diags.HasError() {
		t.Fatalf("private network data source firewall rule diagnostics: %v", diags)
	}
	if !reflect.DeepEqual(rules, out.FirewallRules) {
		t.Fatalf("private network data source firewall rules = %#v, want %#v", rules, out.FirewallRules)
	}
}
