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

type alertResource struct {
	client *client
}

type alertModel struct {
	AlertID             types.String  `tfsdk:"alert_id"`
	AppSlug             types.String  `tfsdk:"app_slug"`
	Name                types.String  `tfsdk:"name"`
	Enabled             types.Bool    `tfsdk:"enabled"`
	Metric              types.String  `tfsdk:"metric"`
	Comparison          types.String  `tfsdk:"comparison"`
	Threshold           types.Float64 `tfsdk:"threshold"`
	WindowSpec          types.String  `tfsdk:"window_spec"`
	FailureSource       types.String  `tfsdk:"failure_source"`
	Action              types.String  `tfsdk:"action"`
	WebhookURL          types.String  `tfsdk:"webhook_url"`
	WebhookSecret       types.String  `tfsdk:"webhook_secret"`
	WebhookSecretMasked types.String  `tfsdk:"webhook_secret_masked"`
	CooldownMinutes     types.Int64   `tfsdk:"cooldown_minutes"`
	EvaluationState     types.String  `tfsdk:"state"`
	AppID               types.String  `tfsdk:"app_id"`
	LastFiredAt         types.String  `tfsdk:"last_fired_at"`
	LastEvaluatedAt     types.String  `tfsdk:"last_evaluated_at"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
}

func newAlertResource() resource.Resource {
	return &alertResource{}
}

func (r *alertResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_alert"
}

func (r *alertResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"alert_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable alert rule identifier.",
				MarkdownDescription: "Gregale's immutable alert rule identifier.",
			},
			"app_slug": schema.StringAttribute{
				Required:            true,
				Description:         "Slug of the Gregale app that owns the alert rule.",
				MarkdownDescription: "Slug of the Gregale app that owns the alert rule.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				Description:         "Unique alert rule name within the app.",
				MarkdownDescription: "Unique alert rule name within the app.",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Whether Gregale evaluates this alert rule.",
				MarkdownDescription: "Whether Gregale evaluates this alert rule.",
			},
			"metric": schema.StringAttribute{
				Required:            true,
				Description:         "Metric to evaluate, such as error_rate_pct or latency_p95_ms.",
				MarkdownDescription: "Metric to evaluate, such as `error_rate_pct` or `latency_p95_ms`. Changing it forces replacement because metric families are immutable.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"comparison": schema.StringAttribute{
				Required:            true,
				Description:         "Comparison operator: gt, gte, lt, or lte.",
				MarkdownDescription: "Comparison operator: `gt`, `gte`, `lt`, or `lte`.",
			},
			"threshold": schema.Float64Attribute{
				Required:            true,
				Description:         "Metric threshold that causes the alert to fire.",
				MarkdownDescription: "Metric threshold that causes the alert to fire.",
			},
			"window_spec": schema.StringAttribute{
				Required:            true,
				Description:         "Evaluation window, such as 5m, 1h, or 24h.",
				MarkdownDescription: "Evaluation window, such as `5m`, `1h`, or `24h`.",
			},
			"failure_source": schema.StringAttribute{
				Optional:            true,
				Description:         "Invocation source filter for the failed_invocations metric.",
				MarkdownDescription: "Invocation source filter for `failed_invocations`: `any`, `cron`, `queue`, `delayed_task`, `async_invoke`, or `inbound_webhook`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"action": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Description:         "Action on fire: webhook, rollback, demote, or promote.",
				MarkdownDescription: "Action on fire: `webhook`, `rollback`, `demote`, or `promote`.",
			},
			"webhook_url": schema.StringAttribute{
				Required:            true,
				Description:         "HTTPS endpoint that receives alert deliveries.",
				MarkdownDescription: "HTTPS endpoint that receives alert deliveries.",
			},
			"webhook_secret": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Description:         "HMAC secret for alert deliveries. Requires Terraform 1.11 or later and is never stored in plan or state.",
				MarkdownDescription: "HMAC secret for alert deliveries. Requires Terraform 1.11 or later and is never stored in plan or state.",
			},
			"webhook_secret_masked": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				Description:         "Masked indicator showing that Gregale has a webhook secret.",
				MarkdownDescription: "Masked indicator showing that Gregale has a webhook secret.",
			},
			"cooldown_minutes": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Description:         "Minimum minutes between alert deliveries.",
				MarkdownDescription: "Minimum minutes between alert deliveries. Gregale accepts values from 5 through 1440.",
			},
			"state": schema.StringAttribute{
				Computed:            true,
				Description:         "Current evaluation state, such as ok or firing.",
				MarkdownDescription: "Current evaluation state, such as `ok` or `firing`.",
			},
			"app_id": schema.StringAttribute{
				Computed:            true,
				Description:         "Gregale's immutable app identifier.",
				MarkdownDescription: "Gregale's immutable app identifier.",
			},
			"last_fired_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp of the most recent alert firing.",
				MarkdownDescription: "RFC3339 timestamp of the most recent alert firing.",
			},
			"last_evaluated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp of the most recent evaluation.",
				MarkdownDescription: "RFC3339 timestamp of the most recent evaluation.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the alert rule was created.",
				MarkdownDescription: "RFC3339 timestamp when the alert rule was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the alert rule was last changed.",
				MarkdownDescription: "RFC3339 timestamp when the alert rule was last changed.",
			},
		},
		Description:         "Manage a Gregale alert rule and observe its evaluation state.",
		MarkdownDescription: "Manage a Gregale alert rule and observe its evaluation state.",
	}
}

func (r *alertResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *alertResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Gregale alert import ID",
			"Use the format <app-slug>/<alert-id>.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("app_slug"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("alert_id"), parts[1])...)
}

func (r *alertResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating an alert.")
		return
	}
	var plan alertModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createAlertRule(ctx, plan.AppSlug.ValueString(), alertRuleRequest{
		Name:            plan.Name.ValueString(),
		Enabled:         boolPointer(plan.Enabled),
		Metric:          plan.Metric.ValueString(),
		Comparison:      plan.Comparison.ValueString(),
		Threshold:       plan.Threshold.ValueFloat64(),
		WindowSpec:      plan.WindowSpec.ValueString(),
		FailureSource:   stringValue(plan.FailureSource),
		Action:          stringPointer(plan.Action),
		WebhookURL:      plan.WebhookURL.ValueString(),
		WebhookSecret:   plan.WebhookSecret.ValueString(),
		CooldownMinutes: intPointer(plan.CooldownMinutes),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale alert", err)
		return
	}
	resp.Diagnostics.Append(setAlertModel(ctx, &resp.State, out, plan)...)
}

func (r *alertResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading an alert.")
		return
	}
	var state alertModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getAlertRule(ctx, state.AppSlug.ValueString(), state.AlertID.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale alert", err)
		return
	}
	resp.Diagnostics.Append(setAlertModel(ctx, &resp.State, out, state)...)
}

func (r *alertResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before updating an alert.")
		return
	}
	var plan alertModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.updateAlertRule(ctx, plan.AppSlug.ValueString(), plan.AlertID.ValueString(), alertRulePatch{
		Name:            stringPointer(plan.Name),
		Enabled:         boolPointer(plan.Enabled),
		Metric:          stringPointer(plan.Metric),
		Comparison:      stringPointer(plan.Comparison),
		Threshold:       floatPointer(plan.Threshold),
		WindowSpec:      stringPointer(plan.WindowSpec),
		Action:          stringPointer(plan.Action),
		WebhookURL:      stringPointer(plan.WebhookURL),
		WebhookSecret:   stringPointer(plan.WebhookSecret),
		CooldownMinutes: intPointer(plan.CooldownMinutes),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not update Gregale alert", err)
		return
	}
	resp.Diagnostics.Append(setAlertModel(ctx, &resp.State, out, plan)...)
}

func (r *alertResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting an alert.")
		return
	}
	var state alertModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteAlertRule(ctx, state.AppSlug.ValueString(), state.AlertID.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale alert", err)
	}
}

func setAlertModel(ctx context.Context, state *tfsdk.State, out alertRuleResponse, fallback alertModel) diag.Diagnostics {
	alertID := out.ID
	if alertID == "" {
		alertID = fallback.AlertID.ValueString()
	}
	appSlug := fallback.AppSlug.ValueString()
	appID := out.AppID
	if appID == "" {
		appID = fallback.AppID.ValueString()
	}
	model := alertModel{
		AlertID:             types.StringValue(alertID),
		AppSlug:             types.StringValue(appSlug),
		Name:                remoteString(out.Name, fallback.Name),
		Enabled:             types.BoolValue(out.Enabled),
		Metric:              remoteString(out.Metric, fallback.Metric),
		Comparison:          remoteString(out.Comparison, fallback.Comparison),
		Threshold:           types.Float64Value(out.Threshold),
		WindowSpec:          remoteString(out.WindowSpec, fallback.WindowSpec),
		FailureSource:       remoteString(out.FailureSource, fallback.FailureSource),
		Action:              remoteString(out.Action, fallback.Action),
		WebhookURL:          remoteString(out.WebhookURL, fallback.WebhookURL),
		WebhookSecret:       types.StringNull(),
		WebhookSecretMasked: types.StringValue(out.WebhookSecretSealedMasked),
		CooldownMinutes:     types.Int64Value(int64(out.CooldownMinutes)),
		EvaluationState:     types.StringValue(out.State),
		AppID:               types.StringValue(appID),
		LastFiredAt:         types.StringValue(out.LastFiredAt),
		LastEvaluatedAt:     types.StringValue(out.LastEvaluatedAt),
		CreatedAt:           types.StringValue(out.CreatedAt),
		UpdatedAt:           types.StringValue(out.UpdatedAt),
	}
	return state.Set(ctx, &model)
}

func floatPointer(value types.Float64) *float64 {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	out := value.ValueFloat64()
	return &out
}
