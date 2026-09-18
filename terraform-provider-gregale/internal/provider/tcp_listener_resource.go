package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type tcpListenerResource struct {
	client *client
}

type tcpListenerModel struct {
	ID         types.String `tfsdk:"id"`
	AppSlug    types.String `tfsdk:"app_slug"`
	Name       types.String `tfsdk:"name"`
	GuestPort  types.Int64  `tfsdk:"guest_port"`
	PublicPort types.Int64  `tfsdk:"public_port"`
	Protocol   types.String `tfsdk:"protocol"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	CreatedAt  types.String `tfsdk:"created_at"`
	UpdatedAt  types.String `tfsdk:"updated_at"`
}

func newTCPListenerResource() resource.Resource {
	return &tcpListenerResource{}
}

func (r *tcpListenerResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_tcp_listener"
}

func (r *tcpListenerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale TCP listener identifier.",
				MarkdownDescription: "Stable Gregale TCP listener identifier.",
			},
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app that owns the listener.",
				MarkdownDescription: "Slug of the Gregale app that owns the listener.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				Description:         "Stable listener name declared by the app.",
				MarkdownDescription: "Stable listener name declared by the app.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"guest_port": schema.Int64Attribute{
				Required:            true,
				Description:         "TCP port exposed by the workload.",
				MarkdownDescription: "TCP port exposed by the workload.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"public_port": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Stable public TCP port. Gregale allocates one from 40000–49999 when omitted.",
				MarkdownDescription: "Stable public TCP port. Gregale allocates one from `40000`–`49999` when omitted.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"protocol": schema.StringAttribute{
				Computed:            true,
				Description:         "Listener protocol. Gregale currently returns `tcp`.",
				MarkdownDescription: "Listener protocol. Gregale currently returns `tcp`.",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether the edge accepts new TCP connections.",
				MarkdownDescription: "Whether the edge accepts new TCP connections.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 listener creation timestamp.",
				MarkdownDescription: "RFC3339 listener creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 listener update timestamp.",
				MarkdownDescription: "RFC3339 listener update timestamp.",
			},
		},
		Description:         "Manage a stable public raw TCP listener for a Gregale app.",
		MarkdownDescription: "Manage a stable public raw TCP listener for a Gregale app.",
	}
}

func (r *tcpListenerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *tcpListenerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Gregale TCP listener import ID",
			"Use the format <app-slug>/<listener-name>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}

func (r *tcpListenerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a TCP listener.")
		return
	}
	var plan tcpListenerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createTCPListener(ctx, plan.AppSlug.ValueString(), tcpListenerRequest{
		Name:       plan.Name.ValueString(),
		GuestPort:  int(plan.GuestPort.ValueInt64()),
		PublicPort: intPointer(plan.PublicPort),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale TCP listener", err)
		return
	}
	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.ValueBool() {
		disabled := false
		out, err = r.client.updateTCPListener(ctx, plan.AppSlug.ValueString(), plan.Name.ValueString(), tcpListenerUpdate{Enabled: &disabled})
		if err != nil {
			resp.Diagnostics.Append(setTCPListenerModel(ctx, &resp.State, out, plan)...)
			appendClientError(&resp.Diagnostics, "Could not disable Gregale TCP listener after creation", err)
			return
		}
	}
	resp.Diagnostics.Append(setTCPListenerModel(ctx, &resp.State, out, plan)...)
}

func (r *tcpListenerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a TCP listener.")
		return
	}
	var state tcpListenerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	listeners, err := r.client.listTCPListeners(ctx, state.AppSlug.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale TCP listeners", err)
		return
	}
	for _, out := range listeners {
		if out.Name == state.Name.ValueString() {
			resp.Diagnostics.Append(setTCPListenerModel(ctx, &resp.State, out, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *tcpListenerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a TCP listener.")
		return
	}
	var plan tcpListenerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	enabled := plan.Enabled.ValueBool()
	out, err := r.client.updateTCPListener(ctx, plan.AppSlug.ValueString(), plan.Name.ValueString(), tcpListenerUpdate{Enabled: &enabled})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale TCP listener", err)
		return
	}
	resp.Diagnostics.Append(setTCPListenerModel(ctx, &resp.State, out, plan)...)
}

func (r *tcpListenerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a TCP listener.")
		return
	}
	var state tcpListenerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteTCPListener(ctx, state.AppSlug.ValueString(), state.Name.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale TCP listener", err)
	}
}

func setTCPListenerModel(ctx context.Context, state *tfsdk.State, out tcpListenerResponse, fallback tcpListenerModel) diag.Diagnostics {
	id := out.ID
	if id == "" {
		id = fallback.ID.ValueString()
	}
	appSlug := fallback.AppSlug.ValueString()
	name := out.Name
	if name == "" {
		name = fallback.Name.ValueString()
	}
	guestPort := out.GuestPort
	if guestPort == 0 {
		guestPort = int(fallback.GuestPort.ValueInt64())
	}
	publicPort := out.PublicPort
	if publicPort == 0 && !fallback.PublicPort.IsNull() && !fallback.PublicPort.IsUnknown() {
		publicPort = int(fallback.PublicPort.ValueInt64())
	}
	model := tcpListenerModel{
		ID:         types.StringValue(id),
		AppSlug:    types.StringValue(appSlug),
		Name:       types.StringValue(name),
		GuestPort:  types.Int64Value(int64(guestPort)),
		PublicPort: types.Int64Value(int64(publicPort)),
		Protocol:   types.StringValue(out.Protocol),
		Enabled:    types.BoolValue(out.Enabled),
		CreatedAt:  types.StringValue(out.CreatedAt),
		UpdatedAt:  types.StringValue(out.UpdatedAt),
	}
	return state.Set(ctx, &model)
}
