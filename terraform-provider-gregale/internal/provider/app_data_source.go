package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type appDataSource struct {
	client *client
}

type appDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	Slug            types.String `tfsdk:"slug"`
	Type            types.String `tfsdk:"type"`
	Visibility      types.String `tfsdk:"visibility"`
	Runtime         types.String `tfsdk:"runtime"`
	ResourceProfile types.String `tfsdk:"resource_profile"`
	RAMMB           types.Int64  `tfsdk:"ram_mb"`
	MaxConcurrency  types.Int64  `tfsdk:"max_concurrency"`
	IdleTimeoutS    types.Int64  `tfsdk:"idle_timeout_s"`
	HealthPath      types.String `tfsdk:"health_path"`
	HealthPathWakes types.Bool   `tfsdk:"health_path_wakes"`
	Status          types.String `tfsdk:"status"`
	URL             types.String `tfsdk:"url"`
}

func newAppDataSource() datasource.DataSource {
	return &appDataSource{}
}

func (d *appDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "gregale_app"
}

func (d *appDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable app identifier.",
				MarkdownDescription: "Gregale's immutable app identifier.",
			},
			"slug": schema.StringAttribute{
				Required:            true,
				Description:         "Stable app slug to look up.",
				MarkdownDescription: "Stable app slug to look up.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale workload type.",
				MarkdownDescription: "Gregale workload type.",
			},
			"visibility": schema.StringAttribute{
				Computed:            true,
				Description:         "Current app visibility.",
				MarkdownDescription: "Current app visibility.",
			},
			"runtime": schema.StringAttribute{
				Computed:            true,
				Description:         "Runtime hint recorded for the app.",
				MarkdownDescription: "Runtime hint recorded for the app.",
			},
			"resource_profile": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale resource profile.",
				MarkdownDescription: "Gregale resource profile.",
			},
			"ram_mb": schema.Int64Attribute{
				Computed:            true,
				Description:         "Requested memory in MiB.",
				MarkdownDescription: "Requested memory in MiB.",
			},
			"max_concurrency": schema.Int64Attribute{
				Computed:            true,
				Description:         "Maximum concurrent requests per instance.",
				MarkdownDescription: "Maximum concurrent requests per instance.",
			},
			"idle_timeout_s": schema.Int64Attribute{
				Computed:            true,
				Description:         "Idle timeout in seconds before the app can park.",
				MarkdownDescription: "Idle timeout in seconds before the app can park.",
			},
			"health_path": schema.StringAttribute{
				Computed:            true,
				Description:         "Application health path used by readiness checks.",
				MarkdownDescription: "Application health path used by readiness checks.",
			},
			"health_path_wakes": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether requests to the health path may wake a parked app.",
				MarkdownDescription: "Whether requests to the health path may wake a parked app.",
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
		Description:         "Reads an existing Gregale API application without managing its lifecycle.",
		MarkdownDescription: "Reads an existing Gregale API application without managing its lifecycle.",
	}
}

func (d *appDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *appDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading an app.")
		return
	}
	var query appDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	slug := query.Slug.ValueString()
	out, err := d.client.getApp(ctx, slug)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale app", err)
		return
	}

	state := appDataSourceModel{
		ID:              types.StringValue(out.ID),
		Slug:            types.StringValue(firstNonEmpty(out.Slug, slug)),
		Type:            types.StringValue(out.Type),
		Visibility:      types.StringValue(out.Visibility),
		Runtime:         types.StringValue(out.Runtime),
		ResourceProfile: types.StringValue(out.ResourceProfile),
		RAMMB:           appDataInt(out.RAMMB),
		MaxConcurrency:  appDataInt(out.MaxConcurrency),
		IdleTimeoutS:    appDataInt(out.IdleTimeoutS),
		HealthPath:      types.StringValue(out.HealthPath),
		HealthPathWakes: appDataBool(out.HealthPathWakes),
		Status:          types.StringValue(out.Status),
		URL:             types.StringValue(out.URL),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func firstNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func appDataInt(value *int) types.Int64 {
	if value == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*value))
}

func appDataBool(value *bool) types.Bool {
	if value == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*value)
}
