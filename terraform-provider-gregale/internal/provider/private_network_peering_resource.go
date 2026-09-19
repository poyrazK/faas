package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type privateNetworkPeeringResource struct {
	client *client
}

type privateNetworkPeeringModel struct {
	ID            types.String `tfsdk:"id"`
	NetworkID     types.String `tfsdk:"network_id"`
	PeerNetworkID types.String `tfsdk:"peer_network_id"`
	Region        types.String `tfsdk:"region"`
	Status        types.String `tfsdk:"status"`
	StatusDetail  types.String `tfsdk:"status_detail"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func newPrivateNetworkPeeringResource() resource.Resource {
	return &privateNetworkPeeringResource{}
}

func (r *privateNetworkPeeringResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_private_network_peering"
}

func (r *privateNetworkPeeringResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale private network peering identifier.",
				MarkdownDescription: "Stable Gregale private network peering identifier.",
			},
			"network_id": schema.StringAttribute{
				Required:            true,
				Description:         "Stable Gregale private network identifier that owns this peering.",
				MarkdownDescription: "Stable Gregale private network identifier that owns this peering.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"peer_network_id": schema.StringAttribute{
				Required:            true,
				Description:         "Stable Gregale private network identifier to peer with.",
				MarkdownDescription: "Stable Gregale private network identifier to peer with.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"region": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale placement region shared by the peered networks.",
				MarkdownDescription: "Gregale placement region shared by the peered networks.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				Description:         "Peering status: pending, ready, or error.",
				MarkdownDescription: "Peering status: `pending`, `ready`, or `error`.",
			},
			"status_detail": schema.StringAttribute{
				Computed:            true,
				Description:         "Latest private network peering reconciliation detail.",
				MarkdownDescription: "Latest private network peering reconciliation detail.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 peering creation timestamp.",
				MarkdownDescription: "RFC3339 peering creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 peering update timestamp.",
				MarkdownDescription: "RFC3339 peering update timestamp.",
			},
		},
		Description:         "Manage a provider-neutral peering between two Gregale private networks.",
		MarkdownDescription: "Manage a provider-neutral peering between two Gregale private networks.",
	}
}

func (r *privateNetworkPeeringResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *privateNetworkPeeringResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	networkID, peeringID, ok := strings.Cut(req.ID, "/")
	if !ok || networkID == "" || peeringID == "" || strings.Contains(peeringID, "/") {
		resp.Diagnostics.AddError("Invalid private network peering import ID", "Use the format <network-id>/<peering-id>.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), peeringID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("network_id"), networkID)...)
}

func (r *privateNetworkPeeringResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a private network peering.")
		return
	}
	var plan privateNetworkPeeringModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createPrivateNetworkPeering(ctx, plan.NetworkID.ValueString(), privateNetworkPeeringRequest{PeerNetworkID: plan.PeerNetworkID.ValueString()})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale private network peering", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkPeeringModel(ctx, &resp.State, out, plan)...)
}

func (r *privateNetworkPeeringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a private network peering.")
		return
	}
	var state privateNetworkPeeringModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getPrivateNetworkPeering(ctx, state.NetworkID.ValueString(), state.ID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale private network peering", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkPeeringModel(ctx, &resp.State, out, state)...)
}

func (r *privateNetworkPeeringResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Private network peering replacement required", "Changing network_id or peer_network_id requires replacing the peering.")
}

func (r *privateNetworkPeeringResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a private network peering.")
		return
	}
	var state privateNetworkPeeringModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deletePrivateNetworkPeering(ctx, state.NetworkID.ValueString(), state.ID.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale private network peering", err)
	}
}

func setPrivateNetworkPeeringModel(ctx context.Context, state *tfsdk.State, out privateNetworkPeeringResponse, fallback privateNetworkPeeringModel) diag.Diagnostics {
	model := privateNetworkPeeringModel{
		ID:            remoteString(out.ID, fallback.ID),
		NetworkID:     remoteString(out.NetworkID, fallback.NetworkID),
		PeerNetworkID: remoteString(out.PeerNetworkID, fallback.PeerNetworkID),
		Region:        remoteString(out.Region, fallback.Region),
		Status:        types.StringValue(out.Status),
		StatusDetail:  types.StringValue(out.StatusDetail),
		CreatedAt:     stringPointerValue(out.CreatedAt),
		UpdatedAt:     stringPointerValue(out.UpdatedAt),
	}
	return state.Set(ctx, &model)
}
