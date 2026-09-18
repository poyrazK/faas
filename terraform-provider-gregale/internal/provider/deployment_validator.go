package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

type deploymentSourceValidator struct{}

func (deploymentSourceValidator) Description(context.Context) string {
	return "Requires either a digest-pinned OCI image or both a GitHub repository and ref."
}

func (deploymentSourceValidator) MarkdownDescription(context.Context) string {
	return "Requires either a digest-pinned OCI image or both a GitHub repository and ref."
}

func (deploymentSourceValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config deploymentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Image.IsUnknown() || config.Repo.IsUnknown() || config.Ref.IsUnknown() || config.NoTriggers.IsUnknown() {
		return
	}

	imageSet := !config.Image.IsNull() && config.Image.ValueString() != ""
	repoSet := !config.Repo.IsNull() && config.Repo.ValueString() != ""
	refSet := !config.Ref.IsNull() && config.Ref.ValueString() != ""

	if imageSet {
		if repoSet || refSet {
			resp.Diagnostics.AddAttributeError(
				path.Root("image"),
				"Conflicting Gregale deployment sources",
				"Set either image or both repo and ref, not both.",
			)
		}
		if !config.NoTriggers.IsNull() && config.NoTriggers.ValueBool() {
			resp.Diagnostics.AddAttributeError(
				path.Root("no_triggers"),
				"Invalid OCI deployment option",
				"no_triggers applies only to GitHub source-ref deployments; remove it when using image.",
			)
		}
		return
	}

	if !repoSet || !refSet {
		resp.Diagnostics.AddAttributeError(
			path.Root("image"),
			"Missing Gregale deployment source",
			"Set image, or set both repo and ref for a GitHub source-ref deployment.",
		)
	}
}
