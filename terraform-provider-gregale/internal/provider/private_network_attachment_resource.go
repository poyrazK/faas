package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type privateNetworkAttachmentResource struct {
	client *client
}

type privateNetworkAttachmentModel struct {
	AppSlug        types.String `tfsdk:"app_slug"`
	NetworkID      types.String `tfsdk:"network_id"`
	Region         types.String `tfsdk:"region"`
	CIDRs          types.Set    `tfsdk:"cidrs"`
	AllowedCIDRs   types.Set    `tfsdk:"allowed_cidrs"`
	AttachmentID   types.String `tfsdk:"attachment_id"`
	Address        types.String `tfsdk:"address"`
	Status         types.String `tfsdk:"status"`
	StatusDetail   types.String `tfsdk:"status_detail"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	FeatureEnabled types.Bool   `tfsdk:"feature_enabled"`
	PlanAllowed    types.Bool   `tfsdk:"plan_allowed"`
	MaxCIDRs       types.Int64  `tfsdk:"max_cidrs"`
}

func newPrivateNetworkAttachmentResource() resource.Resource {
	return &privateNetworkAttachmentResource{}
}

func (r *privateNetworkAttachmentResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_private_network_attachment"
}

func (r *privateNetworkAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app to attach to a private network.",
				MarkdownDescription: "Slug of the Gregale app to attach to a private network.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"network_id": schema.StringAttribute{
				Required:            true,
				Description:         "Provider-neutral private network identifier.",
				MarkdownDescription: "Provider-neutral private network identifier.",
			},
			"region": schema.StringAttribute{
				Required:            true,
				Description:         "Private network placement region.",
				MarkdownDescription: "Private network placement region.",
			},
			"cidrs": schema.SetAttribute{
				Required:            true,
				ElementType:         types.StringType,
				Description:         "Private IPv4 destination CIDRs routed through the attachment.",
				MarkdownDescription: "Private IPv4 destination CIDRs routed through the attachment.",
			},
			"allowed_cidrs": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Description:         "Optional private IPv4 policy ranges admitted symmetrically for ingress and egress. Empty preserves allow-all behavior.",
				MarkdownDescription: "Optional private IPv4 policy ranges admitted symmetrically for ingress and egress. Empty preserves allow-all behavior.",
			},
			"attachment_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale private-network attachment identifier.",
				MarkdownDescription: "Stable Gregale private-network attachment identifier.",
			},
			"address": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable app member IPv4 address when the Gregale-owned fabric provides one.",
				MarkdownDescription: "Stable app member IPv4 address when the Gregale-owned fabric provides one.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				Description:         "Attachment status: pending, ready, or error. Pending and error remain fail-closed.",
				MarkdownDescription: "Attachment status: `pending`, `ready`, or `error`. Pending and error remain fail-closed.",
			},
			"status_detail": schema.StringAttribute{
				Computed:            true,
				Description:         "Latest attachment reconciliation detail.",
				MarkdownDescription: "Latest attachment reconciliation detail.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 attachment creation timestamp.",
				MarkdownDescription: "RFC3339 attachment creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 attachment update timestamp.",
				MarkdownDescription: "RFC3339 attachment update timestamp.",
			},
			"feature_enabled": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether private-network attachments are enabled on the cluster.",
				MarkdownDescription: "Whether private-network attachments are enabled on the cluster.",
			},
			"plan_allowed": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether the account plan permits private-network attachments.",
				MarkdownDescription: "Whether the account plan permits private-network attachments.",
			},
			"max_cidrs": schema.Int64Attribute{
				Computed:            true,
				Description:         "Maximum number of destination CIDRs allowed by the account plan.",
				MarkdownDescription: "Maximum number of destination CIDRs allowed by the account plan.",
			},
		},
		Description:         "Attach a Gregale app to a provider-neutral private network.",
		MarkdownDescription: "Attach a Gregale app to a provider-neutral private network.",
	}
}

func (r *privateNetworkAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *privateNetworkAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid private network attachment import ID", "Use the format <app-slug>.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), req.ID)...)
}

func (r *privateNetworkAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a private-network attachment.")
		return
	}
	var plan privateNetworkAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request, diags := privateNetworkAttachmentRequestFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.setPrivateNetworkAttachment(ctx, plan.AppSlug.ValueString(), request)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not request Gregale private-network attachment", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkAttachmentModel(ctx, &resp.State, out, plan)...)
}

func (r *privateNetworkAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a private-network attachment.")
		return
	}
	var state privateNetworkAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getPrivateNetworkAttachment(ctx, state.AppSlug.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale private-network attachment", err)
		return
	}
	if out.Attachment == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkAttachmentModel(ctx, &resp.State, out, state)...)
}

func (r *privateNetworkAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a private-network attachment.")
		return
	}
	var plan privateNetworkAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	request, diags := privateNetworkAttachmentRequestFromModel(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.setPrivateNetworkAttachment(ctx, plan.AppSlug.ValueString(), request)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale private-network attachment", err)
		return
	}
	resp.Diagnostics.Append(setPrivateNetworkAttachmentModel(ctx, &resp.State, out, plan)...)
}

func (r *privateNetworkAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a private-network attachment.")
		return
	}
	var state privateNetworkAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.clearPrivateNetworkAttachment(ctx, state.AppSlug.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not clear Gregale private-network attachment", err)
	}
}

func privateNetworkAttachmentRequestFromModel(ctx context.Context, model privateNetworkAttachmentModel) (privateNetworkAttachmentRequest, diag.Diagnostics) {
	cidrs := make([]string, 0)
	allowedCIDRs := make([]string, 0)
	var diags diag.Diagnostics
	if !model.CIDRs.IsNull() && !model.CIDRs.IsUnknown() {
		diags.Append(model.CIDRs.ElementsAs(ctx, &cidrs, false)...)
	}
	if !model.AllowedCIDRs.IsNull() && !model.AllowedCIDRs.IsUnknown() {
		diags.Append(model.AllowedCIDRs.ElementsAs(ctx, &allowedCIDRs, false)...)
	}
	return privateNetworkAttachmentRequest{
		NetworkID:    model.NetworkID.ValueString(),
		Region:       model.Region.ValueString(),
		CIDRs:        cidrs,
		AllowedCIDRs: allowedCIDRs,
	}, diags
}

func setPrivateNetworkAttachmentModel(ctx context.Context, state *tfsdk.State, out privateNetworkAttachmentResponse, fallback privateNetworkAttachmentModel) diag.Diagnostics {
	if out.Attachment == nil {
		return diag.Diagnostics{}
	}
	attachment := out.Attachment
	cidrs, diags := types.SetValueFrom(ctx, types.StringType, attachment.CIDRs)
	if diags.HasError() {
		return diags
	}
	allowedCIDRs, allowedDiags := types.SetValueFrom(ctx, types.StringType, attachment.AllowedCIDRs)
	diags.Append(allowedDiags...)
	if diags.HasError() {
		return diags
	}
	model := privateNetworkAttachmentModel{
		AppSlug:        fallback.AppSlug,
		NetworkID:      types.StringValue(attachment.NetworkID),
		Region:         types.StringValue(attachment.Region),
		CIDRs:          cidrs,
		AllowedCIDRs:   allowedCIDRs,
		AttachmentID:   types.StringValue(attachment.ID),
		Address:        types.StringValue(attachment.Address),
		Status:         types.StringValue(attachment.Status),
		StatusDetail:   types.StringValue(attachment.StatusDetail),
		CreatedAt:      stringPointerValue(attachment.CreatedAt),
		UpdatedAt:      stringPointerValue(attachment.UpdatedAt),
		FeatureEnabled: types.BoolValue(out.FeatureEnabled),
		PlanAllowed:    types.BoolValue(out.PlanAllowed),
		MaxCIDRs:       types.Int64Value(int64(out.MaxCIDRs)),
	}
	return state.Set(ctx, &model)
}
