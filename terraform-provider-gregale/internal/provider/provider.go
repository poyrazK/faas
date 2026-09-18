package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const defaultBaseURL = "https://api.gregale.dev"

type gregaleProvider struct {
	version string
}

type providerConfig struct {
	Token   types.String `tfsdk:"token"`
	BaseURL types.String `tfsdk:"base_url"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &gregaleProvider{version: version}
	}
}

func (p *gregaleProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "gregale"
	resp.Version = p.version
}

func (p *gregaleProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Attributes: map[string]providerschema.Attribute{
			"token": providerschema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				Description:         "Gregale API bearer token. Defaults to GREGALE_TOKEN.",
				MarkdownDescription: "Gregale API bearer token. Defaults to `GREGALE_TOKEN`.",
			},
			"base_url": providerschema.StringAttribute{
				Optional:            true,
				Description:         "Gregale API base URL. Defaults to GREGALE_BASE_URL or https://api.gregale.dev.",
				MarkdownDescription: "Gregale API base URL. Defaults to `GREGALE_BASE_URL` or `https://api.gregale.dev`.",
			},
		},
		Description:         "Manage Gregale applications, deployments, raw TCP listeners, static egress IPs, private-network attachments, environment variables, alert rules, scheduled invocations, app secrets, and custom domains, and inspect existing apps, deployments, and project environments.",
		MarkdownDescription: "Manage Gregale applications, deployments, raw TCP listeners, static egress IPs, private-network attachments, environment variables, alert rules, scheduled invocations, app secrets, and custom domains, and inspect existing apps, deployments, and project environments.",
	}
}

func (p *gregaleProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerConfig
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := config.Token.ValueString()
	if config.Token.IsNull() || token == "" {
		token = os.Getenv("GREGALE_TOKEN")
	}
	if token == "" {
		resp.Diagnostics.AddError(
			"Missing Gregale API token",
			"Set the token argument or GREGALE_TOKEN before configuring the provider.",
		)
		return
	}

	baseURL := config.BaseURL.ValueString()
	if config.BaseURL.IsNull() || baseURL == "" {
		baseURL = os.Getenv("GREGALE_BASE_URL")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	client, err := newClient(baseURL, token)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Gregale provider configuration", err.Error())
		return
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *gregaleProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newAppResource,
		newDomainResource,
		newAlertResource,
		newCronResource,
		newSecretResource,
		newEnvResource,
		newDeploymentResource,
		newTCPListenerResource,
		newStaticEgressIPResource,
		newPrivateNetworkAttachmentResource,
	}
}

func (p *gregaleProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newAppDataSource,
		newDeploymentDataSource,
		newLatestDeploymentDataSource,
		newProjectEnvironmentDataSource,
	}
}

func appendClientError(respDiags *diag.Diagnostics, summary string, err error) {
	if err == nil {
		return
	}
	respDiags.AddError(summary, err.Error())
}
