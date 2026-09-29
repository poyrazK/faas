from typing import Literal

OrgAppDeploymentSummaryStatus = Literal[
    "building", "cancelled", "failed", "imaging", "live", "pending", "snapshotting", "superseded"
]

ORG_APP_DEPLOYMENT_SUMMARY_STATUS_VALUES: set[OrgAppDeploymentSummaryStatus] = {
    "building",
    "cancelled",
    "failed",
    "imaging",
    "live",
    "pending",
    "snapshotting",
    "superseded",
}


def check_org_app_deployment_summary_status(value: str) -> OrgAppDeploymentSummaryStatus:
    if value in ORG_APP_DEPLOYMENT_SUMMARY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ORG_APP_DEPLOYMENT_SUMMARY_STATUS_VALUES!r}")
