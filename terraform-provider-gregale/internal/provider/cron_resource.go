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

type cronResource struct {
	client *client
}

type cronModel struct {
	CronID          types.String `tfsdk:"cron_id"`
	AppID           types.String `tfsdk:"app_id"`
	Schedule        types.String `tfsdk:"schedule"`
	Path            types.String `tfsdk:"path"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	Timezone        types.String `tfsdk:"timezone"`
	SkipIfRunning   types.Bool   `tfsdk:"skip_if_running"`
	SuspendedReason types.String `tfsdk:"suspended_reason"`
	CreatedAt       types.String `tfsdk:"created_at"`
	LastFiredAt     types.String `tfsdk:"last_fired_at"`
}

func newCronResource() resource.Resource {
	return &cronResource{}
}

func (r *cronResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_cron"
}

func (r *cronResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"cron_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable cron identifier.",
				MarkdownDescription: "Gregale's immutable cron identifier.",
			},
			"app_id": schema.StringAttribute{
				Required:            true,
				Description:         "Stable Gregale app identifier that owns the schedule.",
				MarkdownDescription: "Stable Gregale app identifier that owns the schedule.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"schedule": schema.StringAttribute{
				Required:            true,
				Description:         "Five-field cron expression in m h dom mon dow format.",
				MarkdownDescription: "Five-field cron expression in `m h dom mon dow` format.",
			},
			"path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "App path to POST when the schedule fires. Defaults to `/`.",
				MarkdownDescription: "App path to POST when the schedule fires. Defaults to `/`.",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether the scheduler should evaluate this cron.",
				MarkdownDescription: "Whether the scheduler should evaluate this cron.",
			},
			"timezone": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "IANA timezone used to interpret the schedule. Defaults to UTC.",
				MarkdownDescription: "IANA timezone used to interpret the schedule. Defaults to `UTC`.",
			},
			"skip_if_running": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether to skip a fire while the previous invocation is still running.",
				MarkdownDescription: "Whether to skip a fire while the previous invocation is still running.",
			},
			"suspended_reason": schema.StringAttribute{
				Computed:            true,
				Description:         "Why Gregale suspended the schedule, when applicable.",
				MarkdownDescription: "Why Gregale suspended the schedule, when applicable.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the cron was created.",
				MarkdownDescription: "RFC3339 timestamp when the cron was created.",
			},
			"last_fired_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp of the most recent fire.",
				MarkdownDescription: "RFC3339 timestamp of the most recent fire.",
			},
		},
		Description:         "Manage a Gregale scheduled app invocation.",
		MarkdownDescription: "Manage a Gregale scheduled app invocation.",
	}
}

func (r *cronResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *cronResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Gregale cron import ID",
			"Use the format <app-id>/<cron-id>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cron_id"), parts[1])...)
}

func (r *cronResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a cron.")
		return
	}
	var plan cronModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createCron(ctx, cronRequest{
		AppID:         plan.AppID.ValueString(),
		Schedule:      plan.Schedule.ValueString(),
		Path:          stringValue(plan.Path),
		Enabled:       boolPointer(plan.Enabled),
		Timezone:      stringValue(plan.Timezone),
		SkipIfRunning: boolPointer(plan.SkipIfRunning),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale cron", err)
		return
	}
	resp.Diagnostics.Append(setCronModel(ctx, &resp.State, out, plan)...)
}

func (r *cronResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a cron.")
		return
	}
	var state cronModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getCron(ctx, state.CronID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale cron", err)
		return
	}
	resp.Diagnostics.Append(setCronModel(ctx, &resp.State, out, state)...)
}

func (r *cronResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a cron.")
		return
	}
	var plan cronModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.updateCron(ctx, plan.CronID.ValueString(), cronPatch{
		Schedule:      stringPointer(plan.Schedule),
		Path:          stringPointer(plan.Path),
		Enabled:       boolPointer(plan.Enabled),
		Timezone:      stringPointer(plan.Timezone),
		SkipIfRunning: boolPointer(plan.SkipIfRunning),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale cron", err)
		return
	}
	resp.Diagnostics.Append(setCronModel(ctx, &resp.State, out, plan)...)
}

func (r *cronResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a cron.")
		return
	}
	var state cronModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteCron(ctx, state.CronID.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale cron", err)
	}
}

func setCronModel(ctx context.Context, state *tfsdk.State, out cronResponse, fallback cronModel) diag.Diagnostics {
	cronID := out.ID
	if cronID == "" {
		cronID = fallback.CronID.ValueString()
	}
	model := cronModel{
		CronID:          types.StringValue(cronID),
		AppID:           remoteString(out.AppID, fallback.AppID),
		Schedule:        remoteString(out.Schedule, fallback.Schedule),
		Path:            remoteString(out.Path, fallback.Path),
		Enabled:         types.BoolValue(out.Enabled),
		Timezone:        remoteString(out.Timezone, fallback.Timezone),
		SkipIfRunning:   types.BoolValue(out.SkipIfRunning),
		SuspendedReason: types.StringValue(out.SuspendedReason),
		CreatedAt:       types.StringValue(out.CreatedAt),
		LastFiredAt:     types.StringValue(out.LastFiredAt),
	}
	return state.Set(ctx, &model)
}
