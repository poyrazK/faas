package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type privateNetworkDataSource struct {
	client *client
}

type privateNetworkDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	NetworkID     types.String `tfsdk:"network_id"`
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

func newPrivateNetworkDataSource() datasource.DataSource {
	return &privateNetworkDataSource{}
}

func (d *privateNetworkDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "gregale_private_network"
}

func (d *privateNetworkDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale private network identifier.",
				MarkdownDescription: "Stable Gregale private network identifier.",
			},
			"network_id": schema.StringAttribute{
				Required:            true,
				Description:         "Stable Gregale private network identifier to look up.",
				MarkdownDescription: "Stable Gregale private network identifier to look up.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable private network name.",
				MarkdownDescription: "Stable private network name.",
			},
			"region": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale placement region for the private network.",
				MarkdownDescription: "Gregale placement region for the private network.",
			},
			"cidr": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC1918 IPv4 network range.",
				MarkdownDescription: "RFC1918 IPv4 network range.",
			},
			"allowed_cidrs": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				Description:         "Private IPv4 policy ranges admitted symmetrically for ingress and egress.",
				MarkdownDescription: "Private IPv4 policy ranges admitted symmetrically for ingress and egress.",
			},
			"firewall_rules": schema.ListNestedAttribute{
				Computed:            true,
				Description:         "Protocol and port allow rules applied to every attachment.",
				MarkdownDescription: "Protocol and port allow rules applied to every attachment.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"direction": schema.StringAttribute{
							Computed:            true,
							Description:         "Traffic direction: ingress or egress.",
							MarkdownDescription: "Traffic direction: `ingress` or `egress`.",
						},
						"protocol": schema.StringAttribute{
							Computed:            true,
							Description:         "Network protocol: tcp, udp, or icmp.",
							MarkdownDescription: "Network protocol: `tcp`, `udp`, or `icmp`.",
						},
						"cidrs": schema.SetAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							Description:         "Source CIDRs for ingress or destination CIDRs for egress.",
							MarkdownDescription: "Source CIDRs for ingress or destination CIDRs for egress.",
						},
						"ports": schema.SetAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							Description:         "TCP or UDP ports and inclusive ranges; empty for ICMP.",
							MarkdownDescription: "TCP or UDP ports and inclusive ranges; empty for ICMP.",
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
		Description:         "Reads an existing Gregale-owned provider-neutral private network without managing its lifecycle.",
		MarkdownDescription: "Reads an existing Gregale-owned provider-neutral private network without managing its lifecycle.",
	}
}

func (d *privateNetworkDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	configured, ok := req.ProviderData.(*client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *client, got %T", req.ProviderData))
		return
	}
	d.client = configured
}

func (d *privateNetworkDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a private network.")
		return
	}
	var query privateNetworkDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	networkID := query.NetworkID.ValueString()
	out, err := d.client.getPrivateNetwork(ctx, networkID)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale private network", err)
		return
	}

	state, diags := privateNetworkDataSourceState(ctx, out, networkID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func privateNetworkDataSourceState(ctx context.Context, out privateNetworkResponse, networkID string) (privateNetworkDataSourceModel, diag.Diagnostics) {
	allowedCIDRs, diags := types.SetValueFrom(ctx, types.StringType, out.AllowedCIDRs)
	firewallRules, firewallDiags := firewallRulesValueFromAPI(ctx, out.FirewallRules)
	diags.Append(firewallDiags...)

	return privateNetworkDataSourceModel{
		ID:            types.StringValue(firstNonEmpty(out.ID, networkID)),
		NetworkID:     types.StringValue(networkID),
		Name:          types.StringValue(out.Name),
		Region:        types.StringValue(out.Region),
		CIDR:          types.StringValue(out.CIDR),
		AllowedCIDRs:  allowedCIDRs,
		FirewallRules: firewallRules,
		Status:        types.StringValue(out.Status),
		StatusDetail:  types.StringValue(out.StatusDetail),
		CreatedAt:     stringPointerValue(out.CreatedAt),
		UpdatedAt:     stringPointerValue(out.UpdatedAt),
	}, diags
}
