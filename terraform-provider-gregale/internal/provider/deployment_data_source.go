package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type deploymentDataSource struct {
	client *client
}

type deploymentDataSourceModel struct {
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

func newDeploymentDataSource() datasource.DataSource {
	return &deploymentDataSource{}
}

func (d *deploymentDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "gregale_deployment"
}

func (d *deploymentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable deployment identifier.",
				MarkdownDescription: "Gregale's immutable deployment identifier.",
			},
			"deployment_id": schema.StringAttribute{
				Required:            true,
				Description:         "Immutable Gregale deployment identifier to look up.",
				MarkdownDescription: "Immutable Gregale deployment identifier to look up.",
			},
			"app_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Stable Gregale app identifier.",
				MarkdownDescription: "Stable Gregale app identifier.",
			},
			"build_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Build identifier associated with the deployment.",
				MarkdownDescription: "Build identifier associated with the deployment.",
			},
			"image_digest": schema.StringAttribute{
				Computed:            true,
				Description:         "Image digest produced by the deployment.",
				MarkdownDescription: "Image digest produced by the deployment.",
			},
			"kind": schema.StringAttribute{
				Computed:            true,
				Description:         "Deployment build kind.",
				MarkdownDescription: "Deployment build kind.",
			},
			"status": schema.StringAttribute{
				Computed:            true,
				Description:         "Deployment lifecycle status.",
				MarkdownDescription: "Deployment lifecycle status.",
			},
			"scope": schema.StringAttribute{
				Computed:            true,
				Description:         "Runtime environment scope selected by the deployment.",
				MarkdownDescription: "Runtime environment scope selected by the deployment.",
			},
			"commit_sha": schema.StringAttribute{
				Computed:            true,
				Description:         "Resolved commit SHA used by Gregale.",
				MarkdownDescription: "Resolved commit SHA used by Gregale.",
			},
			"source_url": schema.StringAttribute{
				Computed:            true,
				Description:         "Resolved source URL for the deployment.",
				MarkdownDescription: "Resolved source URL for the deployment.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 deployment creation timestamp.",
				MarkdownDescription: "RFC3339 deployment creation timestamp.",
			},
			"preview_url": schema.StringAttribute{
				Computed:            true,
				Description:         "Shareable preview URL, when available.",
				MarkdownDescription: "Shareable preview URL, when available.",
			},
			"preview_host": schema.StringAttribute{
				Computed:            true,
				Description:         "Preview hostname, when available.",
				MarkdownDescription: "Preview hostname, when available.",
			},
			"stage_state": schema.StringAttribute{
				Computed:            true,
				Description:         "JSON deployment stage summary.",
				MarkdownDescription: "JSON deployment stage summary.",
			},
			"error": schema.StringAttribute{
				Computed:            true,
				Description:         "Deployment failure message, when applicable.",
				MarkdownDescription: "Deployment failure message, when applicable.",
			},
			"error_code": schema.StringAttribute{
				Computed:            true,
				Description:         "Structured deployment failure code, when applicable.",
				MarkdownDescription: "Structured deployment failure code, when applicable.",
			},
			"error_hint": schema.StringAttribute{
				Computed:            true,
				Description:         "Failure hint, when applicable.",
				MarkdownDescription: "Failure hint, when applicable.",
			},
			"error_why": schema.StringAttribute{
				Computed:            true,
				Description:         "Failure explanation, when applicable.",
				MarkdownDescription: "Failure explanation, when applicable.",
			},
			"error_fix": schema.StringAttribute{
				Computed:            true,
				Description:         "Failure remediation, when applicable.",
				MarkdownDescription: "Failure remediation, when applicable.",
			},
		},
		Description:         "Reads an existing Gregale deployment without managing its lifecycle.",
		MarkdownDescription: "Reads an existing Gregale deployment without managing its lifecycle.",
	}
}

func (d *deploymentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *deploymentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a deployment.")
		return
	}
	var query deploymentDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &query)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deploymentID := query.DeploymentID.ValueString()
	out, err := d.client.getDeployment(ctx, deploymentID)
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale deployment", err)
		return
	}
	preview, err := d.client.getDeploymentURL(ctx, deploymentID)
	if err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not read Gregale deployment preview URL", err)
		return
	}

	resp.Diagnostics.Append(setDeploymentDataSourceState(ctx, resp, out, preview, deploymentID)...)
}

func setDeploymentDataSourceState(ctx context.Context, resp *datasource.ReadResponse, out deploymentResponse, preview deploymentURLResponse, deploymentID string) diag.Diagnostics {
	stageState := string(out.StageState)
	if stageState == "null" {
		stageState = ""
	}
	state := deploymentDataSourceModel{
		ID:           types.StringValue(firstNonEmpty(out.ID, deploymentID)),
		DeploymentID: types.StringValue(deploymentID),
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
	return resp.State.Set(ctx, &state)
}
