from typing import Literal

OrgAppDeploymentSummaryKind = Literal["dockerfile", "github", "image", "preview", "tarball"]

ORG_APP_DEPLOYMENT_SUMMARY_KIND_VALUES: set[OrgAppDeploymentSummaryKind] = {
    "dockerfile",
    "github",
    "image",
    "preview",
    "tarball",
}


def check_org_app_deployment_summary_kind(value: str) -> OrgAppDeploymentSummaryKind:
    if value in ORG_APP_DEPLOYMENT_SUMMARY_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_APP_DEPLOYMENT_SUMMARY_KIND_VALUES!r}")
