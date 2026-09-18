package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type latestDeploymentDataSource struct {
	client *client
}

type latestDeploymentDataSourceModel struct {
	AppSlug      types.String `tfsdk:"app_slug"`
	ID           types.String `tfsdk:"id"`
	DeploymentID types.String `tfsdk:"deployment_id"`
	AppID        types.String `tfsdk:"app_id"`
	BuildID      types.String `tfsdk:"build_id"`
	ImageDigest  types.String `tfsdk:"image_digest"`
	Kind         types.String `tfsdk:"kind"`
	Status       types.String `tfsdk:"status"`
	Scope        types.String `tfsdk:"scope"`
	CommitSHA    types.String `tfsdk:"commit_sha"`
	SourceURL    types.String `tfsdk:"source_url"`
	CreatedAt    types.String `tfsdk:"created_at"`
	PreviewURL   types.String `tfsdk:"preview_url"`
	PreviewHost  types.String `tfsdk:"preview_host"`
	StageState   types.String `tfsdk:"stage_state"`
	Error        types.String `tfsdk:"error"`
	ErrorCode    types.String `tfsdk:"error_code"`
	ErrorHint    types.String `tfsdk:"error_hint"`
	ErrorWhy     types.String `tfsdk:"error_why"`
	ErrorFix     types.String `tfsdk:"error_fix"`
}

func newLatestDeploymentDataSource() datasource.DataSource {
	return &latestDeploymentDataSource{}
}

func (d *latestDeploymentDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "gregale_latest_deployment"
}

func (d *latestDeploymentDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	var base datasource.SchemaResponse
	(&deploymentDataSource{}).Schema(ctx, req, &base)
	base.Schema.Attributes["deployment_id"] = schema.StringAttribute{
		Computed:            true,
		Description:         "Immutable Gregale deployment identifier.",
		MarkdownDescription: "Immutable Gregale deployment identifier.",
	}
	base.Schema.Attributes["app_slug"] = schema.StringAttribute{
		Required:            true,
		Description:         "Stable Gregale app slug whose newest deployment should be read.",
		MarkdownDescription: "Stable Gregale app slug whose newest deployment should be read.",
	}
	base.Schema.Description = "Reads the newest deployment for an existing Gregale app without managing its lifecycle."
	base.Schema.MarkdownDescription = base.Schema.Description
	resp.Schema = base.Schema
}

func (d *latestDeploymentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *latestDeploymentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading the latest deployment.")
		return
	}
	var query latestDeploymentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	appSlug := query.AppSlug.ValueString()
	out, err := d.client.getLatestAppDeployment(ctx, appSlug)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale latest deployment", err)
		return
	}
	preview, err := d.client.getDeploymentURL(ctx, out.ID)
	if err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not read Gregale latest deployment preview URL", err)
		return
	}
	stageState := string(out.StageState)
	if stageState == "null" {
		stageState = ""
	}
	state := latestDeploymentDataSourceModel{
		AppSlug:      types.StringValue(appSlug),
		ID:           types.StringValue(out.ID),
		DeploymentID: types.StringValue(out.ID),
		AppID:        types.StringValue(out.AppID),
		BuildID:      types.StringValue(out.BuildID),
		ImageDigest:  types.StringValue(out.ImageDigest),
		Kind:         types.StringValue(out.Kind),
		Status:       types.StringValue(out.Status),
		Scope:        types.StringValue(out.Scope),
		CommitSHA:    types.StringValue(out.CommitSHA),
		SourceURL:    types.StringValue(out.SourceURL),
		CreatedAt:    types.StringValue(out.CreatedAt),
		PreviewURL:   types.StringValue(preview.URL),
		PreviewHost:  types.StringValue(preview.Host),
		StageState:   types.StringValue(stageState),
		Error:        types.StringValue(out.Error),
		ErrorCode:    types.StringValue(out.ErrorCode),
		ErrorHint:    types.StringValue(out.ErrorHint),
		ErrorWhy:     types.StringValue(out.ErrorWhy),
		ErrorFix:     types.StringValue(out.ErrorFix),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
