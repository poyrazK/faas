from typing import Literal

PlatformTenantSurfaceDeploymentChangedWebhookPayloadDeploymentStatus = Literal["failed", "live"]

PLATFORM_TENANT_SURFACE_DEPLOYMENT_CHANGED_WEBHOOK_PAYLOAD_DEPLOYMENT_STATUS_VALUES: set[
    PlatformTenantSurfaceDeploymentChangedWebhookPayloadDeploymentStatus
] = {
    "failed",
    "live",
}


def check_platform_tenant_surface_deployment_changed_webhook_payload_deployment_status(
    value: str,
) -> PlatformTenantSurfaceDeploymentChangedWebhookPayloadDeploymentStatus:
    if value in PLATFORM_TENANT_SURFACE_DEPLOYMENT_CHANGED_WEBHOOK_PAYLOAD_DEPLOYMENT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_SURFACE_DEPLOYMENT_CHANGED_WEBHOOK_PAYLOAD_DEPLOYMENT_STATUS_VALUES!r}"
    )
