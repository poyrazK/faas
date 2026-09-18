package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type projectEnvironmentDataSource struct {
	client *client
}

type projectEnvironmentModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectSlug types.String `tfsdk:"project_slug"`
	Slug        types.String `tfsdk:"slug"`
	ProjectID   types.String `tfsdk:"project_id"`
	Protected   types.Bool   `tfsdk:"protected"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func newProjectEnvironmentDataSource() datasource.DataSource {
	return &projectEnvironmentDataSource{}
}

func (d *projectEnvironmentDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = "gregale_project_environment"
}

func (d *projectEnvironmentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				Description:         "Composite project/environment identifier.",
				MarkdownDescription: "Composite project/environment identifier.",
			},
			"project_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Project slug owning the environment.",
				MarkdownDescription: "Project slug owning the environment.",
			},
			"slug": schema.StringAttribute{
				Required:            true,
				Description:         "Environment slug.",
				MarkdownDescription: "Environment slug.",
			},
			"project_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale project identifier.",
				MarkdownDescription: "Gregale project identifier.",
			},
			"protected": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether the environment is protected from promotion.",
				MarkdownDescription: "Whether the environment is protected from promotion.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "Environment creation timestamp.",
				MarkdownDescription: "Environment creation timestamp.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "Environment last-update timestamp.",
				MarkdownDescription: "Environment last-update timestamp.",
			},
		},
		Description:         "Read a durable Gregale project environment.",
		MarkdownDescription: "Read a durable Gregale project environment.",
	}
}

func (d *projectEnvironmentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *projectEnvironmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading an environment.")
		return
	}
	var query projectEnvironmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &query)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := d.client.getProjectEnvironment(ctx, query.ProjectSlug.ValueString(), query.Slug.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Could not read Gregale project environment", err.Error())
		return
	}
	state := projectEnvironmentModel{
		ID:          types.StringValue(query.ProjectSlug.ValueString() + "/" + out.Slug),
		ProjectSlug: types.StringValue(query.ProjectSlug.ValueString()),
		Slug:        types.StringValue(out.Slug),
		ProjectID:   types.StringValue(out.ProjectID),
		Protected:   types.BoolValue(out.Protected),
		CreatedAt:   types.StringValue(out.CreatedAt),
		UpdatedAt:   types.StringValue(out.UpdatedAt),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
