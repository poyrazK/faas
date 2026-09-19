package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestPrivateNetworkResourceSchemaIsValid(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("private network resource schema diagnostics: %v", diags)
	}
}

func TestPrivateNetworkResourceSchemaIncludesMutablePolicy(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attribute, ok := resp.Schema.Attributes["allowed_cidrs"]
	if !ok {
		t.Fatal("private network schema is missing allowed_cidrs")
	}
	if !attribute.IsOptional() || !attribute.IsComputed() {
		t.Fatalf("allowed_cidrs attribute = %#v, want optional and computed", attribute)
	}
}

func TestPrivateNetworkResourceSchemaIncludesFirewallRules(t *testing.T) {
	var resp resource.SchemaResponse
	newPrivateNetworkResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attribute, ok := resp.Schema.Attributes["firewall_rules"]
	if !ok {
		t.Fatal("private network schema is missing firewall_rules")
	}
	if !attribute.IsOptional() || !attribute.IsComputed() {
		t.Fatalf("firewall_rules attribute = %#v, want optional and computed", attribute)
	}
}

func TestPrivateNetworkFirewallRulesRoundTrip(t *testing.T) {
	ctx := context.Background()
	want := []privateNetworkFirewallRule{{
		Direction: "ingress",
		Protocol:  "tcp",
		CIDRs:     []string{"10.20.0.0/24"},
		Ports:     []string{"443", "8000-8080"},
	}}

	value, diags := firewallRulesValueFromAPI(ctx, want)
	if diags.HasError() {
		t.Fatalf("firewall rule state diagnostics: %v", diags)
	}
	got, diags := firewallRulesFromModel(ctx, value)
	if diags.HasError() {
		t.Fatalf("firewall rule model diagnostics: %v", diags)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("firewall rule round trip = %#v, want %#v", got, want)
	}
}
