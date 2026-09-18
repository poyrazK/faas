package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type domainResource struct {
	client *client
}

type domainModel struct {
	Domain             types.String `tfsdk:"domain"`
	AppID              types.String `tfsdk:"app_id"`
	ChallengeToken     types.String `tfsdk:"challenge_token"`
	TXTRecord          types.String `tfsdk:"txt_record"`
	Verified           types.Bool   `tfsdk:"verified"`
	VerificationStatus types.String `tfsdk:"verification_status"`
	VerifiedAt         types.String `tfsdk:"verified_at"`
	Default            types.Bool   `tfsdk:"default"`
	CertStatus         types.String `tfsdk:"cert_status"`
	CertExpiresAt      types.String `tfsdk:"cert_expires_at"`
	CertSANs           types.List   `tfsdk:"cert_sans"`
	CertLastError      types.String `tfsdk:"cert_last_error"`
	DNSLastCheckedAt   types.String `tfsdk:"dns_last_checked_at"`
}

func newDomainResource() resource.Resource {
	return &domainResource{}
}

func (r *domainResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = "gregale_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"domain": schema.StringAttribute{
				Required:            true,
				Description:         "Custom hostname to bind to the app.",
				MarkdownDescription: "Custom hostname to bind to the app. DNS verification is asynchronous.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"app_id": schema.StringAttribute{
				Required:            true,
				Description:         "Stable Gregale app identifier that owns the hostname.",
				MarkdownDescription: "Stable Gregale app identifier that owns the hostname.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"challenge_token": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				Description:         "DNS verification challenge token returned when the domain is created.",
				MarkdownDescription: "DNS verification challenge token returned when the domain is created.",
			},
			"txt_record": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				Description:         "Complete TXT record value to publish for verification.",
				MarkdownDescription: "Complete TXT record value to publish for verification.",
			},
			"verified": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether Gregale has verified the domain's DNS configuration.",
				MarkdownDescription: "Whether Gregale has verified the domain's DNS configuration.",
			},
			"verification_status": schema.StringAttribute{
				Computed:            true,
				Description:         "Verification lifecycle status: pending or verified.",
				MarkdownDescription: "Verification lifecycle status: `pending` or `verified`.",
			},
			"verified_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when DNS verification completed.",
				MarkdownDescription: "RFC3339 timestamp when DNS verification completed.",
			},
			"default": schema.BoolAttribute{
				Computed:            true,
				Description:         "Whether this is the app's default custom domain.",
				MarkdownDescription: "Whether this is the app's default custom domain.",
			},
			"cert_status": schema.StringAttribute{
				Computed:            true,
				Description:         "Durable TLS status, such as pending, issued, renewing, or failed.",
				MarkdownDescription: "Durable TLS status, such as `pending`, `issued`, `renewing`, or `failed`.",
			},
			"cert_expires_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp when the current certificate expires.",
				MarkdownDescription: "RFC3339 timestamp when the current certificate expires.",
			},
			"cert_sans": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				Description:         "DNS names covered by the current certificate.",
				MarkdownDescription: "DNS names covered by the current certificate.",
			},
			"cert_last_error": schema.StringAttribute{
				Computed:            true,
				Description:         "Latest durable certificate issuance error, if any.",
				MarkdownDescription: "Latest durable certificate issuance error, if any.",
			},
			"dns_last_checked_at": schema.StringAttribute{
				Computed:            true,
				Description:         "RFC3339 timestamp of the latest DNS observation.",
				MarkdownDescription: "RFC3339 timestamp of the latest DNS observation.",
			},
		},
		Description:         "Bind a custom hostname to a Gregale app and observe DNS/TLS verification.",
		MarkdownDescription: "Bind a custom hostname to a Gregale app and observe DNS/TLS verification.",
	}
}

func (r *domainResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("domain"), req.ID)...)
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before creating a domain.")
		return
	}
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.createDomain(ctx, domainRequest{
		Domain: plan.Domain.ValueString(),
		AppID:  plan.AppID.ValueString(),
	})
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not create Gregale domain", err)
		return
	}
	resp.Diagnostics.Append(setDomainModel(ctx, &resp.State, out, plan)...)
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before reading a domain.")
		return
	}
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.getDomain(ctx, state.Domain.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		appendClientError(&resp.Diagnostics, "Could not read Gregale domain", err)
		return
	}
	resp.Diagnostics.Append(setDomainModel(ctx, &resp.State, out, state)...)
}

func (r *domainResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Domain updates require replacement",
		"The domain hostname and owning app are immutable; Terraform should plan a replacement instead.",
	)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.client == nil {
		resp.Diagnostics.AddError("Unconfigured Gregale provider", "Configure the provider before deleting a domain.")
		return
	}
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.deleteDomain(ctx, state.Domain.ValueString()); err != nil && !isNotFound(err) {
		appendClientError(&resp.Diagnostics, "Could not delete Gregale domain", err)
	}
}

func setDomainModel(ctx context.Context, state *tfsdk.State, out domainResponse, fallback domainModel) diag.Diagnostics {
	domain := fallback.Domain.ValueString()
	if domain == "" {
		domain = out.Domain
	}
	appID := out.AppID
	if appID == "" {
		appID = fallback.AppID.ValueString()
	}
	certSANs, diags := types.ListValueFrom(ctx, types.StringType, out.CertSANs)
	if diags.HasError() {
		return diags
	}
	verificationStatus := "pending"
	if out.Verified {
		verificationStatus = "verified"
	}
	certExpiresAt := out.CertExpiresAt
	if certExpiresAt == "" {
		certExpiresAt = out.CertNotAfter
	}
	model := domainModel{
		Domain:             types.StringValue(domain),
		AppID:              types.StringValue(appID),
		ChallengeToken:     remoteString(out.ChallengeToken, fallback.ChallengeToken),
		TXTRecord:          remoteString(out.TXTRecord, fallback.TXTRecord),
		Verified:           types.BoolValue(out.Verified),
		VerificationStatus: types.StringValue(verificationStatus),
		VerifiedAt:         types.StringValue(out.VerifiedAt),
		Default:            types.BoolValue(out.Default),
		CertStatus:         types.StringValue(out.CertStatus),
		CertExpiresAt:      types.StringValue(certExpiresAt),
		CertSANs:           certSANs,
		CertLastError:      types.StringValue(out.CertLastError),
		DNSLastCheckedAt:   types.StringValue(out.DNSLastCheckedAt),
	}
	diags.Append(state.Set(ctx, &model)...)
	return diags
}
