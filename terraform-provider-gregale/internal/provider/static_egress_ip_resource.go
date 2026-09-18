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

type staticEgressIPResource struct {
	client *client
}

type staticEgressIPModel struct {
	AppSlug     types.String `tfsdk:"app_slug"`
	IP          types.String `tfsdk:"ip"`
	SetAt       types.String `tfsdk:"set_at"`
	PlanCap     types.Int64  `tfsdk:"plan_cap"`
	PlanAllowed types.Bool   `tfsdk:"plan_allowed"`
}

func newStaticEgressIPResource() resource.Resource {
	return &staticEgressIPResource{}
}

func (r *staticEgressIPResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_static_egress_ip"
}

func (r *staticEgressIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app whose outbound traffic should use the pinned IP.",
				MarkdownDescription: "Slug of the Gregale app whose outbound traffic should use the pinned IP.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ip": schema.StringAttribute{
				Required:            true,
				Description:         "Public IPv4 address to use for the app's outbound traffic. The address must be provisioned for the Gregale host.",
				MarkdownDescription: "Public IPv4 address to use for the app's outbound traffic. The address must be provisioned for the Gregale host.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"set_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when Gregale pinned the IP.",
				MarkdownDescription: "RFC3339 timestamp when Gregale pinned the IP.",
			},
			"plan_cap": schema.Int64Attribute{
				Computed:            true,
				Description:         "Maximum number of static egress IPs allowed per app by the account plan.",
				MarkdownDescription: "Maximum number of static egress IPs allowed per app by the account plan.",
			},
			"plan_allowed": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether the account plan permits static egress IPs.",
				MarkdownDescription: "Whether the account plan permits static egress IPs.",
			},
		},
		Description:         "Pin a stable public IPv4 address for a Gregale app's outbound traffic.",
		MarkdownDescription: "Pin a stable public IPv4 address for a Gregale app's outbound traffic.",
	}
}

func (r *staticEgressIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *staticEgressIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid static egress IP import ID", "Use the format <app-slug>.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), req.ID)...)
}

func (r *staticEgressIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a static egress IP.")
		return
	}
	var plan staticEgressIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.setStaticEgressIP(ctx, plan.AppSlug.ValueString(), plan.IP.ValueString())
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not pin Gregale static egress IP", err)
		return
	}
	resp.Diagnostics.Append(setStaticEgressIPModel(ctx, &resp.State, out, plan)...)
}

func (r *staticEgressIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a static egress IP.")
		return
	}
	var state staticEgressIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getStaticEgressIP(ctx, state.AppSlug.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale static egress IP", err)
		return
	}
	if out.IP == nil || *out.IP == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setStaticEgressIPModel(ctx, &resp.State, out, state)...)
}

func (r *staticEgressIPResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Static egress IP updates require replacement",
		"The app and pinned IP are immutable in Terraform; changing either should plan a replacement.",
	)
}

func (r *staticEgressIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a static egress IP.")
		return
	}
	var state staticEgressIPModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.clearStaticEgressIP(ctx, state.AppSlug.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not clear Gregale static egress IP", err)
	}
}

func setStaticEgressIPModel(ctx context.Context, state *tfsdk.State, out staticEgressIPResponse, fallback staticEgressIPModel) diag.Diagnostics {
	appSlug := fallback.AppSlug.ValueString()
	model := staticEgressIPModel{
		AppSlug:     types.StringValue(appSlug),
		IP:          remoteStringPointer(out.IP, fallback.IP),
		SetAt:       stringPointerValue(out.SetAt),
		PlanCap:     types.Int64Value(int64(out.PlanCap)),
		PlanAllowed: types.BoolValue(out.PlanAllowed),
	}
	return state.Set(ctx, &model)
}

func stringPointerValue(value *string) types.String {
	if value == nil {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

func remoteStringPointer(value *string, fallback types.String) types.String {
	if value == nil {
		return fallback
	}
	return types.StringValue(*value)
}
