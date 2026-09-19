package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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

type projectEnvironmentConfigResource struct {
	client *client
}

const maxProjectEnvironmentConfigBytes = 64 << 10

var projectEnvironmentConfigKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

type projectEnvironmentConfigModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectSlug types.String `tfsdk:"project_slug"`
	Environment types.String `tfsdk:"environment"`
	Values      types.String `tfsdk:"values"`
	Version     types.Int64  `tfsdk:"version"`
	ConfigHash  types.String `tfsdk:"config_hash"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func newProjectEnvironmentConfigResource() resource.Resource {
	return &projectEnvironmentConfigResource{}
}

func (r *projectEnvironmentConfigResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_project_environment_config"
}

func (r *projectEnvironmentConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
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
			"environment": schema.StringAttribute{
				Required:            true,
				Description:         "Registered project environment slug.",
				MarkdownDescription: "Registered project environment slug.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"values": schema.StringAttribute{
				Required:            true,
				Description:         "Non-secret JSON object for the environment. Prefer jsonencode(...) for stable Terraform plans.",
				MarkdownDescription: "Non-secret JSON object for the environment. Prefer `jsonencode(...)` for stable Terraform plans.",
			},
			"version": schema.Int64Attribute{
				Computed:            true,
				Description:         "Immutable Gregale environment configuration version.",
				MarkdownDescription: "Immutable Gregale environment configuration version.",
			},
			"config_hash": schema.StringAttribute{
				Computed:            true,
				Description:         "SHA-256 hash of the canonical configuration object.",
				MarkdownDescription: "SHA-256 hash of the canonical configuration object.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when this configuration version was created.",
				MarkdownDescription: "RFC3339 timestamp when this configuration version was created.",
			},
		},
		Description:         "Manage non-secret configuration for an existing Gregale project environment.",
		MarkdownDescription: "Manage non-secret configuration for an existing Gregale project environment.",
	}
}

func (r *projectEnvironmentConfigResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{projectEnvironmentConfigValidator{}}
}

func (r *projectEnvironmentConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *projectEnvironmentConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectSlug, environment, ok := strings.Cut(req.ID, "/")
	if !ok || projectSlug == "" || environment == "" || strings.Contains(environment, "/") {
		resp.Diagnostics.AddError(
			"Invalid Gregale project environment configuration import ID",
			"Use the format <project-slug>/<environment-slug>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_slug"), projectSlug)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), environment)...)
}

func (r *projectEnvironmentConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before managing project environment configuration.")
		return
	}
	var plan projectEnvironmentConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	values, err := canonicalProjectEnvironmentConfig(plan.Values.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("values"), "Invalid project environment configuration", err.Error())
		return
	}
	out, err := r.client.updateProjectEnvironmentConfig(ctx, plan.ProjectSlug.ValueString(), plan.Environment.ValueString(), projectEnvironmentConfigRequest{Values: values})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not configure Gregale project environment", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentConfigModel(ctx, &resp.State, out, plan)...)
}

func (r *projectEnvironmentConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading project environment configuration.")
		return
	}
	var state projectEnvironmentConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getProjectEnvironmentConfig(ctx, state.ProjectSlug.ValueString(), state.Environment.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale project environment configuration", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentConfigModel(ctx, &resp.State, out, state)...)
}

func (r *projectEnvironmentConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before managing project environment configuration.")
		return
	}
	var plan projectEnvironmentConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	values, err := canonicalProjectEnvironmentConfig(plan.Values.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("values"), "Invalid project environment configuration", err.Error())
		return
	}
	out, err := r.client.updateProjectEnvironmentConfig(ctx, plan.ProjectSlug.ValueString(), plan.Environment.ValueString(), projectEnvironmentConfigRequest{Values: values})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale project environment configuration", err)
		return
	}
	resp.Diagnostics.Append(setProjectEnvironmentConfigModel(ctx, &resp.State, out, plan)...)
}

func (r *projectEnvironmentConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before resetting project environment configuration.")
		return
	}
	var state projectEnvironmentConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.updateProjectEnvironmentConfig(ctx, state.ProjectSlug.ValueString(), state.Environment.ValueString(), projectEnvironmentConfigRequest{Values: json.RawMessage(`{}`)})
	if err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not reset Gregale project environment configuration", err)
	}
}

type projectEnvironmentConfigValidator struct{}

func (projectEnvironmentConfigValidator) Description(context.Context) string {
	return "Requires a valid non-secret JSON object for the project environment configuration."
}

func (projectEnvironmentConfigValidator) MarkdownDescription(context.Context) string {
	return "Requires a valid non-secret JSON object for the project environment configuration."
}

func (projectEnvironmentConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config projectEnvironmentConfigModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || config.Values.IsUnknown() {
		return
	}
	if _, err := canonicalProjectEnvironmentConfig(config.Values.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("values"), "Invalid project environment configuration", err.Error())
	}
}

func setProjectEnvironmentConfigModel(ctx context.Context, state *tfsdk.State, out projectEnvironmentConfigResponse, fallback projectEnvironmentConfigModel) diag.Diagnostics {
	model, diags := projectEnvironmentConfigModelFromResponse(out, fallback)
	if diags.HasError() {
		return diags
	}
	return state.Set(ctx, &model)
}

func projectEnvironmentConfigModelFromResponse(out projectEnvironmentConfigResponse, fallback projectEnvironmentConfigModel) (projectEnvironmentConfigModel, diag.Diagnostics) {
	values, err := canonicalProjectEnvironmentConfig(string(out.Values))
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Invalid Gregale project environment configuration", err.Error())
		return projectEnvironmentConfigModel{}, diags
	}
	projectSlug := firstNonEmpty(out.ProjectSlug, fallback.ProjectSlug.ValueString())
	environment := firstNonEmpty(out.Environment, fallback.Environment.ValueString())
	return projectEnvironmentConfigModel{
		ID:          types.StringValue(projectSlug + "/" + environment),
		ProjectSlug: types.StringValue(projectSlug),
		Environment: types.StringValue(environment),
		Values:      types.StringValue(string(values)),
		Version:     types.Int64Value(out.Version),
		ConfigHash:  types.StringValue(out.ConfigHash),
		UpdatedAt:   types.StringValue(out.UpdatedAt),
	}, nil
}

func canonicalProjectEnvironmentConfig(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	if len(trimmed) > maxProjectEnvironmentConfigBytes {
		return nil, fmt.Errorf("configuration exceeds %d bytes", maxProjectEnvironmentConfigBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("configuration must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("configuration must contain one JSON value")
		}
		return nil, fmt.Errorf("configuration must contain one JSON value: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("configuration must be a JSON object")
	}
	for key := range object {
		if !projectEnvironmentConfigKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid configuration key %q", key)
		}
		if projectEnvironmentConfigKeyIsSensitive(key) {
			return nil, fmt.Errorf("configuration key %q is reserved for secrets", key)
		}
	}
	return json.Marshal(object)
}

func projectEnvironmentConfigKeyIsSensitive(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '.'
	})
	for i, part := range parts {
		switch part {
		case "secret", "secrets", "password", "token", "credential", "credentials":
			return true
		}
		if part == "key" && i > 0 && (parts[i-1] == "api" || parts[i-1] == "access" || parts[i-1] == "private") {
			return true
		}
	}
	return false
}
