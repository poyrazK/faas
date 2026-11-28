from typing import Literal

PlatformTenantSelfDeploymentResponseStatus = Literal[
    "building", "cancelled", "failed", "imaging", "live", "pending", "snapshotting", "superseded"
]

PLATFORM_TENANT_SELF_DEPLOYMENT_RESPONSE_STATUS_VALUES: set[PlatformTenantSelfDeploymentResponseStatus] = {
    "building",
    "cancelled",
    "failed",
    "imaging",
    "live",
    "pending",
    "snapshotting",
    "superseded",
}


def check_platform_tenant_self_deployment_response_status(value: str) -> PlatformTenantSelfDeploymentResponseStatus:
    if value in PLATFORM_TENANT_SELF_DEPLOYMENT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SELF_DEPLOYMENT_RESPONSE_STATUS_VALUES!r}"
    )
