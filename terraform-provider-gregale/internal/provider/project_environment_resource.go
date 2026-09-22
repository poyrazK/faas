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

type projectEnvironmentResource struct {
	client *client
}

type projectEnvironmentResourceModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectSlug types.String `tfsdk:"project_slug"`
	Slug        types.String `tfsdk:"slug"`
	ProjectID   types.String `tfsdk:"project_id"`
	Protected   types.Bool   `tfsdk:"protected"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func newProjectEnvironmentResource() resource.Resource {
	return &projectEnvironmentResource{}
}

func (r *projectEnvironmentResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_project_environment"
}

func (r *projectEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
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
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"slug": schema.StringAttribute{
				Required:            true,
				Description:         "Lowercase project environment slug.",
				MarkdownDescription: "Lowercase project environment slug.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"project_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale project identifier.",
				MarkdownDescription: "Gregale project identifier.",
			},
			"protected": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether the environment is protected from destructive lifecycle operations and promotion.",
				MarkdownDescription: "Whether the environment is protected from destructive lifecycle operations and promotion.",
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
		Description:         "Manage a Gregale project environment with guarded deletion.",
		MarkdownDescription: "Manage a Gregale project environment with guarded deletion.",
	}
}

func (r *projectEnvironmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *projectEnvironmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectSlug, environment, ok := strings.Cut(req.ID, "/")
	if !ok || projectSlug == "" || environment == "" || strings.Contains(environment, "/") {
		resp.Diagnostics.AddError(
			"Invalid Gregale project environment import ID",
			"Use the format <project-slug>/<environment-slug>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_slug"), projectSlug)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("slug"), environment)...)
}

func (r *projectEnvironmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a project environment.")
		return
	}
	var plan projectEnvironmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var protected *bool
	if !plan.Protected.IsNull() && !plan.Protected.IsUnknown() {
		protected = boolPointer(plan.Protected)
	}
	out, err := r.client.createProjectEnvironment(ctx, plan.ProjectSlug.ValueString(), projectEnvironmentRequest{
		Slug:      plan.Slug.ValueString(),
		Protected: protected,
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale project environment", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentResourceModel(ctx, &resp.State, out, plan)...)
}

func (r *projectEnvironmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a project environment.")
		return
	}
	var state projectEnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getProjectEnvironment(ctx, state.ProjectSlug.ValueString(), state.Slug.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale project environment", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentResourceModel(ctx, &resp.State, out, state)...)
}

func (r *projectEnvironmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a project environment.")
		return
	}
	var plan projectEnvironmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Protected.IsNull() || plan.Protected.IsUnknown() {
		resp.Diagnostics.AddError("Unknown project environment protection", "The protected value must be known before updating the environment.")
		return
	}
	out, err := r.client.updateProjectEnvironment(ctx, plan.ProjectSlug.ValueString(), plan.Slug.ValueString(), projectEnvironmentPatch{
		Protected: boolPointer(plan.Protected),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale project environment", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentResourceModel(ctx, &resp.State, out, plan)...)
}

func (r *projectEnvironmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a project environment.")
		return
	}
	var state projectEnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteProjectEnvironment(ctx, state.ProjectSlug.ValueString(), state.Slug.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale project environment", err)
	}
}

func setProjectEnvironmentResourceModel(ctx context.Context, state *tfsdk.State, out projectEnvironmentResponse, fallback projectEnvironmentResourceModel) diag.Diagnostics {
	projectSlug := fallback.ProjectSlug.ValueString()
	slug := fallback.Slug.ValueString()
	slug = firstNonEmpty(out.Slug, slug)
	model := projectEnvironmentResourceModel{
		ID:          types.StringValue(projectSlug + "/" + slug),
		ProjectSlug: types.StringValue(projectSlug),
		Slug:        types.StringValue(slug),
		ProjectID:   types.StringValue(out.ProjectID),
		Protected:   types.BoolValue(out.Protected),
		CreatedAt:   types.StringValue(out.CreatedAt),
		UpdatedAt:   types.StringValue(out.UpdatedAt),
	}
	return state.Set(ctx, &model)
}
