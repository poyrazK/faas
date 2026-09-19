package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type privateNetworkResource struct {
	client *client
}

type privateNetworkModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Region        types.String `tfsdk:"region"`
	CIDR          types.String `tfsdk:"cidr"`
	AllowedCIDRs  types.Set    `tfsdk:"allowed_cidrs"`
	FirewallRules types.List   `tfsdk:"firewall_rules"`
	Status        types.String `tfsdk:"status"`
	StatusDetail  types.String `tfsdk:"status_detail"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

type privateNetworkFirewallRuleModel struct {
	Direction types.String `tfsdk:"direction"`
	Protocol  types.String `tfsdk:"protocol"`
	CIDRs     types.Set    `tfsdk:"cidrs"`
	Ports     types.Set    `tfsdk:"ports"`
}

var privateNetworkFirewallRuleObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"direction": types.StringType,
		"protocol":  types.StringType,
		"cidrs":     types.SetType{ElemType: types.StringType},
		"ports":     types.SetType{ElemType: types.StringType},
	},
}

func newPrivateNetworkResource() resource.Resource {
	return &privateNetworkResource{}
}

func (r *privateNetworkResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_private_network"
}

func (r *privateNetworkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale private network identifier.",
				MarkdownDescription: "Stable Gregale private network identifier.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				Description:         "Stable private network name.",
				MarkdownDescription: "Stable private network name.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"region": schema.StringAttribute{
				Required:            true,
				Description:         "Gregale placement region for the private network.",
				MarkdownDescription: "Gregale placement region for the private network.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"cidr": schema.StringAttribute{
				Required:            true,
				Description:         "RFC1918 IPv4 network range from /16 through /28.",
				MarkdownDescription: "RFC1918 IPv4 network range from `/16` through `/28`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"allowed_cidrs": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Description:         "Optional private IPv4 policy ranges admitted symmetrically for ingress and egress. Empty preserves allow-all behavior.",
				MarkdownDescription: "Optional private IPv4 policy ranges admitted symmetrically for ingress and egress. Empty preserves allow-all behavior.",
			},
			"firewall_rules": schema.ListNestedAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Optional protocol and port allow rules applied to every attachment. Empty preserves allow-all behavior.",
				MarkdownDescription: "Optional protocol and port allow rules applied to every attachment. Empty preserves allow-all behavior.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"direction": schema.StringAttribute{
							Required:            true,
							Description:         "Traffic direction: ingress or egress.",
							MarkdownDescription: "Traffic direction: `ingress` or `egress`.",
						},
						"protocol": schema.StringAttribute{
							Required:            true,
							Description:         "Network protocol: tcp, udp, or icmp.",
							MarkdownDescription: "Network protocol: `tcp`, `udp`, or `icmp`.",
						},
						"cidrs": schema.SetAttribute{
							Optional:            true,
							Computed:            true,
							ElementType:         types.StringType,
							Description:         "Optional source CIDRs for ingress or destination CIDRs for egress. Empty means the entire network CIDR.",
							MarkdownDescription: "Optional source CIDRs for ingress or destination CIDRs for egress. Empty means the entire network CIDR.",
						},
						"ports": schema.SetAttribute{
							Optional:            true,
							Computed:            true,
							ElementType:         types.StringType,
							Description:         "TCP or UDP ports and inclusive ranges such as 443 or 8000-8080. Omit for ICMP.",
							MarkdownDescription: "TCP or UDP ports and inclusive ranges such as `443` or `8000-8080`. Omit for ICMP.",
						},
					},
				},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				Description:         "Network status: ready or error.",
				MarkdownDescription: "Network status: `ready` or `error`.",
			},
			"status_detail": schema.StringAttribute{
				Computed:            true,
				Description:         "Latest private network provisioning detail.",
				MarkdownDescription: "Latest private network provisioning detail.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 network creation timestamp.",
				MarkdownDescription: "RFC3339 network creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 network update timestamp.",
				MarkdownDescription: "RFC3339 network update timestamp.",
			},
		},
		Description:         "Manage a Gregale-owned provider-neutral private network.",
		MarkdownDescription: "Manage a Gregale-owned provider-neutral private network.",
	}
}

func (r *privateNetworkResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	configured, ok := req.ProviderData.(*client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client, got %T", req.ProviderData))
		return
	}
	r.client = configured
}

func (r *privateNetworkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid private network import ID", "Use the stable Gregale network ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *privateNetworkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a private network.")
		return
	}
	var plan privateNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request, diags := privateNetworkRequestFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createPrivateNetwork(ctx, request)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale private network", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkModel(ctx, &resp.State, out, plan)...)
}

func (r *privateNetworkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a private network.")
		return
	}
	var state privateNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getPrivateNetwork(ctx, state.ID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale private network", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkModel(ctx, &resp.State, out, state)...)
}

func (r *privateNetworkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a private network.")
		return
	}
	var plan privateNetworkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	allowedCIDRs, diags := setStringsFromModel(ctx, plan.AllowedCIDRs)
	resp.Diagnostics.Append(diags...)
	firewallRules, firewallDiags := firewallRulesFromModel(ctx, plan.FirewallRules)
	resp.Diagnostics.Append(firewallDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.updatePrivateNetworkPolicy(ctx, plan.ID.ValueString(), privateNetworkPolicyRequest{AllowedCIDRs: allowedCIDRs, FirewallRules: firewallRules})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale private network policy", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkModel(ctx, &resp.State, out, plan)...)
}

func (r *privateNetworkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a private network.")
		return
	}
	var state privateNetworkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deletePrivateNetwork(ctx, state.ID.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale private network", err)
	}
}

func setPrivateNetworkModel(ctx context.Context, state *tfsdk.State, out privateNetworkResponse, fallback privateNetworkModel) diag.Diagnostics {
	allowedCIDRs, diags := types.SetValueFrom(ctx, types.StringType, out.AllowedCIDRs)
	if diags.HasError() {
		return diags
	}
	firewallRules, firewallDiags := firewallRulesValueFromAPI(ctx, out.FirewallRules)
	diags.Append(firewallDiags...)
	if diags.HasError() {
		return diags
	}
	model := privateNetworkModel{
		ID:            remoteString(out.ID, fallback.ID),
		Name:          remoteString(out.Name, fallback.Name),
		Region:        remoteString(out.Region, fallback.Region),
		CIDR:          remoteString(out.CIDR, fallback.CIDR),
		AllowedCIDRs:  allowedCIDRs,
		FirewallRules: firewallRules,
		Status:        types.StringValue(out.Status),
		StatusDetail:  types.StringValue(out.StatusDetail),
		CreatedAt:     stringPointerValue(out.CreatedAt),
		UpdatedAt:     stringPointerValue(out.UpdatedAt),
	}
	return state.Set(ctx, &model)
}

func privateNetworkRequestFromModel(ctx context.Context, model privateNetworkModel) (privateNetworkRequest, diag.Diagnostics) {
	allowedCIDRs, diags := setStringsFromModel(ctx, model.AllowedCIDRs)
	firewallRules, firewallDiags := firewallRulesFromModel(ctx, model.FirewallRules)
	diags.Append(firewallDiags...)
	return privateNetworkRequest{
		Name:          model.Name.ValueString(),
		Region:        model.Region.ValueString(),
		CIDR:          model.CIDR.ValueString(),
		AllowedCIDRs:  allowedCIDRs,
		FirewallRules: firewallRules,
	}, diags
}

func firewallRulesFromModel(ctx context.Context, value types.List) ([]privateNetworkFirewallRule, diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	var models []privateNetworkFirewallRuleModel
	var diags diag.Diagnostics
	diags.Append(value.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return nil, diags
	}
	rules := make([]privateNetworkFirewallRule, 0, len(models))
	for _, model := range models {
		cidrs, cidrDiags := setStringsFromModel(ctx, model.CIDRs)
		ports, portDiags := setStringsFromModel(ctx, model.Ports)
		diags.Append(cidrDiags...)
		diags.Append(portDiags...)
		rules = append(rules, privateNetworkFirewallRule{
			Direction: model.Direction.ValueString(),
			Protocol:  model.Protocol.ValueString(),
			CIDRs:     cidrs,
			Ports:     ports,
		})
	}
	return rules, diags
}

func firewallRulesValueFromAPI(ctx context.Context, rules []privateNetworkFirewallRule) (types.List, diag.Diagnostics) {
	models := make([]privateNetworkFirewallRuleModel, 0, len(rules))
	var diags diag.Diagnostics
	for _, rule := range rules {
		cidrs, cidrDiags := types.SetValueFrom(ctx, types.StringType, rule.CIDRs)
		ports, portDiags := types.SetValueFrom(ctx, types.StringType, rule.Ports)
		diags.Append(cidrDiags...)
		diags.Append(portDiags...)
		models = append(models, privateNetworkFirewallRuleModel{
			Direction: types.StringValue(rule.Direction),
			Protocol:  types.StringValue(rule.Protocol),
			CIDRs:     cidrs,
			Ports:     ports,
		})
	}
	value, valueDiags := types.ListValueFrom(ctx, privateNetworkFirewallRuleObjectType, models)
	diags.Append(valueDiags...)
	return value, diags
}

func setStringsFromModel(ctx context.Context, value types.Set) ([]string, diag.Diagnostics) {
	values := make([]string, 0)
	var diags diag.Diagnostics
	if !value.IsNull() && !value.IsUnknown() {
		diags.Append(value.ElementsAs(ctx, &values, false)...)
	}
	return values, diags
}
