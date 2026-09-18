package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	deploymentPollInterval = 2 * time.Second
	deploymentWaitTimeout  = 30 * time.Minute
)

type deploymentResource struct {
	client *client
}

type deploymentModel struct {
	AppSlug      types.String `tfsdk:"app_slug"`
	Image        types.String `tfsdk:"image"`
	Repo         types.String `tfsdk:"repo"`
	Ref          types.String `tfsdk:"ref"`
	Environment  types.String `tfsdk:"environment"`
	NoTriggers   types.Bool   `tfsdk:"no_triggers"`
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

func newDeploymentResource() resource.Resource {
	return &deploymentResource{}
}

func (r *deploymentResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_deployment"
}

func (r *deploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app to deploy.",
				MarkdownDescription: "Slug of the Gregale app to deploy.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"image": schema.StringAttribute{
				Optional:            true,
				Description:         "Digest-pinned OCI image reference to deploy. Set this or both `repo` and `ref`.",
				MarkdownDescription: "Digest-pinned OCI image reference to deploy. Set this or both `repo` and `ref`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"repo": schema.StringAttribute{
				Optional:            true,
				Description:         "GitHub repository slug, for example `acme/orders-api`.",
				MarkdownDescription: "GitHub repository slug, for example `acme/orders-api`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"ref": schema.StringAttribute{
				Optional:            true,
				Description:         "Git branch, tag, short commit SHA, or full commit SHA to deploy.",
				MarkdownDescription: "Git branch, tag, short commit SHA, or full commit SHA to deploy.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"environment": schema.StringAttribute{
				Optional:            true,
				Description:         "Registered Gregale project environment to target.",
				MarkdownDescription: "Registered Gregale project environment to target.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"no_triggers": schema.BoolAttribute{
				Optional:            true,
				Description:         "Skip trigger declarations found in gregale.yaml.",
				MarkdownDescription: "Skip trigger declarations found in `gregale.yaml`.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"deployment_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Immutable Gregale deployment identifier.",
				MarkdownDescription: "Immutable Gregale deployment identifier.",
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
		Description:         "Deploy a digest-pinned OCI image or GitHub source ref to Gregale and expose lifecycle and preview metadata.",
		MarkdownDescription: "Deploy a digest-pinned OCI image or GitHub source ref to Gregale and expose lifecycle and preview metadata.",
	}
}

func (r *deploymentResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{deploymentSourceValidator{}}
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *deploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Gregale deployment import ID",
			"Use the format <app-slug>/<deployment-id>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("deployment_id"), parts[1])...)
}

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a deployment.")
		return
	}
	var plan deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out deploymentResponse
	var err error
	if plan.Image.ValueString() != "" {
		out, err = r.client.createImageDeployment(ctx, plan.AppSlug.ValueString(), imageDeploymentRequest{
			Image:       plan.Image.ValueString(),
			Environment: stringValue(plan.Environment),
		})
	} else {
		out, err = r.client.createSourceRefDeployment(ctx, plan.AppSlug.ValueString(), deploymentRequest{
			Repo:        plan.Repo.ValueString(),
			Ref:         plan.Ref.ValueString(),
			Environment: stringValue(plan.Environment),
			NoTriggers:  plan.NoTriggers.ValueBool(),
		})
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale deployment", err)
		return
	}
	final, preview, waitErr := r.waitForDeployment(ctx, out.ID)
	if final.ID == "" {
		final = out
	}
	resp.Diagnostics.Append(setDeploymentModel(ctx, &resp.State, final, preview, plan)...)
	if waitErr != nil {
		appendClientError(&resp.Diagnostics, "Could not wait for Gregale deployment", waitErr)
		return
	}
	if final.Status != "live" {
		resp.Diagnostics.AddError(
			"Gregale deployment did not become live",
			deploymentFailureDetail(final),
		)
	}
}

func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a deployment.")
		return
	}
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getDeployment(ctx, state.DeploymentID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale deployment", err)
		return
	}
	preview, err := r.client.getDeploymentURL(ctx, state.DeploymentID.ValueString())
	if err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not read Gregale deployment preview URL", err)
		return
	}
	resp.Diagnostics.Append(setDeploymentModel(ctx, &resp.State, out, preview, state)...)
}

func (r *deploymentResource) Update(ctx context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Gregale deployments are immutable", "Change the image, source ref, or deployment inputs to create a new deployment.")
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a deployment.")
		return
	}
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	status := stringValue(state.Status)
	if status != "pending" && status != "building" && status != "imaging" && status != "snapshotting" {
		return
	}
	_, err := r.client.cancelDeployment(ctx, state.AppSlug.ValueString(), state.DeploymentID.ValueString(), "user")
	if err != nil && !isDeploymentCancellationRace(err) {
		appendClientError(&resp.Diagnostics, "Could not cancel Gregale deployment", err)
	}
}

func (r *deploymentResource) waitForDeployment(ctx context.Context, deploymentID string) (deploymentResponse, deploymentURLResponse, error) {
	waitCtx, cancel := context.WithTimeout(ctx, deploymentWaitTimeout)
	defer cancel()
	var last deploymentResponse
	for {
		out, err := r.client.getDeployment(waitCtx, deploymentID)
		if err != nil {
			return last, deploymentURLResponse{}, err
		}
		last = out
		switch out.Status {
		case "live", "failed", "superseded", "cancelled":
			preview, previewErr := r.client.getDeploymentURL(waitCtx, deploymentID)
			if previewErr != nil && !isNotFound(previewErr) {
				return out, deploymentURLResponse{}, previewErr
			}
			return out, preview, nil
		}
		timer := time.NewTimer(deploymentPollInterval)
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return last, deploymentURLResponse{}, fmt.Errorf("timed out after %s", deploymentWaitTimeout)
			}
			return last, deploymentURLResponse{}, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func setDeploymentModel(ctx context.Context, state *tfsdk.State, out deploymentResponse, preview deploymentURLResponse, fallback deploymentModel) diag.Diagnostics {
	stageState := string(out.StageState)
	if stageState == "null" {
		stageState = ""
	}
	model := deploymentModel{
		AppSlug:      fallback.AppSlug,
		Image:        fallback.Image,
		Repo:         fallback.Repo,
		Ref:          fallback.Ref,
		Environment:  fallback.Environment,
		NoTriggers:   fallback.NoTriggers,
		DeploymentID: remoteString(out.ID, fallback.DeploymentID),
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
	return state.Set(ctx, &model)
}

func deploymentFailureDetail(out deploymentResponse) string {
	parts := make([]string, 0, 3)
	if out.ErrorCode != "" {
		parts = append(parts, out.ErrorCode)
	}
	if out.Error != "" {
		parts = append(parts, out.Error)
	}
	if out.ErrorFix != "" {
		parts = append(parts, "fix: "+out.ErrorFix)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Deployment ended with status %q.", out.Status)
	}
	return strings.Join(parts, "; ")
}

func isDeploymentCancellationRace(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.code == "deployment_cancel_not_cancellable" || apiErr.code == "deployment_cancel_live_forbidden" || isNotFound(err)
}
