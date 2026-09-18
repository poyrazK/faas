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

const defaultSecretScope = "default"

type secretResource struct {
	client *client
}

type secretModel struct {
	AppSlug   types.String `tfsdk:"app_slug"`
	Scope     types.String `tfsdk:"scope"`
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
	Kid       types.String `tfsdk:"kid"`
	ValueHash types.String `tfsdk:"value_hash"`
}

func newSecretResource() resource.Resource {
	return &secretResource{}
}

func (r *secretResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app that owns the secret.",
				MarkdownDescription: "Slug of the Gregale app that owns the secret.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"scope": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Environment scope for the secret. Defaults to `default`.",
				MarkdownDescription: "Environment scope for the secret. Defaults to `default`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required:            true,
				Description:         "Secret key, such as DATABASE_URL or API_TOKEN.",
				MarkdownDescription: "Secret key, such as `DATABASE_URL` or `API_TOKEN`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"value": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Description:         "Secret plaintext. Requires Terraform 1.11 or later and is never stored in plan or state.",
				MarkdownDescription: "Secret plaintext. Requires Terraform 1.11 or later and is never stored in plan or state.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the secret was created.",
				MarkdownDescription: "RFC3339 timestamp when the secret was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the secret was last changed.",
				MarkdownDescription: "RFC3339 timestamp when the secret was last changed.",
			},
			"kid": schema.StringAttribute{
				Computed:            true,
				Description:         "Host key identity that sealed the current secret value.",
				MarkdownDescription: "Host key identity that sealed the current secret value.",
			},
			"value_hash": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				Description:         "Opaque value-equality fingerprint used by Gregale for drift diagnostics.",
				MarkdownDescription: "Opaque value-equality fingerprint used by Gregale for drift diagnostics.",
			},
		},
		Description:         "Manage a Gregale app secret without storing its plaintext in Terraform state.",
		MarkdownDescription: "Manage a Gregale app secret without storing its plaintext in Terraform state.",
	}
}

func (r *secretResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 && len(parts) != 3 {
		resp.Diagnostics.AddError(
			"Invalid Gregale secret import ID",
			"Use the format <app-slug>/<key> or <app-slug>/<scope>/<key>.",
		)
		return
	}
	if parts[0] == "" || parts[len(parts)-1] == "" {
		resp.Diagnostics.AddError("Invalid Gregale secret import ID", "App slug and secret key must not be empty.")
		return
	}
	scope := defaultSecretScope
	if len(parts) == 3 {
		if parts[1] == "" || parts[1] == "__all__" {
			resp.Diagnostics.AddError("Invalid Gregale secret import ID", "Secret scope must be a concrete scope name.")
			return
		}
		scope = parts[1]
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope"), scope)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), parts[len(parts)-1])...)
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a secret.")
		return
	}
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := stringValue(plan.Scope)
	if err := r.client.setSecret(ctx, plan.AppSlug.ValueString(), scope, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		appendClientError(&resp.Diagnostics, "Could not set Gregale secret", err)
		return
	}
	r.refreshState(ctx, &resp.Diagnostics, &resp.State, plan)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a secret.")
		return
	}
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := stringValue(state.Scope)
	metadata, found, err := r.client.getSecret(ctx, state.AppSlug.ValueString(), scope, state.Key.ValueString())
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale secret metadata", err)
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setSecretModel(ctx, &resp.State, metadata, state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating a secret.")
		return
	}
	var plan secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scope := stringValue(plan.Scope)
	if err := r.client.setSecret(ctx, plan.AppSlug.ValueString(), scope, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale secret", err)
		return
	}
	r.refreshState(ctx, &resp.Diagnostics, &resp.State, plan)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a secret.")
		return
	}
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteSecret(ctx, state.AppSlug.ValueString(), stringValue(state.Scope), state.Key.ValueString()); err != nil && !isSecretNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale secret", err)
	}
}

func (r *secretResource) refreshState(ctx context.Context, diags *diag.Diagnostics, state *tfsdk.State, fallback secretModel) {
	metadata, found, err := r.client.getSecret(ctx, fallback.AppSlug.ValueString(), stringValue(fallback.Scope), fallback.Key.ValueString())
	if err != nil {
		appendClientError(diags, "Secret was set but metadata refresh failed", err)
		return
	}
	if !found {
		diags.AddError("Secret was set but could not be read", "Gregale accepted the write, but the secret was not returned by the metadata endpoint.")
		return
	}
	diags.Append(setSecretModel(ctx, state, metadata, fallback)...)
}

func setSecretModel(ctx context.Context, state *tfsdk.State, metadata secretMetadata, fallback secretModel) diag.Diagnostics {
	scope := metadata.Scope
	if scope == "" {
		scope = fallback.Scope.ValueString()
	}
	if scope == "" {
		scope = defaultSecretScope
	}
	model := secretModel{
		AppSlug:   fallback.AppSlug,
		Scope:     types.StringValue(scope),
		Key:       remoteString(metadata.Key, fallback.Key),
		Value:     types.StringNull(),
		CreatedAt: types.StringValue(metadata.CreatedAt),
		UpdatedAt: types.StringValue(metadata.UpdatedAt),
		Kid:       types.StringValue(metadata.Kid),
		ValueHash: types.StringValue(metadata.ValueHash),
	}
	return state.Set(ctx, &model)
}

func isSecretNotFound(err error) bool {
	if isNotFound(err) {
		return true
	}
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.code == "secret_not_found"
}
