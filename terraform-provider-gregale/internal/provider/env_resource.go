package provider

import (
	"context"
	"errors"
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

const defaultEnvScope = "default"

type envResource struct {
	client *client
}

type envModel struct {
	AppSlug   types.String `tfsdk:"app_slug"`
	Scope     types.String `tfsdk:"scope"`
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

func newEnvResource() resource.Resource {
	return &envResource{}
}

func (r *envResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_env"
}

func (r *envResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app that owns the environment variable.",
				MarkdownDescription: "Slug of the Gregale app that owns the environment variable.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"scope": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Environment scope for the variable. Defaults to `default`.",
				MarkdownDescription: "Environment scope for the variable. Defaults to `default`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required:            true,
				Description:         "Environment variable key, such as LOG_LEVEL or FEATURE_FLAG.",
				MarkdownDescription: "Environment variable key, such as `LOG_LEVEL` or `FEATURE_FLAG`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Description:         "Environment variable value. Requires Terraform 1.11 or later and is never stored in plan or state.",
				MarkdownDescription: "Environment variable value. Requires Terraform 1.11 or later and is never stored in plan or state.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the variable was created.",
				MarkdownDescription: "RFC3339 timestamp when the variable was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the variable was last changed.",
				MarkdownDescription: "RFC3339 timestamp when the variable was last changed.",
			},
		},
		Description:         "Manage a Gregale app environment variable without storing its value in Terraform state.",
		MarkdownDescription: "Manage a Gregale app environment variable without storing its value in Terraform state.",
	}
}

func (r *envResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *envResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 && len(parts) != 3 {
		resp.Diagnostics.AddError(
			"Invalid Gregale environment variable import ID",
			"Use the format <app-slug>/<key> or <app-slug>/<scope>/<key>.",
		)
		return
	}
	if parts[0] == "" || parts[len(parts)-1] == "" {
		resp.Diagnostics.AddError("Invalid Gregale environment variable import ID", "App slug and environment variable key must not be empty.")
		return
	}
	scope := defaultEnvScope
	if len(parts) == 3 {
		if parts[1] == "" || parts[1] == "__all__" {
			resp.Diagnostics.AddError("Invalid Gregale environment variable import ID", "Environment variable scope must be a concrete scope name.")
			return
		}
		scope = parts[1]
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope"), scope)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[len(parts)-1])...)
}

func (r *envResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating an environment variable.")
		return
	}
	var plan envModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := stringValue(plan.Scope)
	if err := r.client.setEnv(ctx, plan.AppSlug.ValueString(), scope, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		appendClientError(&resp.Diagnostics, "Could not set Gregale environment variable", err)
		return
	}
	r.refreshState(ctx, &resp.Diagnostics, &resp.State, plan)
}

func (r *envResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading an environment variable.")
		return
	}
	var state envModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	metadata, found, err := r.client.getEnv(ctx, state.AppSlug.ValueString(), stringValue(state.Scope), state.Key.ValueString())
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale environment variable metadata", err)
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setEnvModel(ctx, &resp.State, metadata, state)...)
}

func (r *envResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating an environment variable.")
		return
	}
	var plan envModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := stringValue(plan.Scope)
	if err := r.client.setEnv(ctx, plan.AppSlug.ValueString(), scope, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale environment variable", err)
		return
	}
	r.refreshState(ctx, &resp.Diagnostics, &resp.State, plan)
}

func (r *envResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting an environment variable.")
		return
	}
	var state envModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteEnv(ctx, state.AppSlug.ValueString(), stringValue(state.Scope), state.Key.ValueString()); err != nil && !isEnvNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale environment variable", err)
	}
}

func (r *envResource) refreshState(ctx context.Context, diags *diag.Diagnostics, state *tfsdk.State, fallback envModel) {
	metadata, found, err := r.client.getEnv(ctx, fallback.AppSlug.ValueString(), stringValue(fallback.Scope), fallback.Key.ValueString())
	if err != nil {
		appendClientError(diags, "Environment variable was set but metadata refresh failed", err)
		return
	}
	if !found {
		diags.AddError("Environment variable was set but could not be read", "Gregale accepted the write, but the variable was not returned by the metadata endpoint.")
		return
	}
	diags.Append(setEnvModel(ctx, state, metadata, fallback)...)
}

func setEnvModel(ctx context.Context, state *tfsdk.State, metadata envMetadata, fallback envModel) diag.Diagnostics {
	scope := metadata.Scope
	if scope == "" {
		scope = fallback.Scope.ValueString()
	}
	if scope == "" {
		scope = defaultEnvScope
	}
	model := envModel{
		AppSlug:   fallback.AppSlug,
		Scope:     types.StringValue(scope),
		Key:       remoteString(metadata.Key, fallback.Key),
		Value:     types.StringNull(),
		CreatedAt: types.StringValue(metadata.CreatedAt),
		UpdatedAt: types.StringValue(metadata.UpdatedAt),
	}
	return state.Set(ctx, &model)
}

func isEnvNotFound(err error) bool {
	if isNotFound(err) {
		return true
	}
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.code == "env_var_not_found"
}
