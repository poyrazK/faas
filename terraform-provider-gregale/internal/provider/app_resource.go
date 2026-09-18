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

type appResource struct {
	client *client
}

type appModel struct {
	Slug            types.String `tfsdk:"slug"`
	Visibility      types.String `tfsdk:"visibility"`
	Runtime         types.String `tfsdk:"runtime"`
	ResourceProfile types.String `tfsdk:"resource_profile"`
	RAMMB           types.Int64  `tfsdk:"ram_mb"`
	MaxConcurrency  types.Int64  `tfsdk:"max_concurrency"`
	IdleTimeoutS    types.Int64  `tfsdk:"idle_timeout_s"`
	HealthPath      types.String `tfsdk:"health_path"`
	HealthPathWakes types.Bool   `tfsdk:"health_path_wakes"`
	AppID           types.String `tfsdk:"app_id"`
	Status          types.String `tfsdk:"status"`
	URL             types.String `tfsdk:"url"`
}

func newAppResource() resource.Resource {
	return &appResource{}
}

func (r *appResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_app"
}

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				Required:            true,
				Description:         "Stable app slug. Renaming is intentionally a replacement in this first provider contract.",
				MarkdownDescription: "Stable app slug. Renaming is intentionally a replacement in this first provider contract.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"visibility": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "App visibility as accepted by the Gregale API.",
				MarkdownDescription: "App visibility as accepted by the Gregale API.",
			},
			"runtime": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Optional runtime hint for the app's first deployment.",
				MarkdownDescription: "Optional runtime hint for the app's first deployment.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"resource_profile": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Gregale resource profile, such as micro or small.",
				MarkdownDescription: "Gregale resource profile, such as `micro` or `small`.",
			},
			"ram_mb": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Requested memory in MiB.",
				MarkdownDescription: "Requested memory in MiB.",
			},
			"max_concurrency": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Maximum concurrent requests per instance.",
				MarkdownDescription: "Maximum concurrent requests per instance.",
			},
			"idle_timeout_s": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Idle timeout in seconds before the app can park.",
				MarkdownDescription: "Idle timeout in seconds before the app can park.",
			},
			"health_path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Application health path used by deployment readiness checks.",
				MarkdownDescription: "Application health path used by deployment readiness checks.",
			},
			"health_path_wakes": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether requests to the health path may wake a parked app.",
				MarkdownDescription: "Whether requests to the health path may wake a parked app.",
			},
			"app_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable app identifier.",
				MarkdownDescription: "Gregale's immutable app identifier.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				Description:         "Current app status.",
				MarkdownDescription: "Current app status.",
			},
			"url": schema.StringAttribute{
				Computed:            true,
				Description:         "Current public app URL, when one is available.",
				MarkdownDescription: "Current public app URL, when one is available.",
			},
		},
		Description:         "A Gregale API application.",
		MarkdownDescription: "A Gregale API application.",
	}
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("slug"), req.ID)...)
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating an app.")
		return
	}
	var plan appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createApp(ctx, appRequest{
		Slug:            plan.Slug.ValueString(),
		Visibility:      stringValue(plan.Visibility),
		Type:            "app",
		Runtime:         stringValue(plan.Runtime),
		ResourceProfile: stringValue(plan.ResourceProfile),
		RAMMB:           intPointer(plan.RAMMB),
		MaxConcurrency:  intPointer(plan.MaxConcurrency),
		IdleTimeoutS:    intPointer(plan.IdleTimeoutS),
		HealthPath:      stringValue(plan.HealthPath),
		HealthPathWakes: boolPointer(plan.HealthPathWakes),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale app", err)
		return
	}
	resp.Diagnostics.Append(setAppModel(ctx, &resp.State, out, plan)...)
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading an app.")
		return
	}
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getApp(ctx, state.Slug.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale app", err)
		return
	}
	resp.Diagnostics.Append(setAppModel(ctx, &resp.State, out, state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating an app.")
		return
	}
	var plan appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.updateApp(ctx, plan.Slug.ValueString(), appPatch{
		Visibility:      stringPointer(plan.Visibility),
		ResourceProfile: stringPointer(plan.ResourceProfile),
		RAMMB:           intPointer(plan.RAMMB),
		MaxConcurrency:  intPointer(plan.MaxConcurrency),
		IdleTimeoutS:    intPointer(plan.IdleTimeoutS),
		HealthPath:      stringPointer(plan.HealthPath),
		HealthPathWakes: boolPointer(plan.HealthPathWakes),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale app", err)
		return
	}
	resp.Diagnostics.Append(setAppModel(ctx, &resp.State, out, plan)...)
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting an app.")
		return
	}
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteApp(ctx, state.Slug.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale app", err)
	}
}

func setAppModel(ctx context.Context, state *tfsdk.State, out appResponse, fallback appModel) diag.Diagnostics {
	slug := out.Slug
	if slug == "" {
		slug = fallback.Slug.ValueString()
	}
	model := appModel{
		Slug:            types.StringValue(slug),
		Visibility:      remoteString(out.Visibility, fallback.Visibility),
		Runtime:         remoteString(out.Runtime, fallback.Runtime),
		ResourceProfile: remoteString(out.ResourceProfile, fallback.ResourceProfile),
		RAMMB:           remoteInt(out.RAMMB, fallback.RAMMB),
		MaxConcurrency:  remoteInt(out.MaxConcurrency, fallback.MaxConcurrency),
		IdleTimeoutS:    remoteInt(out.IdleTimeoutS, fallback.IdleTimeoutS),
		HealthPath:      remoteString(out.HealthPath, fallback.HealthPath),
		HealthPathWakes: remoteBool(out.HealthPathWakes, fallback.HealthPathWakes),
		AppID:           types.StringValue(out.ID),
		Status:          types.StringValue(out.Status),
		URL:             types.StringValue(out.URL),
	}
	return state.Set(ctx, &model)
}

func stringValue(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	out := value.ValueString()
	return &out
}

func intPointer(value types.Int64) *int {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	out := int(value.ValueInt64())
	return &out
}

func boolPointer(value types.Bool) *bool {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	out := value.ValueBool()
	return &out
}

func remoteString(value string, fallback types.String) types.String {
	if value != "" {
		return types.StringValue(value)
	}
	return fallback
}

func remoteInt(value *int, fallback types.Int64) types.Int64 {
	if value != nil {
		return types.Int64Value(int64(*value))
	}
	return fallback
}

func remoteBool(value *bool, fallback types.Bool) types.Bool {
	if value != nil {
		return types.BoolValue(*value)
	}
	if fallback.IsNull() || fallback.IsUnknown() {
		return types.BoolValue(false)
	}
	return fallback
}
